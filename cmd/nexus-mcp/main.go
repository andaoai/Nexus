// nexus-mcp：NexusCRM 的 MCP stdio 服务器。
// 由 claude CLI 作为子进程拉起（--mcp-config），把客户/供应商建档能力
// 以工具形式暴露给聊天 AI；所有写操作走本机 Nexus HTTP API，
// 身份 = 会话 owner（NEXUS_API_USER），权限与页面上手操作完全一致。
//
// 环境变量：NEXUS_API_URL（默认 http://localhost:8080）、
//          NEXUS_API_USER（必填，会话 owner）、NEXUS_CONV_ID（会话 id，用于自动绑定）。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// ---- JSON-RPC / MCP 骨架 ----

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	log.SetOutput(os.Stderr) // stdout 只走 MCP 协议
	srv := &server{
		apiURL: strings.TrimRight(envOr("NEXUS_API_URL", "http://localhost:8080"), "/"),
		user:   os.Getenv("NEXUS_API_USER"),
		convID: os.Getenv("NEXUS_CONV_ID"),
		client: &http.Client{},
	}
	if srv.user == "" {
		log.Fatal("缺少 NEXUS_API_USER（会话 owner）")
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		out, err := srv.dispatch(&req)
		if err != nil {
			log.Printf("dispatch %s: %v", req.Method, err)
			continue
		}
		if out == nil { // notification，无响应
			continue
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(os.Stdout, string(b))
	}
}

// dispatch 处理一条请求；notification 返回 nil。
func (s *server) dispatch(req *rpcRequest) (*rpcResponse, error) {
	if req.ID == nil {
		return nil, nil // notifications/initialized 等
	}
	switch req.Method {
	case "initialize":
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "nexus", "version": "0.1.0"},
		}}, nil
	case "tools/list":
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": toolDefs()}}, nil
	case "tools/call":
		var args map[string]any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errResp(req.ID, "参数不是合法 JSON"), nil
			}
		}
		text, isErr := s.callTool(req.Params.Name, args)
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}}, nil
	}
	return errResp(req.ID, "未知方法: "+req.Method), nil
}

func errResp(id json.RawMessage, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32601, Message: msg}}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// server MCP 服务器状态：Nexus API 地址与调用者身份。
type server struct {
	apiURL string
	user   string // 会话 owner（X-User-ID）
	convID string // 当前会话 id，建档后自动绑定
	client *http.Client
}

// ---- 工具定义 ----

func toolDefs() []map[string]any {
	return []map[string]any{
		{
			"name": "search_subjects",
			"description": "按关键词搜索已有客户和供应商（匹配名称/需求/行业），用于建档前查重，避免重复创建。返回 id、name、类型、行业、需求摘要。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"keyword": map[string]any{"type": "string", "description": "客户/供应商名称或需求关键词"},
				},
				"required": []string{"keyword"},
			},
		},
		{
			"name": "upsert_customer",
			"description": "创建或更新客户画像。名称已存在则合并更新（非空字段覆盖），否则自动创建新客户并绑定到当前会话。信息不全也可先建基础画像。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":         map[string]any{"type": "string", "description": "客户名称（必填）"},
					"contact":      map[string]any{"type": "string", "description": "联系人"},
					"phone":        map[string]any{"type": "string"},
					"email":        map[string]any{"type": "string"},
					"industry":     map[string]any{"type": "string", "description": "行业，如 制造业/零售"},
					"requirements": map[string]any{"type": "string", "description": "需求描述（尽量保留客户原话要点）"},
					"tech_stack":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "现有/期望技术栈"},
					"priority":     map[string]any{"type": "integer", "description": "优先级 1-5"},
				},
				"required": []string{"name"},
			},
		},
		{
			"name": "link_subject",
			"description": "把当前会话绑定到一个已存在的客户或供应商（建档/搜索确认后使用，绑定后管理员可在全局视图看到本会话围绕谁展开）。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"subject_type": map[string]any{"type": "string", "enum": []string{"customer", "supplier"}, "description": "客户或供应商"},
					"subject_id":   map[string]any{"type": "string", "description": "实体 id，如 c-xxxx / s-xxxx"},
				},
				"required": []string{"subject_type", "subject_id"},
			},
		},
		{
			"name": "upsert_supplier",
			"description": "创建或更新供应商（需要管理员身份，无权限时会返回错误提示）。名称已存在则合并更新，否则创建。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "description": "供应商名称（必填）"},
					"contact":     map[string]any{"type": "string"},
					"phone":       map[string]any{"type": "string"},
					"specialties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "专长领域"},
					"price_level": map[string]any{"type": "string", "description": "价位：低端/中端/中高端/高端"},
				},
				"required": []string{"name"},
			},
		},
	}
}

