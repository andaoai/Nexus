package gitstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/andaoai/Nexus/internal/core"
)

// index.go：内存索引。读全走这里，写成功后同步更新。
// 重建方式：git ls-tree -r HEAD 遍历远端 head 的全部文件，按路径分类加载。

// entityKind 实体类型与仓库目录的映射。
const (
	dirCustomers    = "customers"
	dirSuppliers    = "suppliers"
	dirSolutions    = "solutions"
	dirMatches      = "matches"
	dirConversations = "conversations"
)

type index struct {
	mu            sync.RWMutex
	rev           string
	customers     map[string]core.Customer
	suppliers     map[string]core.Supplier
	solutions     map[string]core.Solution
	matches       map[string]core.Match
	conversations map[string]core.Conversation
}

func newIndex() *index {
	return &index{
		customers:     map[string]core.Customer{},
		suppliers:     map[string]core.Supplier{},
		solutions:     map[string]core.Solution{},
		matches:       map[string]core.Match{},
		conversations: map[string]core.Conversation{},
	}
}

// Rebuild 从 bare 仓库指定 rev 全量重建索引。
func (ix *index) Rebuild(ctx context.Context, repo *Repo, rev string) error {
	out, err := gitOut(ctx, repo.dir, "ls-tree", "-r", "-z", rev)
	if err != nil {
		return fmt.Errorf("ls-tree: %w", err)
	}

	fresh := newIndex()

	// -z 格式：<mode> <type> <sha>\t<path>\0
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		tab := strings.Index(entry, "\t")
		if tab < 0 {
			continue
		}
		meta, path := entry[:tab], entry[tab+1:]
		if !strings.Contains(meta, " blob ") || !strings.HasSuffix(path, ".json") {
			continue
		}
		sha := strings.Fields(meta)[2]
		data, err := gitOutBytes(ctx, repo.dir, "cat-file", "blob", sha)
		if err != nil {
			continue
		}
		fresh.load(path, data)
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.customers = fresh.customers
	ix.suppliers = fresh.suppliers
	ix.solutions = fresh.solutions
	ix.matches = fresh.matches
	ix.conversations = fresh.conversations
	ix.rev = rev
	return nil
}

func (ix *index) load(path string, data []byte) {
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		return
	}
	switch parts[0] {
	case dirCustomers:
		var c core.Customer
		if json.Unmarshal(data, &c) == nil && c.ID != "" {
			ix.customers[c.ID] = c
		}
	case dirSuppliers:
		var s core.Supplier
		if json.Unmarshal(data, &s) == nil && s.ID != "" {
			ix.suppliers[s.ID] = s
		}
	case dirSolutions:
		var s core.Solution
		if json.Unmarshal(data, &s) == nil && s.ID != "" {
			ix.solutions[s.ID] = s
		}
	case dirMatches:
		var m core.Match
		if json.Unmarshal(data, &m) == nil && m.ID != "" {
			ix.matches[m.ID] = m
		}
	case dirConversations:
		var c core.Conversation
		if json.Unmarshal(data, &c) == nil && c.ID != "" {
			ix.conversations[c.ID] = c
		}
	}
}

// ---- 读接口（RLock）----

func (ix *index) Rev() string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.rev
}

func (ix *index) ListCustomers(owner string) []core.Customer {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out []core.Customer
	for _, c := range ix.customers {
		if owner == "" || c.Owner == owner {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (ix *index) GetCustomer(id string) (core.Customer, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	c, ok := ix.customers[id]
	return c, ok
}

func (ix *index) ListSuppliers() []core.Supplier {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out []core.Supplier
	for _, s := range ix.suppliers {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (ix *index) GetSupplier(id string) (core.Supplier, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	s, ok := ix.suppliers[id]
	return s, ok
}

func (ix *index) ListSolutions() []core.Solution {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out []core.Solution
	for _, s := range ix.solutions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (ix *index) GetSolution(id string) (core.Solution, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	s, ok := ix.solutions[id]
	return s, ok
}

func (ix *index) ListMatches() []core.Match {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out []core.Match
	for _, m := range ix.matches {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (ix *index) GetMatch(id string) (core.Match, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	m, ok := ix.matches[id]
	return m, ok
}

// ListConversations 会话列表。owner 为空返回全部（管理员视图）。
func (ix *index) ListConversations(owner string) []core.Conversation {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out []core.Conversation
	for _, c := range ix.conversations {
		if owner == "" || c.Owner == owner {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (ix *index) GetConversation(id string) (core.Conversation, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	c, ok := ix.conversations[id]
	return c, ok
}

func (ix *index) Counts() (customers, suppliers, solutions, matches, deals int) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	customers, suppliers, solutions, matches = len(ix.customers), len(ix.suppliers), len(ix.solutions), len(ix.matches)
	for _, m := range ix.matches {
		if m.Status == "已签约" {
			deals++
		}
	}
	return
}

// apply 把一次写操作应用到内存（仅本地已提交成功后调用）。
func (ix *index) apply(ops []FileOp) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, op := range ops {
		if op.Delete {
			ix.deleteByPath(op.Path)
			continue
		}
		ix.load(op.Path, op.Data)
	}
}

func (ix *index) deleteByPath(path string) {
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		return
	}
	id := strings.TrimSuffix(filepath.Base(path), ".json")
	switch parts[0] {
	case dirCustomers:
		delete(ix.customers, id)
	case dirSuppliers:
		delete(ix.suppliers, id)
	case dirSolutions:
		delete(ix.solutions, id)
	case dirMatches:
		delete(ix.matches, id)
	case dirConversations:
		delete(ix.conversations, id)
	}
}

// ---- 工具 ----

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	b, err := gitOutBytes(ctx, dir, args...)
	return string(b), err
}

func gitOutBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Output()
}
