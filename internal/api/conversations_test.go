package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andaoai/Nexus/internal/agent"
	"github.com/andaoai/Nexus/internal/core"
	"github.com/andaoai/Nexus/internal/gitstore"
)

// ---- fakeStore 的会话方法 ----

func (f *fakeStore) ListConversations(owner string) ([]core.Conversation, error) {
	var out []core.Conversation
	for _, c := range f.conversations {
		if owner == "" || c.Owner == owner {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeStore) GetConversation(id string) (core.Conversation, error) {
	if c, ok := f.conversations[id]; ok {
		return c, nil
	}
	return core.Conversation{}, gitstore.ErrNotFound
}
func (f *fakeStore) CreateConversation(c core.Conversation, _ string) error {
	f.conversations[c.ID] = c
	return nil
}
func (f *fakeStore) UpdateConversation(c core.Conversation, _ string) error {
	if _, ok := f.conversations[c.ID]; !ok {
		return gitstore.ErrNotFound
	}
	f.conversations[c.ID] = c
	return nil
}

// fakeEngine 恒定回复，记录收到的 system prompt / 消息 / session。
type fakeEngine struct {
	sysPrompt  string
	msg        string
	session    string
	failWith   error
	newSession string
	// failOnSession 非空时，sessionID 等于它则报错（模拟 resume 失效）。
	failOnSession string
}

func (f *fakeEngine) Chat(_ context.Context, systemPrompt, message, sessionID string, _ ...agent.ChatOpts) (string, string, error) {
	if f.failWith != nil {
		return "", "", f.failWith
	}
	if f.failOnSession != "" && sessionID == f.failOnSession {
		return "", "", errors.New("No conversation found with session ID: " + sessionID)
	}
	f.sysPrompt, f.msg, f.session = systemPrompt, message, sessionID
	if f.newSession != "" {
		return "AI 回复", f.newSession, nil
	}
	return "AI 回复", sessionID, nil
}

func newConv(t *testing.T, h http.Handler, userID, subjectType, subjectID string) string {
	t.Helper()
	body := `{"subject_type":"` + subjectType + `","subject_id":"` + subjectID + `","title":"测试会话"}`
	w, out := req(t, h, "POST", "/api/v1/conversations", userID, body)
	if w.Code != 201 {
		t.Fatalf("建会话应 201, got %d: %v", w.Code, out)
	}
	return out["id"].(string)
}

func TestConversationLifecycle(t *testing.T) {
	f := newFake()
	eng := &fakeEngine{newSession: "claude-sess-1"}
	h := Mux(f, eng)

	// user2 建客户，再建绑定该客户的会话
	f.customers["c-x"] = core.Customer{ID: "c-x", Name: "XX科技", Owner: "user2"}
	cid := newConv(t, h, "user2", "customer", "c-x")

	// 非法 subject_type
	w, _ := req(t, h, "POST", "/api/v1/conversations", "user2", `{"subject_type":"whatever"}`)
	if w.Code != 400 {
		t.Fatalf("非法 subject_type 应 400, got %d", w.Code)
	}
	// 绑定不存在的客户
	w, _ = req(t, h, "POST", "/api/v1/conversations", "user2", `{"subject_type":"customer","subject_id":"c-nope"}`)
	if w.Code != 404 {
		t.Fatalf("绑定不存在客户应 404, got %d", w.Code)
	}

	// 全员共享：user3 能看 user2 的会话
	w, _ = req(t, h, "GET", "/api/v1/conversations/"+cid, "user3", "")
	if w.Code != 200 {
		t.Fatalf("user3 看 user2 会话应 200（全员共享）, got %d", w.Code)
	}

	// user2 聊天
	w, out := req(t, h, "POST", "/api/v1/conversations/"+cid+"/chat", "user2", `{"content":"客户想压价10万"}`)
	if w.Code != 200 {
		t.Fatalf("chat 应 200, got %d: %v", w.Code, out)
	}
	if out["ai_message"].(map[string]any)["content"] != "AI 回复" {
		t.Fatalf("AI 回复异常: %v", out["ai_message"])
	}
	// 引擎应收到 skill 兜底 + 客户快照 + 发言者身份
	if !strings.Contains(eng.sysPrompt, "当前对象信息") || !strings.Contains(eng.sysPrompt, "XX科技") {
		t.Fatalf("system prompt 缺少对象上下文: %s", eng.sysPrompt)
	}
	if !strings.Contains(eng.sysPrompt, "当前发言者：经理A") {
		t.Fatalf("system prompt 缺少发言者身份: %s", eng.sysPrompt)
	}
	conv := f.conversations[cid]
	if conv.ClaudeSessionID != "claude-sess-1" {
		t.Fatalf("session id 未记录: %q", conv.ClaudeSessionID)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("应落库 2 条消息, got %d", len(conv.Messages))
	}

	// 摘要：走完整聊天管线，AI 回复文本写入 summary
	w, out = req(t, h, "POST", "/api/v1/conversations/"+cid+"/summary", "user2", "")
	if w.Code != 200 {
		t.Fatalf("摘要应 200, got %d: %v", w.Code, out)
	}
	if f.conversations[cid].Summary != "AI 回复" {
		t.Fatalf("摘要应为 AI 回复文本, got %q", f.conversations[cid].Summary)
	}
	if !strings.Contains(eng.msg, "规范化进展总结") {
		t.Fatalf("摘要应以规范化指令发消息, got %q", eng.msg)
	}

	// 列表：所有人看到全部（数组响应，不用 req 解析）
	f.conversations["cv-other"] = core.Conversation{ID: "cv-other", Owner: "user3"}
	for _, uid := range []string{"user1", "user2", "user3"} {
		r := httptest.NewRequest("GET", "/api/v1/conversations", nil)
		r.Header.Set("X-User-ID", uid)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "cv-other") {
			t.Fatalf("%s 会话列表应含 cv-other, got %d: %s", uid, rec.Code, rec.Body.String())
		}
	}
}

func TestSharedConversationCrossUser(t *testing.T) {
	f := newFake()
	eng := &fakeEngine{newSession: "sess-u2"}
	h := Mux(f, eng)
	f.customers["c-x"] = core.Customer{ID: "c-x", Name: "XX科技", Owner: "user2"}
	cid := newConv(t, h, "user2", "customer", "c-x")

	// user2 先聊一轮
	if w, _ := req(t, h, "POST", "/api/v1/conversations/"+cid+"/chat", "user2", `{"content":"开始沟通"}`); w.Code != 200 {
		t.Fatal("user2 聊天失败")
	}
	// user3 接力同一会话：session 续接 + 发言者身份区分
	eng.newSession = ""
	if w, _ := req(t, h, "POST", "/api/v1/conversations/"+cid+"/chat", "user3", `{"content":"我接着聊"}`); w.Code != 200 {
		t.Fatal("user3 接力聊天失败")
	}
	if eng.session != "sess-u2" {
		t.Fatalf("user3 应复用同一 claude session 续接, got %q", eng.session)
	}
	if !strings.Contains(eng.sysPrompt, "当前发言者：经理B") {
		t.Fatalf("system prompt 应标明 user3 身份: %s", eng.sysPrompt)
	}
	msgs := f.conversations[cid].Messages
	if msgs[len(msgs)-2].Author != "user3" {
		t.Fatalf("user3 消息 author 应为 user3, got %q", msgs[len(msgs)-2].Author)
	}
}

func TestResumeSelfHeal(t *testing.T) {
	f := newFake()
	// 原 session 续不上（本机缓存丢失），第二次调用（sessionID=""）成功
	eng := &fakeEngine{failOnSession: "dead-sess", newSession: "new-sess"}
	h := Mux(f, eng)
	f.customers["c-x"] = core.Customer{ID: "c-x", Name: "XX科技", Owner: "user2"}
	cid := newConv(t, h, "user2", "customer", "c-x")
	conv := f.conversations[cid]
	conv.ClaudeSessionID = "dead-sess"
	conv.Messages = []core.Message{{Role: "user", Author: "user2", Content: "之前聊过预算40万"}}
	f.conversations[cid] = conv

	w, out := req(t, h, "POST", "/api/v1/conversations/"+cid+"/chat", "user2", `{"content":"继续"}`)
	if w.Code != 200 {
		t.Fatalf("自愈重试应 200, got %d: %v", w.Code, out)
	}
	// 重试时应带上摘要/最近对话重建上下文
	if !strings.Contains(eng.sysPrompt, "原会话上下文已不可续接") || !strings.Contains(eng.sysPrompt, "预算40万") {
		t.Fatalf("自愈应注入历史上下文: %s", eng.sysPrompt)
	}
	if f.conversations[cid].ClaudeSessionID != "new-sess" {
		t.Fatalf("新 session id 应落库, got %q", f.conversations[cid].ClaudeSessionID)
	}
}

func TestChatEngineFailureKeepsUserMessage(t *testing.T) {
	f := newFake()
	eng := &fakeEngine{failWith: errors.New("boom")}
	h := Mux(f, eng)
	f.customers["c-x"] = core.Customer{ID: "c-x", Name: "XX科技", Owner: "user2"}
	cid := newConv(t, h, "user2", "customer", "c-x")

	w, _ := req(t, h, "POST", "/api/v1/conversations/"+cid+"/chat", "user2", `{"content":"在吗"}`)
	if w.Code != 502 {
		t.Fatalf("引擎失败应 502, got %d", w.Code)
	}
	// 用户消息 + 失败标记都落库
	msgs := f.conversations[cid].Messages
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "system" {
		t.Fatalf("失败时应落库 user+system 消息, got %+v", msgs)
	}

	// 引擎不可用 → 503
	eng2 := &fakeEngine{failWith: agent.ErrUnavailable}
	h2 := Mux(f, eng2)
	w, _ = req(t, h2, "POST", "/api/v1/conversations/"+cid+"/chat", "user2", `{"content":"再试"}`)
	if w.Code != 503 {
		t.Fatalf("引擎不可用应 503, got %d", w.Code)
	}
}

func TestSkills(t *testing.T) {
	f := newFake()
	h := Mux(f, &fakeEngine{})

	// manager 不能写技能
	w, _ := req(t, h, "PUT", "/api/v1/admin/skills/quote-assist", "user2", `{"content":"xxx"}`)
	if w.Code != 403 {
		t.Fatalf("manager 写技能应 403, got %d", w.Code)
	}

	// admin 写技能
	w, out := req(t, h, "PUT", "/api/v1/admin/skills/quote-assist", "user1", `{"content":"报价话术技能"}`)
	if w.Code != 200 {
		t.Fatalf("admin 写技能应 200, got %d: %v", w.Code, out)
	}

	// 列表可见（数组响应，不用 req 解析）
	r := httptest.NewRequest("GET", "/api/v1/skills", nil)
	r.Header.Set("X-User-ID", "user2")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "quote-assist") {
		t.Fatalf("技能列表应含 quote-assist, got %d: %s", w.Code, w.Body.String())
	}

	// 指定技能的会话：chat 时用技能内容作 system prompt
	f.customers["c-x"] = core.Customer{ID: "c-x", Name: "XX科技", Owner: "user2"}
	eng := &fakeEngine{}
	h2 := Mux(f, eng)
	body := `{"subject_type":"customer","subject_id":"c-x","skill":"quote-assist"}`
	w, out = req(t, h2, "POST", "/api/v1/conversations", "user2", body)
	if w.Code != 201 {
		t.Fatalf("建会话应 201, got %d: %v", w.Code, out)
	}
	cid := out["id"].(string)
	req(t, h2, "POST", "/api/v1/conversations/"+cid+"/chat", "user2", `{"content":"hi"}`)
	if !strings.HasPrefix(eng.sysPrompt, "报价话术技能") {
		t.Fatalf("应以数据仓库技能作 system prompt 前缀, got: %s", eng.sysPrompt)
	}
}

