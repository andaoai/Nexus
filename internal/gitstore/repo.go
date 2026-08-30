package gitstore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// repo.go：本地 bare 仓库管理。clone/fetch/push/建树/提交全部走系统 git
// plumbing（无需工作区，bare 仓库可用），保证行为与真实 git 完全一致。

// Repo 本地 bare 仓库封装。
type Repo struct {
	dir    string // bare 仓库目录（绝对路径）
	remote string // 带凭据的 origin URL
	branch string
}

// OpenRepo 打开（或 clone）本地 bare 仓库缓存。
func OpenRepo(ctx context.Context, repoURL, token, dir, branch string) (*Repo, error) {
	r := &Repo{dir: dir, branch: branch}
	r.remote = authedURL(repoURL, token)

	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		// 已有缓存：更新 remote（token 可能轮换）后 fetch
		if err := r.git(ctx, "remote", "set-url", "origin", r.remote); err != nil {
			return nil, err
		}
		if err := r.Fetch(ctx); err != nil {
			return nil, fmt.Errorf("fetch 缓存仓库: %w", err)
		}
		return r, nil
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if err := r.git(ctx, "clone", "--bare", r.remote, dir); err != nil {
		return nil, fmt.Errorf("clone %s: %w", repoURL, err)
	}
	return r, nil
}

// remoteRef 远端分支在本地的跟踪引用（bare clone 不建 refs/remotes/origin，
// 显式 fetch 到自定义引用，稳定可靠）。
func (r *Repo) remoteRef() string {
	return "refs/nexus/origin/" + r.branch
}

// Fetch 拉取远端到跟踪引用；本地无未推送提交时才 fast-forward 本地分支。
func (r *Repo) Fetch(ctx context.Context) error {
	return r.git(ctx, "fetch", "origin", "+refs/heads/"+r.branch+":"+r.remoteRef())
}

// HeadRev 当前本地分支 head 的 commit sha。
func (r *Repo) HeadRev(ctx context.Context) string {
	out, err := r.gitOut(ctx, "rev-parse", "--verify", "refs/heads/"+r.branch)
	if err != nil {
		return ""
	}
	return out
}

// OriginRev 远端分支的 commit sha（上次 fetch 的结果）。
func (r *Repo) OriginRev(ctx context.Context) string {
	out, err := r.gitOut(ctx, "rev-parse", "--verify", r.remoteRef())
	if err != nil {
		return ""
	}
	return out
}

// Push 推送本地分支到远端。
func (r *Repo) Push(ctx context.Context) error {
	return r.git(ctx, "push", "origin", r.branch)
}

// Unpushed 本地是否领先远端。
func (r *Repo) Unpushed(ctx context.Context) bool {
	local, remote := r.HeadRev(ctx), r.OriginRev(ctx)
	return local != "" && local != remote
}

// CommitFiles 用 plumbing 在 parent 之上写入/删除文件并提交，返回新 commit sha。
// parent 为空则用当前 head。全程不依赖工作区。
func (r *Repo) CommitFiles(ctx context.Context, ops []FileOp, author, msg, parent string) (string, error) {
	if parent == "" {
		parent = r.HeadRev(ctx)
	}
	env := commitEnv(author)

	// 现有文件清单：path → blob sha
	blobs := map[string]string{}
	if parent != "" {
		out, err := r.gitOut(ctx, "ls-tree", "-r", "-z", parent)
		if err != nil {
			return "", fmt.Errorf("ls-tree: %w", err)
		}
		for _, e := range splitNUL(out) {
			// <mode> <type> <sha>\t<path>
			meta, path, ok := strings.Cut(e, "\t")
			if !ok || !strings.Contains(meta, " blob ") {
				continue
			}
			blobs[path] = strings.Fields(meta)[2]
		}
	}

	for _, op := range ops {
		if op.Delete {
			delete(blobs, op.Path)
			continue
		}
		out, err := r.gitIn(ctx, env, op.Data, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", fmt.Errorf("hash-object %s: %w", op.Path, err)
		}
		blobs[op.Path] = trimNL(out)
	}

	tree, err := r.writeTrees(ctx, env, blobs)
	if err != nil {
		return "", err
	}

	args := []string{"commit-tree", tree, "-m", msg}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	out, err := r.gitOutEnv(ctx, env, args...)
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", err)
	}
	commit := trimNL(out)
	if err := r.git(ctx, "update-ref", "refs/heads/"+r.branch, commit); err != nil {
		return "", err
	}
	return commit, nil
}

