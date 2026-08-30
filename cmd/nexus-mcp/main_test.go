package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAPI 模拟 Nexus HTTP API 的客户/供应商/绑定端点。
func fakeAPI(t *testing.T, user string) (*server, map[string]map[string]any, map[string]bool) {
	t.Helper()
	store := map[string]map[string]any{}
	linked := map[string]bool{}
	mux := http.NewServeMux()

	readBody := func(r *http.Request) map[string]any {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		return m
	}
	mux.HandleFunc("GET /api/v1/customers", func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]any
		for _, c := range store {
			if strings.HasPrefix(c["id"].(string), "c-") {
				list = append(list, c)
			}
		}
		json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("POST /api/v1/customers", func(w http.ResponseWriter, r *http.Request) {
		body := readBody(r)
		body["id"] = "c-new01"
		store["c-new01"] = body
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("PUT /api/v1/customers/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := store[id]; !ok {
			w.WriteHeader(404)
			return
		}
		store[id] = readBody(r)
		json.NewEncoder(w).Encode(store[id])
	})
	mux.HandleFunc("GET /api/v1/suppliers", func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]any
		for _, s := range store {
			if strings.HasPrefix(s["id"].(string), "s-") {
				list = append(list, s)
			}
		}
		json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("POST /api/v1/suppliers", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-ID") != "user1" {
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]string{"error": "需要管理员权限"})
			return
		}
		body := readBody(r)
		body["id"] = "s-new01"
		store["s-new01"] = body
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("POST /api/v1/conversations/cv-1/link", func(w http.ResponseWriter, r *http.Request) {
		body := readBody(r)
		if sid, ok := body["subject_id"].(string); ok {
			linked[sid] = true
		}
		if cid, ok := body["contact_id"].(string); ok {
			linked[cid] = true
		}
		w.WriteHeader(200)
	})
	mux.HandleFunc("GET /api/v1/contacts", func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]any
		for _, p := range store {
			id, _ := p["id"].(string)
			if !strings.HasPrefix(id, "p-") {
				continue
			}
			// 简易 company_type/company_id 过滤
			if ct := r.URL.Query().Get("company_type"); ct != "" && p["company_type"] != ct {
				continue
			}
			if cid := r.URL.Query().Get("company_id"); cid != "" && p["company_id"] != cid {
				continue
			}
			list = append(list, p)
		}
		json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("POST /api/v1/contacts", func(w http.ResponseWriter, r *http.Request) {
		body := readBody(r)
		body["id"] = "p-new01"
		store["p-new01"] = body
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("PUT /api/v1/contacts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := store[id]; !ok {
			w.WriteHeader(404)
			return
		}
		store[id] = readBody(r)
		json.NewEncoder(w).Encode(store[id])
	})
	skills := map[string]string{}
	drafts := map[string]string{}
	mux.HandleFunc("PUT /api/v1/admin/skills/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-ID") != "user1" {
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]string{"error": "需要管理员权限"})
			return
		}
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		skills[r.PathValue("name")] = body.Content
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
	})
	mux.HandleFunc("PUT /api/v1/skill-drafts/{name}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		drafts[r.PathValue("name")] = body.Content
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"status": "draft-saved"})
	})

	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	s := &server{apiURL: api.URL, user: user, convID: "cv-1", client: api.Client()}
	return s, store, linked
}

func TestSearchSubjects(t *testing.T) {
	s, store, _ := fakeAPI(t, "user2")
	store["c-1"] = map[string]any{"id": "c-1", "name": "XX智造有限公司", "industry": "制造业", "requirements": "仓库管理系统", "owner": "user2"}

	text, isErr := s.callTool("search_subjects", map[string]any{"keyword": "智造"})
	if isErr || !strings.Contains(text, "c-1") {
		t.Fatalf("应命中已有客户: %v %s", isErr, text)
	}
	text, _ = s.callTool("search_subjects", map[string]any{"keyword": "查无此项"})
	if !strings.Contains(text, "未找到") {
		t.Fatalf("未命中应提示可新建: %s", text)
	}
}

func TestUpsertCustomerCreateAndLink(t *testing.T) {
	s, _, linked := fakeAPI(t, "user2")

	text, isErr := s.callTool("upsert_customer", map[string]any{
		"name": "YY科技", "industry": "零售", "requirements": "进销存", "priority": float64(4),
	})
	if isErr || !strings.Contains(text, "c-new01") || !strings.Contains(text, "已创建") {
		t.Fatalf("应创建成功: %v %s", isErr, text)
	}
	if !linked["c-new01"] {
		t.Fatal("新客户应自动绑定到会话")
	}
}