// callTool 执行一个工具调用，返回给 AI 看的文本与错误标记。
func (s *server) callTool(name string, args map[string]any) (text string, isErr bool) {
	switch name {
	case "search_subjects":
		return s.searchSubjects(str(args["keyword"]))
	case "upsert_customer":
		return s.upsertCustomer(args)
	case "link_subject":
		return s.linkSubject(args)
	case "upsert_supplier":
		return s.upsertSupplier(args)
	default:
		return "未知工具: " + name, true
	}
}

// ---- 业务实现 ----

type subjectHit struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Industry     string `json:"industry,omitempty"`
	Requirements string `json:"requirements,omitempty"`
	Owner        string `json:"owner,omitempty"`
}

// searchSubjects 按关键词过滤客户+供应商。
func (s *server) searchSubjects(keyword string) (string, bool) {
	if keyword == "" {
		return "keyword 不能为空", true
	}
	var customers, suppliers []map[string]any
	s.apiGet("/api/v1/customers", &customers)
	s.apiGet("/api/v1/suppliers", &suppliers)

	kw := strings.ToLower(keyword)
	var hits []subjectHit
	for _, c := range customers {
		if matchKw(kw, c["name"], c["requirements"], c["industry"]) {
			hits = append(hits, subjectHit{Type: "customer", ID: str(c["id"]), Name: str(c["name"]),
				Industry: str(c["industry"]), Requirements: str(c["requirements"]), Owner: str(c["owner"])})
		}
	}
	for _, v := range suppliers {
		if matchKw(kw, v["name"]) {
			hits = append(hits, subjectHit{Type: "supplier", ID: str(v["id"]), Name: str(v["name"]), Owner: str(v["created_by"])})
		}
	}
	if len(hits) == 0 {
		return fmt.Sprintf("未找到匹配「%s」的客户或供应商，可以新建。", keyword), false
	}
	b, _ := json.MarshalIndent(hits, "", "  ")
	return "找到 " + fmt.Sprint(len(hits)) + " 条：\n" + string(b), false
}

func matchKw(kw string, fields ...any) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(str(f)), kw) {
			return true
		}
	}
	return false
}

// upsertCustomer 查重 → 更新或创建 → 绑定会话。
func (s *server) upsertCustomer(args map[string]any) (string, bool) {
	name := strings.TrimSpace(str(args["name"]))
	if name == "" {
		return "name 必填", true
	}
	var all []map[string]any
	s.apiGet("/api/v1/customers", &all)
	if existing := matchByName(all, name); existing != nil {
		merged := mergeCustomer(existing, args)
		if err := s.apiDo("PUT", "/api/v1/customers/"+str(existing["id"]), merged, nil); err != nil {
			return "更新客户失败: " + err.Error(), true
		}
		return fmt.Sprintf("已更新客户画像「%s」（%s），非空字段已合并。", name, str(existing["id"])), false
	}

	cust := map[string]any{"name": name, "owner": s.user}
	for k, v := range args {
		if v != nil && fmt.Sprint(v) != "" {
			cust[k] = v
		}
	}
	// 客户 tech_stack 是逗号分隔字符串字段，AI 传来的是数组
	if arr, ok := cust["tech_stack"].([]any); ok {
		parts := make([]string, len(arr))
		for i, v := range arr {
			parts[i] = str(v)
		}
		cust["tech_stack"] = strings.Join(parts, ",")
	}
	var created map[string]any
	if err := s.apiDo("POST", "/api/v1/customers", cust, &created); err != nil {
		return "创建客户失败: " + err.Error(), true
	}
	id := str(created["id"])
	if s.convID != "" {
		_ = s.apiDo("POST", "/api/v1/conversations/"+s.convID+"/link",
			map[string]any{"subject_type": "customer", "subject_id": id}, nil)
	}
	b, _ := json.MarshalIndent(created, "", "  ")
	return "已创建客户「" + name + "」（" + id + "）并绑定到当前会话：\n" + string(b), false
}