func TestSkillDraftFlow(t *testing.T) {
	f := newFake()
	h := Mux(f, &fakeEngine{})

	// manager 写草稿
	w, out := req(t, h, "PUT", "/api/v1/skill-drafts/my-talk", "user2", `{"content":"经理的报价打法"}`)
	if w.Code != 200 {
		t.Fatalf("manager 写草稿应 200, got %d: %v", w.Code, out)
	}
	// manager 看不到草稿列表
	w, _ = req(t, h, "GET", "/api/v1/admin/skill-drafts", "user2", "")
	if w.Code != 403 {
		t.Fatalf("manager 看草稿列表应 403, got %d", w.Code)
	}
	// manager 不能转正
	w, _ = req(t, h, "POST", "/api/v1/admin/skill-drafts/my-talk/approve", "user2", "")
	if w.Code != 403 {
		t.Fatalf("manager 转正应 403, got %d", w.Code)
	}
	// admin 列出并转正（数组响应，不用 req 解析）
	r := httptest.NewRequest("GET", "/api/v1/admin/skill-drafts", nil)
	r.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "my-talk") {
		t.Fatalf("admin 草稿列表应含 my-talk, got %d: %s", rec.Code, rec.Body.String())
	}
	w, _ = req(t, h, "POST", "/api/v1/admin/skill-drafts/my-talk/approve", "user1", "")
	if w.Code != 200 {
		t.Fatalf("admin 转正应 200, got %d", w.Code)
	}
	if _, ok := f.skills["my-talk"]; !ok {
		t.Fatal("转正后应进入正式技能区")
	}
	if len(f.skillDrafts) != 0 {
		t.Fatalf("转正后草稿应清空, got %v", f.skillDrafts)
	}
}