func TestUpsertCustomerUpdateMerges(t *testing.T) {
	s, store, _ := fakeAPI(t, "user2")
	store["c-1"] = map[string]any{"id": "c-1", "name": "XX智造", "industry": "制造业", "owner": "user2", "phone": "13800000000"}

	// 精确同名 → 走更新
	text, isErr := s.callTool("upsert_customer", map[string]any{
		"name": "XX智造", "email": "zhang@xx.com", "phone": "",
	})
	if isErr || !strings.Contains(text, "已更新") {
		t.Fatalf("应按名称查重走更新: %v %s", isErr, text)
	}
	// 更新后不应新建
	if len(store) != 1 {
		t.Fatalf("同名应更新而非新建, store=%v", store)
	}
}

func TestUpsertSupplierPermission(t *testing.T) {
	s, _, _ := fakeAPI(t, "user2") // manager

	_, isErr := s.callToolCapture("upsert_supplier", map[string]any{"name": "云创网络"})
	if !isErr || !strings.Contains(lastErr, "管理员") {
		t.Fatalf("manager 建供应商应返回权限错误: %v", isErr)
	}

	s1, _, linked := fakeAPI(t, "user1") // admin
	text, isErr := s1.callTool("upsert_supplier", map[string]any{"name": "云创网络", "price_level": "中端"})
	if isErr || !strings.Contains(text, "s-new01") {
		t.Fatalf("admin 建供应商应成功: %v %s", isErr, text)
	}
	if !linked["s-new01"] {
		t.Fatal("新供应商应绑定到会话")
	}
}

// lastErr 捕获最近一次工具错误文本
var lastErr string

func (s *server) callToolCapture(name string, args map[string]any) (string, bool) {
	text, isErr := s.callTool(name, args)
	if isErr {
		lastErr = text
	}
	return text, isErr
}

func TestSearchContacts(t *testing.T) {
	s, store, _ := fakeAPI(t, "user2")
	store["p-1"] = map[string]any{"id": "p-1", "name": "李工", "role": "技术经理",
		"company_type": "customer", "company_id": "c-1", "company_name": "XX智造"}

	text, isErr := s.callTool("search_contacts", map[string]any{"keyword": "李工"})
	if isErr || !strings.Contains(text, "p-1") {
		t.Fatalf("应命中已有联系人: %v %s", isErr, text)
	}
	text, _ = s.callTool("search_contacts", map[string]any{"keyword": "查无此人"})
	if !strings.Contains(text, "未找到") {
		t.Fatalf("未命中应提示可新建: %s", text)
	}
	// 按公司过滤
	text, isErr = s.callTool("search_contacts", map[string]any{"company_id": "c-other"})
	if isErr || !strings.Contains(text, "未找到") {
		t.Fatalf("他公司过滤应无结果: %v %s", isErr, text)
	}
}

func TestUpsertContactCreateAndLink(t *testing.T) {
	s, _, linked := fakeAPI(t, "user2")

	text, isErr := s.callTool("upsert_contact", map[string]any{
		"name": "王经理", "company_type": "supplier", "company_id": "s-1",
		"role": "销售", "responsibility": "负责报价",
	})
	if isErr || !strings.Contains(text, "p-new01") || !strings.Contains(text, "已创建") {
		t.Fatalf("应创建联系人成功: %v %s", isErr, text)
	}
	if !linked["p-new01"] {
		t.Fatal("新建联系人应自动关联到会话")
	}

	// 缺必填参数
	_, isErr = s.callToolCapture("upsert_contact", map[string]any{"name": "张三"})
	if !isErr || !strings.Contains(lastErr, "必填") {
		t.Fatalf("缺 company 应报错: %v", isErr)
	}
}

func TestUpsertContactUpdateMerges(t *testing.T) {
	s, store, _ := fakeAPI(t, "user2")
	store["p-1"] = map[string]any{"id": "p-1", "name": "李工", "role": "技术经理",
		"company_type": "customer", "company_id": "c-1", "company_name": "XX智造", "phone": "13800000000"}

	text, isErr := s.callTool("upsert_contact", map[string]any{
		"name": "李工", "company_type": "customer", "company_id": "c-1",
		"notes": "关注 API 兼容性",
	})
	if isErr || !strings.Contains(text, "已更新") {
		t.Fatalf("同名同公司应走更新: %v %s", isErr, text)
	}
	if len(store) != 1 {
		t.Fatalf("应更新而非新建, store=%v", store)
	}
}

func TestSaveSkillPermission(t *testing.T) {
	// manager → 草稿区
	s, _, _ := fakeAPI(t, "user2")
	text, isErr := s.callTool("save_skill", map[string]any{
		"name": "Quote Tactics", "content": "报价话术…", "why": "多次复用",
	})
	if isErr || !strings.Contains(text, "草稿") {
		t.Fatalf("manager 存技能应进草稿区: %v %s", isErr, text)
	}

	// admin → 正式技能
	s1, _, _ := fakeAPI(t, "user1")
	text, isErr = s1.callTool("save_skill", map[string]any{
		"name": "quote-tactics", "content": "报价话术…",
	})
	if isErr || !strings.Contains(text, "正式技能") {
		t.Fatalf("admin 存技能应直接生效: %v %s", isErr, text)
	}
}
