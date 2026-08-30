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

// ---- fakeStore 的会话/技能方法 ----

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
func (f *fakeStore) ListSkills() ([]gitstore.Skill, error) {
	var out []gitstore.Skill
	for name, content := range f.skills {
		out = append(out, gitstore.Skill{Name: name, Content: content})
	}
	return out, nil
}
func (f *fakeStore) GetSkill(name string) (string, error) {
	if c, ok := f.skills[name]; ok {
		return c, nil
	}
	return "", gitstore.ErrNotFound
}
func (f *fakeStore) PutSkill(name, content, _ string) error {
	name = strings.TrimSuffix(name, ".md")
	f.skills[name] = content
	return nil
}

// fakeEngine 恒定回复，记录收到的 system prompt / 消息 / session。
type fakeEngine struct {
	sysPrompt  string
	msg        string
	session    string
	failWith   error
	newSession string
}

func (f *fakeEngine) Chat(_ context.Context, systemPrompt, message, sessionID string, _ ...agent.ChatOpts) (string, string, error) {
	if f.failWith != nil {
		return "", "", f.failWith
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

	// user3 不能看 user2 的会话
	w, _ = req(t, h, "GET", "/api/v1/conversations/"+cid, "user3", "")
	if w.Code != 403 {
		t.Fatalf("user3 看 user2 会话应 403, got %d", w.Code)
	}

	// user2 聊天
	w, out := req(t, h, "POST", "/api/v1/conversations/"+cid+"/chat", "user2", `{"content":"客户想压价10万"}`)
	if w.Code != 200 {
		t.Fatalf("chat 应 200, got %d: %v", w.Code, out)
	}
	if out["ai_message"].(map[string]any)["content"] != "AI 回复" {
		t.Fatalf("AI 回复异常: %v", out["ai_message"])
	}
	// 引擎应收到 skill 兜底 + 客户快照上下文
	if !strings.Contains(eng.sysPrompt, "当前对象信息") || !strings.Contains(eng.sysPrompt, "XX科技") {
		t.Fatalf("system prompt 缺少对象上下文: %s", eng.sysPrompt)
	}
	conv := f.conversations[cid]
	if conv.ClaudeSessionID != "claude-sess-1" {
		t.Fatalf("session id 未记录: %q", conv.ClaudeSessionID)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("应落库 2 条消息, got %d", len(conv.Messages))
	}

	// 摘要
	w, out = req(t, h, "POST", "/api/v1/conversations/"+cid+"/summary", "user2", "")
	if w.Code != 200 {
		t.Fatalf("摘要应 200, got %d: %v", w.Code, out)
	}
	if f.conversations[cid].Summary == "" {
		t.Fatal("摘要未保存")
	}

	// admin all=1 全局视图（数组响应，不用 req 解析）
	f.conversations["cv-other"] = core.Conversation{ID: "cv-other", Owner: "user3"}
	r := httptest.NewRequest("GET", "/api/v1/conversations?all=1", nil)
	r.Header.Set("X-User-ID", "user1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "cv-other") {
		t.Fatalf("admin 全局视图应含 cv-other, got %d: %s", w.Code, w.Body.String())
	}
	// manager all=1 拒绝
	r.Header.Set("X-User-ID", "user2")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("manager 全局视图应 403, got %d", w.Code)
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

var _ = time.Now