func TestContactAPIAndLink(t *testing.T) {
	f := newFake()
	eng := &fakeEngine{}
	h := Mux(f, eng)
	f.customers["c-x"] = core.Customer{ID: "c-x", Name: "XX智造", Owner: "user2"}

	// 建联系人
	w, out := req(t, h, "POST", "/api/v1/contacts", "user2",
		`{"name":"李工","company_type":"customer","company_id":"c-x","role":"技术经理"}`)
	if w.Code != 201 {
		t.Fatalf("建联系人应 201, got %d: %v", w.Code, out)
	}
	pid := out["id"].(string)
	if out["company_name"] != "XX智造" {
		t.Fatalf("公司名快照应自动带出, got %v", out["company_name"])
	}

	// 非法 company_type
	w, _ = req(t, h, "POST", "/api/v1/contacts", "user2", `{"name":"王五","company_type":"alien"}`)
	if w.Code != 400 {
		t.Fatalf("非法 company_type 应 400, got %d", w.Code)
	}

	// 过滤列表
	r := httptest.NewRequest("GET", "/api/v1/contacts?company_id=c-x", nil)
	r.Header.Set("X-User-ID", "user3")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "李工") {
		t.Fatalf("联系人过滤列表异常: %d %s", rec.Code, rec.Body.String())
	}

	// 联系人团队公共：user3 也能补充更新（PUT 整实体替换，需带全字段）
	w, _ = req(t, h, "PUT", "/api/v1/contacts/"+pid, "user3",
		`{"name":"李工","role":"技术经理","phone":"13800000000"}`)
	if w.Code != 200 {
		t.Fatalf("user3 改联系人应 200（团队公共档案）, got %d", w.Code)
	}

	// 会话绑定联系人：公司自动成为对象 + 🔗 消息
	cid := newConv(t, h, "user2", "general", "")
	w, out = req(t, h, "POST", "/api/v1/conversations/"+cid+"/link", "user2", `{"contact_id":"`+pid+`"}`)
	if w.Code != 200 {
		t.Fatalf("link contact 应 200, got %d: %v", w.Code, out)
	}
	conv := f.conversations[cid]
	if conv.ContactID != pid || conv.ContactName != "李工" {
		t.Fatalf("会话未绑定联系人: %+v", conv)
	}
	if conv.SubjectType != "customer" || conv.SubjectName != "XX智造" {
		t.Fatalf("绑定联系人后公司应成为会话对象: %+v", conv)
	}
	if len(conv.Messages) == 0 || !strings.Contains(conv.Messages[0].Content, "已关联联系人") {
		t.Fatalf("应追加 🔗 消息: %+v", conv.Messages)
	}

	// 聊天时应注入联系人档案上下文
	if w, _ := req(t, h, "POST", "/api/v1/conversations/"+cid+"/chat", "user2", `{"content":"李工怎么看"}`); w.Code != 200 {
		t.Fatal("聊天失败")
	}
	if !strings.Contains(eng.sysPrompt, "技术经理") || !strings.Contains(eng.sysPrompt, "李工") {
		t.Fatalf("system prompt 应含联系人档案: %s", eng.sysPrompt)
	}
}

var _ = time.Now
