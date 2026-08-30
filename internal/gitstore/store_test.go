package gitstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/andaoai/Nexus/internal/core"
)

// newTestRemote 建一个本地 bare 远端仓库（初始含一次提交）。
func newTestRemote(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("", "init", "--bare", remote)

	// 用一个临时 worktree 推一个初始提交（git push 需要远端至少有分支历史一致性）
	work := filepath.Join(t.TempDir(), "seed")
	run("", "init", "-b", "main", work)
	cfg := func(k, v string) { run(work, "config", k, v) }
	cfg("user.name", "seed")
	cfg("user.email", "seed@nexus.local")
	os.WriteFile(filepath.Join(work, "README.md"), []byte("# nexus-data\n"), 0o644)
	run(work, "add", "-A")
	run(work, "commit", "-m", "seed")
	run(work, "remote", "add", "origin", remote)
	run(work, "push", "-u", "origin", "main")
	return remote
}

// openStore 打开指向 test remote 的 store。
func openStore(t *testing.T, remote, cacheDir string) Store {
	t.Helper()
	s, err := Open(context.Background(), Options{
		RepoURL:  remote,
		Branch:   "main",
		Token:    "",
		CacheDir: cacheDir,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestCustomerCRUD(t *testing.T) {
	remote := newTestRemote(t)
	s := openStore(t, remote, filepath.Join(t.TempDir(), "cache.git"))

	c := core.Customer{ID: core.NewID("c"), Name: "测试科技", Owner: "user2", Status: "跟进中"}
	if err := s.CreateCustomer(c, "user2"); err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}

	got, err := s.GetCustomer(c.ID)
	if err != nil || got.Name != "测试科技" {
		t.Fatalf("GetCustomer: %v %+v", err, got)
	}

	got.Status = "已成交"
	got.UpdatedAt = time.Now()
	if err := s.UpdateCustomer(got, "user2"); err != nil {
		t.Fatalf("UpdateCustomer: %v", err)
	}
	if c2, _ := s.GetCustomer(c.ID); c2.Status != "已成交" {
		t.Fatalf("更新未生效: %+v", c2)
	}

	if err := s.DeleteCustomer(c.ID, "user1"); err != nil {
		t.Fatalf("DeleteCustomer: %v", err)
	}
	if _, err := s.GetCustomer(c.ID); err != ErrNotFound {
		t.Fatalf("删除后应 ErrNotFound, got %v", err)
	}
}

func TestConcurrentWriters(t *testing.T) {
	remote := newTestRemote(t)
	cache := filepath.Join(t.TempDir(), "shared-cache.git")

	// 模拟两台机器：各自独立的本地缓存
	s1 := openStore(t, remote, cache)
	s2 := openStore(t, remote, filepath.Join(t.TempDir(), "cache2.git"))

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, st := range []Store{s1, s2} {
		wg.Add(1)
		go func(i int, st Store) {
			defer wg.Done()
			c := core.Customer{ID: core.NewID("c"), Name: string(rune('A' + i)), Owner: "user2"}
			errs <- st.CreateCustomer(c, "user2")
		}(i, st)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发写入失败: %v", err)
		}
	}

	// 同步后两人都能看到对方的数据
	if err := s1.SyncNow(context.Background()); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if err := s2.SyncNow(context.Background()); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	cs1, _ := s1.ListCustomers("")
	cs2, _ := s2.ListCustomers("")
	if len(cs1) != 2 || len(cs2) != 2 {
		t.Fatalf("并发写后应各有 2 条客户, got %d / %d", len(cs1), len(cs2))
	}
}

func TestSupplierSolutionMatch(t *testing.T) {
	remote := newTestRemote(t)
	s := openStore(t, remote, filepath.Join(t.TempDir(), "cache.git"))

	sp := core.Supplier{ID: core.NewID("s"), Name: "云创科技", CreatedBy: "user1"}
	if err := s.CreateSupplier(sp, "user1"); err != nil {
		t.Fatalf("CreateSupplier: %v", err)
	}
	sol := core.Solution{ID: core.NewID("sol"), Name: "仓储方案", SupplierID: sp.ID,
		EstimatedCost: 280000, DeliveryDays: 60, TechStack: []string{"Java"}, CreatedBy: "user1"}
	if err := s.CreateSolution(sol, "user1"); err != nil {
		t.Fatalf("CreateSolution: %v", err)
	}
	m := core.Match{ID: core.NewID("m"), CustomerID: "c-x", SolutionID: sol.ID,
		SupplierID: sp.ID, MatchScore: 92, Status: "待确认", CreatedBy: "user2"}
	if err := s.CreateMatch(m, "user2"); err != nil {
		t.Fatalf("CreateMatch: %v", err)
	}
	m.Status = "已签约"
	if err := s.UpdateMatch(m, "user2"); err != nil {
		t.Fatalf("UpdateMatch: %v", err)
	}

	cust, sup, solu, mat, deals := s.Counts()
	if cust != 0 || sup != 1 || solu != 1 || mat != 1 || deals != 1 {
		t.Fatalf("Counts 错误: %d %d %d %d %d", cust, sup, solu, mat, deals)
	}
}

func TestAuthedURL(t *testing.T) {
	got := authedURL("https://github.com/a/b.git", "tok")
	want := "https://x-access-token:tok@github.com/a/b.git"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