// linkSubject 把会话绑定到已有实体。
func (s *server) linkSubject(args map[string]any) (string, bool) {
	typ, id := str(args["subject_type"]), str(args["subject_id"])
	if typ != "customer" && typ != "supplier" || id == "" {
		return "subject_type（customer/supplier）与 subject_id 必填", true
	}
	if s.convID == "" {
		return "当前会话上下文缺失（NEXUS_CONV_ID 未设置）", true
	}
	if err := s.apiDo("POST", "/api/v1/conversations/"+s.convID+"/link",
		map[string]any{"subject_type": typ, "subject_id": id}, nil); err != nil {
		return "绑定失败: " + err.Error(), true
	}
	return fmt.Sprintf("已把会话绑定到%s %s。", typ, id), false
}

// upsertSupplier 同 upsertCustomer；创建/更新供应商需要 admin，权限错误原样透传。
func (s *server) upsertSupplier(args map[string]any) (string, bool) {
	name := strings.TrimSpace(str(args["name"]))
	if name == "" {
		return "name 必填", true
	}
	var all []map[string]any
	s.apiGet("/api/v1/suppliers", &all)
	if existing := matchByName(all, name); existing != nil {
		merged := mergeSupplier(existing, args)
		if err := s.apiDo("PUT", "/api/v1/suppliers/"+str(existing["id"]), merged, nil); err != nil {
			return "更新供应商失败: " + err.Error(), true
		}
		return fmt.Sprintf("已更新供应商「%s」（%s）。", name, str(existing["id"])), false
	}
	var created map[string]any
	if err := s.apiDo("POST", "/api/v1/suppliers", args, &created); err != nil {
		return "创建供应商失败（供应商由管理员维护，如无权限请联系管理员）: " + err.Error(), true
	}
	id := str(created["id"])
	if s.convID != "" {
		_ = s.apiDo("POST", "/api/v1/conversations/"+s.convID+"/link",
			map[string]any{"subject_type": "supplier", "subject_id": id}, nil)
	}
	b, _ := json.MarshalIndent(created, "", "  ")
	return "已创建供应商「" + name + "」（" + id + "）并绑定到当前会话：\n" + string(b), false
}

// matchByName 精确匹配优先，其次互相包含。
func matchByName(list []map[string]any, name string) map[string]any {
	ln := strings.ToLower(name)
	var loose map[string]any
	for _, v := range list {
		wn := strings.ToLower(str(v["name"]))
		if wn == ln {
			return v
		}
		if loose == nil && (strings.Contains(wn, ln) || strings.Contains(ln, wn)) {
			loose = v
		}
	}
	return loose
}

// mergeCustomer 已有实体 + 非空参数（数组字段整体替换）。
func mergeCustomer(existing map[string]any, args map[string]any) map[string]any {
	merged := map[string]any{}
	for k, v := range existing {
		merged[k] = v
	}
	for _, k := range []string{"contact", "phone", "email", "industry", "requirements", "tech_stack", "priority"} {
		if v, ok := args[k]; ok && v != nil && fmt.Sprint(v) != "" && fmt.Sprint(v) != "[]" {
			if k == "tech_stack" {
				if arr, isArr := v.([]any); isArr {
					parts := make([]string, len(arr))
					for i, e := range arr {
						parts[i] = str(e)
					}
					v = strings.Join(parts, ",")
				}
			}
			merged[k] = v
		}
	}
	return merged
}

func mergeSupplier(existing map[string]any, args map[string]any) map[string]any {
	merged := map[string]any{}
	for k, v := range existing {
		merged[k] = v
	}
	for _, k := range []string{"contact", "phone", "specialties", "price_level"} {
		if v, ok := args[k]; ok && v != nil && fmt.Sprint(v) != "" && fmt.Sprint(v) != "[]" {
			merged[k] = v
		}
	}
	return merged
}

// ---- HTTP 客户端 ----

func (s *server) apiGet(path string, out any) {
	_ = s.apiDo("GET", path, nil, out)
}

func (s *server) apiDo(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, s.apiURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-User-ID", s.user)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct{ Error string `json:"error"` }
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("%d %s", resp.StatusCode, e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
