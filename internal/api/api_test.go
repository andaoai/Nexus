package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andaoai/Nexus/internal/core"
	"github.com/andaoai/Nexus/internal/gitstore"
)

// fakeStore 内存实现，用于 HTTP 层测试。
type fakeStore struct {
	customers     map[string]core.Customer
	suppliers     map[string]core.Supplier
	solutions     map[string]core.Solution
	matches       map[string]core.Match
	conversations map[string]core.Conversation
	skills        map[string]string
}

func newFake() *fakeStore {
	return &fakeStore{
		customers:     map[string]core.Customer{},
		suppliers:     map[string]core.Supplier{},
		solutions:     map[string]core.Solution{},
		matches:       map[string]core.Match{},
		conversations: map[string]core.Conversation{},
		skills:        map[string]string{},
	}
}

func (f *fakeStore) ListCustomers(owner string) ([]core.Customer, error) {
	var out []core.Customer
	for _, c := range f.customers {
		if owner == "" || c.Owner == owner {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeStore) GetCustomer(id string) (core.Customer, error) {
	if c, ok := f.customers[id]; ok {
		return c, nil
	}
	return core.Customer{}, gitstore.ErrNotFound
}
func (f *fakeStore) CreateCustomer(c core.Customer, _ string) error { f.customers[c.ID] = c; return nil }
func (f *fakeStore) UpdateCustomer(c core.Customer, _ string) error {
	if _, ok := f.customers[c.ID]; !ok {
		return gitstore.ErrNotFound
	}
	f.customers[c.ID] = c
	return nil
}
func (f *fakeStore) DeleteCustomer(id string, _ string) error {
	delete(f.customers, id)
	return nil
}
func (f *fakeStore) ListSuppliers() ([]core.Supplier, error) {
	var out []core.Supplier
	for _, s := range f.suppliers {
		out = append(out, s)
	}
	return out, nil
}
func (f *fakeStore) GetSupplier(id string) (core.Supplier, error) {
	if s, ok := f.suppliers[id]; ok {
		return s, nil
	}
	return core.Supplier{}, gitstore.ErrNotFound
}
func (f *fakeStore) CreateSupplier(s core.Supplier, _ string) error { f.suppliers[s.ID] = s; return nil }
func (f *fakeStore) UpdateSupplier(s core.Supplier, _ string) error { f.suppliers[s.ID] = s; return nil }
func (f *fakeStore) ListSolutions() ([]core.Solution, error) {
	var out []core.Solution
	for _, s := range f.solutions {
		out = append(out, s)
	}
	return out, nil
}
func (f *fakeStore) GetSolution(id string) (core.Solution, error) {
	if s, ok := f.solutions[id]; ok {
		return s, nil
	}
	return core.Solution{}, gitstore.ErrNotFound
}
func (f *fakeStore) CreateSolution(s core.Solution, _ string) error { f.solutions[s.ID] = s; return nil }
func (f *fakeStore) ListMatches() ([]core.Match, error) {
	var out []core.Match
	for _, m := range f.matches {
		out = append(out, m)
	}
	return out, nil
}
func (f *fakeStore) GetMatch(id string) (core.Match, error) {
	if m, ok := f.matches[id]; ok {
		return m, nil
	}
	return core.Match{}, gitstore.ErrNotFound
}
func (f *fakeStore) CreateMatch(m core.Match, _ string) error { f.matches[m.ID] = m; return nil }
func (f *fakeStore) UpdateMatch(m core.Match, _ string) error { f.matches[m.ID] = m; return nil }
func (f *fakeStore) Counts() (int, int, int, int, int) {
	return len(f.customers), len(f.suppliers), len(f.solutions), len(f.matches), 0
}
func (f *fakeStore) SyncNow(ctx context.Context) error  { return nil }
func (f *fakeStore) SyncLoop(ctx context.Context, d time.Duration) {}

// req 执行请求并返回响应。
func req(t *testing.T, h http.Handler, method, path, userID, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	if userID != "" {
		r.Header.Set("X-User-ID", userID)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是 JSON: %s", w.Body.String())
		}
	}
	return w, out
}

func TestAuthRequired(t *testing.T) {
	h := Mux(newFake(), nil)
	w, _ := req(t, h, "GET", "/api/v1/customers", "", "")
	if w.Code != 401 {
		t.Fatalf("无 X-User-ID 应 401, got %d", w.Code)
	}
	w, _ = req(t, h, "GET", "/api/v1/customers", "hacker", "")
	if w.Code != 401 {
		t.Fatalf("未知用户应 401, got %d", w.Code)
	}
}

func TestCustomerPermission(t *testing.T) {
	f := newFake()
	h := Mux(f, nil)

	// user2 建客户
	body := `{"name":"XX科技","industry":"制造业"}`
	w, out := req(t, h, "POST", "/api/v1/customers", "user2", body)
	if w.Code != 201 {
		t.Fatalf("user2 建客户应 201, got %d: %v", w.Code, out)
	}
	cid := out["id"].(string)
	if f.customers[cid].Owner != "user2" {
		t.Fatalf("owner 应默认 user2, got %q", f.customers[cid].Owner)
	}

	// user3 不能改 user2 的客户
	w, _ = req(t, h, "PUT", "/api/v1/customers/"+cid, "user3", `{"status":"已报价"}`)
	if w.Code != 403 {
		t.Fatalf("user3 改 user2 客户应 403, got %d", w.Code)
	}

	// user3 不能改归属
	w, _ = req(t, h, "PUT", "/api/v1/customers/"+cid, "user2", `{"owner":"user3"}`)
	if w.Code != 403 {
		t.Fatalf("变更归属应 403, got %d", w.Code)
	}

	// user2 自己能改
	w, _ = req(t, h, "PUT", "/api/v1/customers/"+cid, "user2", `{"status":"已报价"}`)
	if w.Code != 200 {
		t.Fatalf("user2 改自己客户应 200, got %d: %v", w.Code, out)
	}

	// user1 能删任意
	w, _ = req(t, h, "DELETE", "/api/v1/customers/"+cid, "user1", "")
	if w.Code != 200 {
		t.Fatalf("admin 删除应 200, got %d", w.Code)
	}
}

func TestSupplierSolutionAdminOnly(t *testing.T) {
	h := Mux(newFake(), nil)

	w, _ := req(t, h, "POST", "/api/v1/suppliers", "user2", `{"name":"云创科技"}`)
	if w.Code != 403 {
		t.Fatalf("经理建供应商应 403, got %d", w.Code)
	}
	w, out := req(t, h, "POST", "/api/v1/suppliers", "user1", `{"name":"云创科技","specialties":["ERP"]}`)
	if w.Code != 201 {
		t.Fatalf("admin 建供应商应 201, got %d: %v", w.Code, out)
	}
	sid := out["id"].(string)

	w, _ = req(t, h, "POST", "/api/v1/solutions", "user2", `{"name":"仓储方案"}`)
	if w.Code != 403 {
		t.Fatalf("经理建方案应 403, got %d", w.Code)
	}
	body := `{"name":"仓储方案","supplier_id":"` + sid + `","estimated_cost":280000,"delivery_days":60,"tech_stack":["Java"]}`
	w, sol := req(t, h, "POST", "/api/v1/solutions", "user1", body)
	if w.Code != 201 {
		t.Fatalf("admin 建方案应 201, got %d: %v", w.Code, sol)
	}
	t.Logf("solution id=%v", sol["id"])
}

func TestMatchScoreAndStatus(t *testing.T) {
	f := newFake()
	h := Mux(f, nil)

	f.suppliers["s-x"] = core.Supplier{ID: "s-x", Name: "云创"}
	f.solutions["sol-x"] = core.Solution{ID: "sol-x", SupplierID: "s-x",
		EstimatedCost: 280000, DeliveryDays: 60, TechStack: []string{"Java", "MySQL"}}

	// user3 建匹配：全维度匹配 → 高分绿灯
	body := `{"customer_id":"c-1","solution_id":"sol-x","budget":300000,"desired_days":90,"desired_stack":["Java"]}`
	w, out := req(t, h, "POST", "/api/v1/matches", "user3", body)
	if w.Code != 201 {
		t.Fatalf("建匹配应 201, got %d: %v", w.Code, out)
	}
	m := out["match"].(map[string]any)
	if m["match_score"].(float64) < 95 {
		t.Fatalf("全匹配分数应 ≥95, got %v", m["match_score"])
	}
	if m["supplier_id"] != "s-x" {
		t.Fatalf("supplier_id 应自动从方案带出, got %v", m["supplier_id"])
	}

	mid := m["id"].(string)
	// user2 非创建者不能改状态
	w, _ = req(t, h, "PUT", "/api/v1/matches/"+mid, "user2", `{"status":"已签约"}`)
	if w.Code != 403 {
		t.Fatalf("非创建者改匹配应 403, got %d", w.Code)
	}
	// 创建者可以
	w, _ = req(t, h, "PUT", "/api/v1/matches/"+mid, "user3", `{"status":"已签约"}`)
	if w.Code != 200 {
		t.Fatalf("创建者改匹配应 200, got %d", w.Code)
	}
	// 非法状态
	w, _ = req(t, h, "PUT", "/api/v1/matches/"+mid, "user3", `{"status":"随便"}`)
	if w.Code != 400 {
		t.Fatalf("非法状态应 400, got %d", w.Code)
	}
}

func TestDashboardAndAgentStub(t *testing.T) {
	h := Mux(newFake(), nil)
	w, out := req(t, h, "GET", "/api/v1/stats/dashboard", "user2", "")
	if w.Code != 200 || out["customers"].(float64) != 0 {
		t.Fatalf("dashboard 异常: %d %v", w.Code, out)
	}
	w, _ = req(t, h, "POST", "/api/v1/agent/analyze", "user2", `{}`)
	if w.Code != 501 {
		t.Fatalf("agent 占位应 501, got %d", w.Code)
	}
}

var _ = errors.New