// writeTrees 由扁平 path→blob 清单自底向上构造目录树，返回根 tree sha。
func (r *Repo) writeTrees(ctx context.Context, env []string, blobs map[string]string) (string, error) {
	if len(blobs) == 0 {
		return "", fmt.Errorf("空树：至少需要一个文件")
	}
	lines, err := r.treeLines(ctx, env, blobs, "")
	if err != nil {
		return "", err
	}
	out, err := r.gitIn(ctx, env, []byte(strings.Join(lines, "\n")+"\n"), "mktree")
	if err != nil {
		return "", fmt.Errorf("mktree: %w", err)
	}
	return trimNL(out), nil
}

// treeLines 递归生成 mktree 输入行；子目录先递归建树拿 sha。
func (r *Repo) treeLines(ctx context.Context, env []string, blobs map[string]string, prefix string) ([]string, error) {
	dirs := map[string]bool{}
	files := map[string]string{}
	for path, sha := range blobs {
		rel := path
		if prefix != "" {
			if !strings.HasPrefix(path, prefix) {
				continue // 不属于该前缀的文件只在它自己的层级出现
			}
			rel = strings.TrimPrefix(path, prefix)
		}
		if rel == "" {
			continue
		}
		if name, _, isDir := strings.Cut(rel, "/"); isDir {
			dirs[name+"/"] = true
		} else {
			files[name] = sha
		}
	}

	keys := make([]string, 0, len(dirs)+len(files))
	for k := range dirs {
		keys = append(keys, k)
	}
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []string
	for _, k := range keys {
		if strings.HasSuffix(k, "/") {
			name := strings.TrimSuffix(k, "/")
			subLines, err := r.treeLines(ctx, env, blobs, prefix+name+"/")
			if err != nil {
				return nil, err
			}
			out, err := r.gitIn(ctx, env, []byte(strings.Join(subLines, "\n")+"\n"), "mktree")
			if err != nil {
				return nil, fmt.Errorf("mktree %s%s: %w", prefix, name, err)
			}
			lines = append(lines, "040000 tree "+trimNL(out)+"\t"+name)
		} else {
			lines = append(lines, "100644 blob "+files[k]+"\t"+k)
		}
	}
	return lines, nil
}

// Lock 独占缓存目录（flock），防同机多实例争抢。
func (r *Repo) Lock() (unlock func(), err error) {
	f, err := os.OpenFile(r.dir+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("缓存目录被其他进程占用: %w", err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// workDir 返回 git 命令的工作目录。仓库尚不存在时（clone 前）用其父目录。
func (r *Repo) workDir() string {
	if _, err := os.Stat(r.dir); err == nil {
		return r.dir
	}
	return filepath.Dir(r.dir)
}

func (r *Repo) git(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.workDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %s: %w", args, trimNL(string(out)), err)
	}
	return nil
}

func (r *Repo) gitOut(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.workDir()
	out, err := cmd.Output()
	if err != nil {
		return trimNL(string(out)), fmt.Errorf("git %v: %w", args, err)
	}
	return trimNL(string(out)), nil
}

func (r *Repo) gitOutEnv(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.workDir()
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return trimNL(string(out)), fmt.Errorf("git %v: %w", args, err)
	}
	return trimNL(string(out)), nil
}

// gitIn 带 stdin 执行。
func (r *Repo) gitIn(ctx context.Context, env []string, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.workDir()
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return trimNL(string(out)), fmt.Errorf("git %v: %s: %w", args, trimNL(string(out)), err)
	}
	return string(out), nil
}

func commitEnv(author string) []string {
	now := time.Now().Format(time.RFC3339)
	return append(os.Environ(),
		"GIT_AUTHOR_NAME="+author, "GIT_AUTHOR_EMAIL="+author+"@nexus.local",
		"GIT_COMMITTER_NAME="+author, "GIT_COMMITTER_EMAIL="+author+"@nexus.local",
		"GIT_AUTHOR_DATE="+now, "GIT_COMMITTER_DATE="+now,
	)
}

func authedURL(repoURL, token string) string {
	// https://host/... → https://<user>:<token>@host/...
	// GitHub 用 x-access-token；Gitee 等用 oauth2（token 即密码）
	const scheme = "https://"
	if len(repoURL) <= len(scheme) || repoURL[:len(scheme)] != scheme {
		return repoURL
	}
	user := "x-access-token"
	if i := strings.Index(repoURL[len(scheme):], "/"); i > 0 {
		host := repoURL[len(scheme) : len(scheme)+i]
		if !strings.Contains(host, "github.com") {
			user = "oauth2"
		}
	}
	return scheme + user + ":" + token + "@" + repoURL[len(scheme):]
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func splitNUL(s string) []string {
	return strings.Split(s, "\x00")
}
