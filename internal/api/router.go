package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/andaoai/Nexus/internal/agent"
	"github.com/andaoai/Nexus/internal/core"
	"github.com/andaoai/Nexus/internal/gitstore"
)

// chatEngine AI 聊天引擎接口（*agent.Engine 实现）。
type chatEngine interface {
	Chat(ctx context.Context, systemPrompt, message, sessionID string, opts ...agent.ChatOpts) (result, newSessionID string, err error)
}

// Mux 构建全部路由。store/引擎 依赖注入，便于测试。
func Mux(st gitstore.Store, eng chatEngine) http.Handler {
	mux := http.NewServeMux()

	registerConversationRoutes(mux, st, eng)

	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, map[string]any{"status": "ok", "time": time.Now()})
	})

	// 客户
	mux.Handle("GET /api/v1/customers", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := st.ListCustomers("") // 读不隔离：全部可见
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		okJSON(w, list)
	})))
	mux.Handle("POST /api/v1/customers", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var c core.Customer
		if err := decode(r, &c); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		if c.Name == "" {
			errJSON(w, 400, "name 必填")
			return
		}
		if c.Owner == "" {
			c.Owner = u.ID // 默认归当前用户
		}
		if !core.CanWriteCustomer(*u, c) {
			errJSON(w, 403, "只能创建归属自己的客户")
			return
		}
		if c.Status == "" {
			c.Status = "跟进中"
		}
		if c.ID == "" {
			c.ID = core.NewID("c")
		}
		now := time.Now()
		c.CreatedAt, c.UpdatedAt = now, now
		if err := st.CreateCustomer(c, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		okJSON(w, c)
	})))
	mux.Handle("GET /api/v1/customers/{id}", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := st.GetCustomer(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, c)
	})))
	mux.Handle("PUT /api/v1/customers/{id}", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		id := r.PathValue("id")
		var c core.Customer
		if err := decode(r, &c); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		old, err := st.GetCustomer(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		c.ID = id
		if c.Owner == "" {
			c.Owner = old.Owner
		}
		if c.Owner != old.Owner && !core.IsAdmin(*u) {
			errJSON(w, 403, "不能变更客户归属人")
			return
		}
		if !core.CanWriteCustomer(*u, c) {
			errJSON(w, 403, "只能修改归属自己的客户")
			return
		}
		c.CreatedAt = old.CreatedAt
		c.UpdatedAt = time.Now()
		if err := st.UpdateCustomer(c, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, c)
	})))
	mux.Handle("DELETE /api/v1/customers/{id}", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		id := r.PathValue("id")
		c, err := st.GetCustomer(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !core.CanWriteCustomer(*u, c) {
			errJSON(w, 403, "只能删除归属自己的客户")
			return
		}
		if err := st.DeleteCustomer(id, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, map[string]string{"deleted": id})
	})))

	// 供应商（manager 只读）
	mux.Handle("GET /api/v1/suppliers", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := st.ListSuppliers()
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		okJSON(w, list)
	})))
	mux.Handle("POST /api/v1/suppliers", auth(adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var s core.Supplier
		if err := decode(r, &s); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		if s.Name == "" {
			errJSON(w, 400, "name 必填")
			return
		}
		if s.ID == "" {
			s.ID = core.NewID("s")
		}
		s.CreatedBy = u.ID
		s.CreatedAt = time.Now()
		if err := st.CreateSupplier(s, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		okJSON(w, s)
	}))))
	mux.Handle("PUT /api/v1/suppliers/{id}", auth(adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		id := r.PathValue("id")
		var s core.Supplier
		if err := decode(r, &s); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		old, err := st.GetSupplier(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.ID = id
		s.CreatedBy = old.CreatedBy
		s.CreatedAt = old.CreatedAt
		if err := st.UpdateSupplier(s, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, s)
	}))))

	// 方案（manager 只读）
	mux.Handle("GET /api/v1/solutions", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := st.ListSolutions()
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		if sid := r.URL.Query().Get("supplier_id"); sid != "" {
			filtered := list[:0]
			for _, s := range list {
				if s.SupplierID == sid {
					filtered = append(filtered, s)
				}
			}
			list = filtered
		}
		okJSON(w, list)
	})))
	mux.Handle("POST /api/v1/solutions", auth(adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var s core.Solution
		if err := decode(r, &s); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		if s.Name == "" {
			errJSON(w, 400, "name 必填")
			return
		}
		if s.ID == "" {
			s.ID = core.NewID("sol")
		}
		s.CreatedBy = u.ID
		s.CreatedAt = time.Now()
		if err := st.CreateSolution(s, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		okJSON(w, s)
	}))))

	// 匹配
	mux.Handle("GET /api/v1/matches", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := st.ListMatches()
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		okJSON(w, list)
	})))
	mux.Handle("POST /api/v1/matches", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var req struct {
			core.Match
			Budget       float64  `json:"budget"`
			DesiredDays  int      `json:"desired_days"`
			DesiredStack []string `json:"desired_stack"`
		}
		if err := decode(r, &req); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		if req.CustomerID == "" || req.SolutionID == "" {
			errJSON(w, 400, "customer_id 与 solution_id 必填")
			return
		}
		sol, err := st.GetSolution(req.SolutionID)
		if err != nil {
			writeErr(w, err)
			return
		}
		// 匹配度计算（预算 40% + 技术 35% + 时间 25%）
		result := core.ComputeMatchScore(core.MatchInput{
			Budget: req.Budget, DesiredDays: req.DesiredDays, DesiredStack: req.DesiredStack,
		}, sol)

		m := req.Match
		m.ID = core.NewID("m")
		m.SupplierID = sol.SupplierID
		m.MatchScore = result.Score
		m.MatchReason = lightOf(result.Score)
		if m.Status == "" {
			m.Status = "待确认"
		}
		m.CreatedBy = u.ID
		m.CreatedAt = time.Now()
		if err := st.CreateMatch(m, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		okJSON(w, map[string]any{"match": m, "breakdown": result.Breakdown})
	})))
	mux.Handle("PUT /api/v1/matches/{id}", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		id := r.PathValue("id")
		old, err := st.GetMatch(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !core.CanWriteMatch(*u, old) {
			errJSON(w, 403, "只能更新自己创建的匹配")
			return
		}
		var body struct {
			Status string `json:"status"`
		}
		if err := decode(r, &body); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		if !matchStatusValid(body.Status) {
			errJSON(w, 400, "status 必须是: 待确认/已确认/已签约/已放弃")
			return
		}
		old.Status = body.Status
		if err := st.UpdateMatch(old, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, old)
	})))

	// 仪表盘
	mux.Handle("GET /api/v1/stats/dashboard", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, s, sol, m, deals := st.Counts()
		okJSON(w, map[string]any{
			"customers": c, "suppliers": s, "solutions": sol, "matches": m, "deals": deals,
		})
	})))

	// Agent 占位
	for _, ep := range []string{"analyze", "match", "suggest"} {
		mux.Handle("POST /api/v1/agent/"+ep, auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			errJSON(w, http.StatusNotImplemented, "AI Agent 功能规划中（v0.4.0）")
		})))
	}

	// 管理端
	mux.Handle("POST /api/v1/admin/sync", auth(adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := st.SyncNow(r.Context()); err != nil {
			errJSON(w, 503, err.Error())
			return
		}
		okJSON(w, map[string]string{"status": "synced"})
	}))))
	mux.Handle("GET /api/v1/admin/mesh/status", auth(adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, meshStatus(r))
	}))))

	// 前端静态资源（embed，可选）
	if h := staticHandler(); h != nil {
		mux.Handle("GET /", h)
	}

	return logRequests(mux)
}

func matchStatusValid(s string) bool {
	for _, v := range core.StatusMatch {
		if v == s {
			return true
		}
	}
	return false
}

// lightOf 匹配度 → 红黄绿灯文案。
func lightOf(score int) string {
	switch {
	case score >= 80:
		return "绿灯：可正式报价"
	case score >= 60:
		return "黄灯：需调整方案或议价"
	default:
		return "红灯：暂不推进或重新匹配"
	}
}

// writeErr 把 store 错误映射为 HTTP 状态码。
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gitstore.ErrNotFound):
		errJSON(w, 404, "不存在")
	case errors.Is(err, gitstore.ErrConflict):
		errJSON(w, 409, err.Error())
	case errors.Is(err, gitstore.ErrUnavailable):
		errJSON(w, 503, err.Error())
	default:
		errJSON(w, 500, err.Error())
	}
}

// logRequests 简易访问日志。
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.URL.Path != "/api/v1/health" {
			id := r.Header.Get("X-User-ID")
			if id == "" {
				id = "-"
			}
			os.Stdout.WriteString(time.Now().Format("15:04:05") + " " + id + " " + r.Method + " " + r.URL.Path + "\n")
		}
	})
}
