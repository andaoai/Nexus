package gitstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/andaoai/Nexus/internal/core"
)

// ErrNotFound 实体不存在。
var ErrNotFound = errors.New("not found")

// ErrConflict 远端有新提交且重试用尽。
var ErrConflict = errors.New("push 冲突，请稍后重试")

// ErrUnavailable 远端不可达（push 网络失败）。
var ErrUnavailable = errors.New("数据仓库暂不可达")

// FileOp 一次提交内的文件操作。
type FileOp struct {
	Path   string
	Data   []byte
	Delete bool
}

// Skill AI 技能：一段提示词文档，聊天时注入 system prompt。
type Skill struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Store 数据存储接口（api 层依赖此接口，便于测试注入）。
type Store interface {
	ListCustomers(owner string) ([]core.Customer, error)
	GetCustomer(id string) (core.Customer, error)
	CreateCustomer(c core.Customer, actor string) error
	UpdateCustomer(c core.Customer, actor string) error
	DeleteCustomer(id string, actor string) error

	ListSuppliers() ([]core.Supplier, error)
	GetSupplier(id string) (core.Supplier, error)
	CreateSupplier(s core.Supplier, actor string) error
	UpdateSupplier(s core.Supplier, actor string) error

	ListSolutions() ([]core.Solution, error)
	GetSolution(id string) (core.Solution, error)
	CreateSolution(s core.Solution, actor string) error

	ListMatches() ([]core.Match, error)
	GetMatch(id string) (core.Match, error)
	CreateMatch(m core.Match, actor string) error
	UpdateMatch(m core.Match, actor string) error

	// 聊天会话（conversations/<owner>/<id>.json，整文件覆盖提交）
	ListConversations(owner string) ([]core.Conversation, error)
	GetConversation(id string) (core.Conversation, error)
	CreateConversation(c core.Conversation, actor string) error
	UpdateConversation(c core.Conversation, actor string) error

	// AI 技能（skills/<name>.md，管理员维护）
	ListSkills() ([]Skill, error)
	GetSkill(name string) (string, error)
	PutSkill(name, content, actor string) error

	// Counts 仪表盘统计。
	Counts() (customers, suppliers, solutions, matches, deals int)
	// SyncNow 立即 fetch 远端并重建索引。
	SyncNow(ctx context.Context) error
	// SyncLoop 周期同步 + 重推未推送提交，随 ctx 结束。
	SyncLoop(ctx context.Context, every time.Duration)
}

// gitStore Store 的 git 实现。
type gitStore struct {
	mu       sync.Mutex // 串行化全部写操作（3 人团队够用）
	repo     *Repo
	ix       *index
	branch   string
	pushFail int
}

// Options 打开参数。
type Options struct {
	RepoURL  string // https://github.com/owner/repo.git（不含凭据）
	Branch   string
	Token    string
	CacheDir string // 本地 bare 仓库目录
}

// Open 打开数据存储：clone/复用本地缓存并重建索引。
func Open(ctx context.Context, opt Options) (Store, error) {
	repo, err := OpenRepo(ctx, opt.RepoURL, opt.Token, opt.CacheDir, opt.Branch)
	if err != nil {
		return nil, err
	}
	unlock, err := repo.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	ix := newIndex()
	if rev := repo.HeadRev(ctx); rev != "" {
		if err := ix.Rebuild(ctx, repo, rev); err != nil {
			return nil, err
		}
	}
	return &gitStore{repo: repo, ix: ix, branch: opt.Branch}, nil
}

// SyncLoop 周期同步：fetch 远端 + 重推未推送提交。
func (s *gitStore) SyncLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.SyncNow(ctx); err != nil {
				log.Printf("[gitstore] 同步失败: %v", err)
			}
			if err := s.retryPush(ctx); err != nil {
				log.Printf("[gitstore] 重推失败: %v", err)
			}
		}
	}
}

// SyncNow 立即 fetch 并按需重建索引。
func (s *gitStore) SyncNow(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.repo.Fetch(ctx); err != nil {
		return err
	}
	remote := s.repo.OriginRev(ctx)
	if remote == "" || remote == s.ix.Rev() {
		return nil
	}
	return s.ix.Rebuild(ctx, s.repo, remote)
}

func (s *gitStore) retryPush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.repo.Unpushed(ctx) {
		return nil
	}
	if err := s.repo.Push(ctx); err != nil {
		return err
	}
	log.Printf("[gitstore] 未推送提交已重推成功")
	return nil
}

// commitAndPush 统一写路径：锁内 fetch → commit → push（被拒则重放重试 ≤3 次）→ 更新索引。
func (s *gitStore) commitAndPush(ctx context.Context, ops []FileOp, actor, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.repo.Fetch(ctx); err != nil {
			return fmt.Errorf("%w: fetch: %v", ErrUnavailable, err)
		}
		// 本地有未推送提交且远端领先：重放到新 head
		if err := s.rebaseOntoOrigin(ctx); err != nil {
			return err
		}
		if _, err := s.repo.CommitFiles(ctx, ops, actor, msg, ""); err != nil {
			return err
		}
		if err := s.repo.Push(ctx); err != nil {
			lastErr = err
			continue // fetch+rebase 后重试
		}
		s.ix.apply(ops)
		return nil
	}
	return fmt.Errorf("%w: push: %v", ErrUnavailable, lastErr)
}

// rebaseOntoOrigin 若本地有未推送提交且远端领先，将未推送的文件变更
// 重放到新远端 head 上（树手术，无需工作区；失败则跟随远端，最后写赢）。
func (s *gitStore) rebaseOntoOrigin(ctx context.Context) error {
	local, remote := s.repo.HeadRev(ctx), s.repo.OriginRev(ctx)
	if local == "" || remote == "" || local == remote {
		return nil
	}
	ops, err := s.unpushedOps(ctx, remote, local)
	if err != nil || len(ops) == 0 {
		// 不可回放：放弃本地提交，跟随远端
		log.Printf("[gitstore] 丢弃不可重放的本地提交 %s: %v", short(local), err)
		return s.repo.git(ctx, "update-ref", "refs/heads/"+s.branch, remote)
	}
	// 重放：以远端 head 为基点重建提交
	if err := s.repo.Fetch(ctx); err != nil {
		return err
	}
	remote = s.repo.OriginRev(ctx)
	if _, err := s.repo.CommitFiles(ctx, ops, "nexus", "rebase unpushed writes", remote); err != nil {
		log.Printf("[gitstore] 重放失败，跟随远端: %v", err)
		return s.repo.git(ctx, "update-ref", "refs/heads/"+s.branch, remote)
	}
	return nil
}

// unpushedOps 提取 remote..local 之间变更的文件（含删除）。
func (s *gitStore) unpushedOps(ctx context.Context, remote, local string) ([]FileOp, error) {
	out, err := gitOut(ctx, s.repo.dir, "diff", "--name-status", "-z", remote, local)
	if err != nil {
		return nil, err
	}
	fields := stringsSplitNUL(out)
	var ops []FileOp
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		op := FileOp{Path: path}
		if status == "D" {
			op.Delete = true
		} else {
			data, err := gitOutBytes(ctx, s.repo.dir, "show", local+":"+path)
			if err != nil {
				return nil, err
			}
			op.Data = data
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// ---- 实体路径 ----

func customerPath(c core.Customer) string { return dirCustomers + "/" + c.Owner + "/" + c.ID + ".json" }
func supplierPath(s core.Supplier) string { return dirSuppliers + "/" + s.ID + ".json" }
func solutionPath(s core.Solution) string { return dirSolutions + "/" + s.ID + ".json" }
func matchPath(m core.Match) string       { return dirMatches + "/" + m.ID + ".json" }
func conversationPath(c core.Conversation) string {
	return dirConversations + "/" + c.Owner + "/" + c.ID + ".json"
}

func marshal(v any) ([]byte, error) { return core.JSON(v) }

// ---- Customer ----

func (s *gitStore) ListCustomers(owner string) ([]core.Customer, error) {
	return s.ix.ListCustomers(owner), nil
}

func (s *gitStore) GetCustomer(id string) (core.Customer, error) {
	if c, ok := s.ix.GetCustomer(id); ok {
		return c, nil
	}
	return core.Customer{}, ErrNotFound
}

func (s *gitStore) CreateCustomer(c core.Customer, actor string) error {
	data, err := marshal(c)
	if err != nil {
		return err
	}
	return s.commitAndPush(context.Background(), []FileOp{{Path: customerPath(c), Data: data}},
		actor, fmt.Sprintf("%s: create customer %s", actor, c.ID))
}

func (s *gitStore) UpdateCustomer(c core.Customer, actor string) error {
	old, ok := s.ix.GetCustomer(c.ID)
	if !ok {
		return ErrNotFound
	}
	ops := []FileOp{{Path: customerPath(c), Data: mustMarshal(c)}}
	if old.Owner != c.Owner { // 归属变更：删旧路径写新路径
		ops = append(ops, FileOp{Path: customerPath(old), Delete: true})
	}
	return s.commitAndPush(context.Background(), ops,
		actor, fmt.Sprintf("%s: update customer %s", actor, c.ID))
}

func (s *gitStore) DeleteCustomer(id string, actor string) error {
	c, ok := s.ix.GetCustomer(id)
	if !ok {
		return ErrNotFound
	}
	return s.commitAndPush(context.Background(), []FileOp{{Path: customerPath(c), Delete: true}},
		actor, fmt.Sprintf("%s: delete customer %s", actor, id))
}

// ---- Supplier ----

func (s *gitStore) ListSuppliers() ([]core.Supplier, error) {
	return s.ix.ListSuppliers(), nil
}

func (s *gitStore) GetSupplier(id string) (core.Supplier, error) {
	if v, ok := s.ix.GetSupplier(id); ok {
		return v, nil
	}
	return core.Supplier{}, ErrNotFound
}

func (s *gitStore) CreateSupplier(v core.Supplier, actor string) error {
	data, err := marshal(v)
	if err != nil {
		return err
	}
	return s.commitAndPush(context.Background(), []FileOp{{Path: supplierPath(v), Data: data}},
		actor, fmt.Sprintf("%s: create supplier %s", actor, v.ID))
}

func (s *gitStore) UpdateSupplier(v core.Supplier, actor string) error {
	if _, ok := s.ix.GetSupplier(v.ID); !ok {
		return ErrNotFound
	}
	data, err := marshal(v)
	if err != nil {
		return err
	}
	return s.commitAndPush(context.Background(), []FileOp{{Path: supplierPath(v), Data: data}},
		actor, fmt.Sprintf("%s: update supplier %s", actor, v.ID))
}

// ---- Solution ----

func (s *gitStore) ListSolutions() ([]core.Solution, error) {
	return s.ix.ListSolutions(), nil
}

func (s *gitStore) GetSolution(id string) (core.Solution, error) {
	if v, ok := s.ix.GetSolution(id); ok {
		return v, nil
	}
	return core.Solution{}, ErrNotFound
}

func (s *gitStore) CreateSolution(v core.Solution, actor string) error {
	data, err := marshal(v)
	if err != nil {
		return err
	}
	return s.commitAndPush(context.Background(), []FileOp{{Path: solutionPath(v), Data: data}},
		actor, fmt.Sprintf("%s: create solution %s", actor, v.ID))
}

// ---- Match ----

func (s *gitStore) ListMatches() ([]core.Match, error) {
	return s.ix.ListMatches(), nil
}

func (s *gitStore) GetMatch(id string) (core.Match, error) {
	if v, ok := s.ix.GetMatch(id); ok {
		return v, nil
	}
	return core.Match{}, ErrNotFound
}

func (s *gitStore) CreateMatch(m core.Match, actor string) error {
	data, err := marshal(m)
	if err != nil {
		return err
	}
	return s.commitAndPush(context.Background(), []FileOp{{Path: matchPath(m), Data: data}},
		actor, fmt.Sprintf("%s: create match %s", actor, m.ID))
}

func (s *gitStore) UpdateMatch(m core.Match, actor string) error {
	if _, ok := s.ix.GetMatch(m.ID); !ok {
		return ErrNotFound
	}
	data, err := marshal(m)
	if err != nil {
		return err
	}
	return s.commitAndPush(context.Background(), []FileOp{{Path: matchPath(m), Data: data}},
		actor, fmt.Sprintf("%s: update match %s", actor, m.ID))
}

func (s *gitStore) Counts() (int, int, int, int, int) {
	return s.ix.Counts()
}

// ---- Conversation ----

func (s *gitStore) ListConversations(owner string) ([]core.Conversation, error) {
	return s.ix.ListConversations(owner), nil
}

func (s *gitStore) GetConversation(id string) (core.Conversation, error) {
	if c, ok := s.ix.GetConversation(id); ok {
		return c, nil
	}
	return core.Conversation{}, ErrNotFound
}

func (s *gitStore) CreateConversation(c core.Conversation, actor string) error {
	data, err := marshal(c)
	if err != nil {
		return err
	}
	return s.commitAndPush(context.Background(), []FileOp{{Path: conversationPath(c), Data: data}},
		actor, fmt.Sprintf("%s: create conversation %s", actor, c.ID))
}

// UpdateConversation 整文件覆盖提交：追加消息 / 摘要 / session_id 都走这里。
func (s *gitStore) UpdateConversation(c core.Conversation, actor string) error {
	if _, ok := s.ix.GetConversation(c.ID); !ok {
		return ErrNotFound
	}
	data, err := marshal(c)
	if err != nil {
		return err
	}
	return s.commitAndPush(context.Background(), []FileOp{{Path: conversationPath(c), Data: data}},
		actor, fmt.Sprintf("%s: update conversation %s", actor, c.ID))
}

// ---- Skill（skills/<name>.md，每次请求实时读 head rev，保证最新）----

func skillPath(name string) string { return "skills/" + name + ".md" }

// skillName 校验技能名：仅允许字母数字-_，防路径穿越。
func skillName(raw string) (string, error) {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(raw, ".md"), ".md"))
	if raw == "" || len(raw) > 64 {
		return "", fmt.Errorf("技能名非法")
	}
	for _, r := range raw {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return "", fmt.Errorf("技能名只能包含字母数字-_")
		}
	}
	return raw, nil
}

func (s *gitStore) ListSkills() ([]Skill, error) {
	ctx := context.Background()
	rev := s.repo.HeadRev(ctx)
	if rev == "" {
		return nil, nil
	}
	out, err := gitOut(ctx, s.repo.dir, "ls-tree", "--name-only", rev, "skills/")
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		base := filepath.Base(line)
		name, err := skillName(base)
		if err != nil {
			continue
		}
		content, err := s.GetSkill(name)
		if err != nil {
			continue
		}
		skills = append(skills, Skill{Name: name, Content: content})
	}
	return skills, nil
}

func (s *gitStore) GetSkill(name string) (string, error) {
	name, err := skillName(name)
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	rev := s.repo.HeadRev(ctx)
	if rev == "" {
		return "", ErrNotFound
	}
	out, err := gitOutBytes(ctx, s.repo.dir, "cat-file", "blob", rev+":"+skillPath(name))
	if err != nil {
		return "", ErrNotFound
	}
	return string(out), nil
}

func (s *gitStore) PutSkill(name, content, actor string) error {
	name, err := skillName(name)
	if err != nil {
		return err
	}
	verb := "create"
	if _, err := s.GetSkill(name); err == nil {
		verb = "update"
	}
	return s.commitAndPush(context.Background(),
		[]FileOp{{Path: skillPath(name), Data: []byte(content)}},
		actor, fmt.Sprintf("%s: %s skill %s", actor, verb, name))
}

func mustMarshal(v any) []byte {
	b, err := core.JSON(v)
	if err != nil {
		panic(err) // 结构体序列化不应失败
	}
	return b
}

// stringsSplitNUL 按 \0 分割（git -z 输出）。
func stringsSplitNUL(s string) []string {
	return strings.Split(s, "\x00")
}
