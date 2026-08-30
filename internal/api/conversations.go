package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/andaoai/Nexus/internal/agent"
	"github.com/andaoai/Nexus/internal/core"
	"github.com/andaoai/Nexus/internal/gitstore"
)

// defaultSkill 内置默认技能：数据仓库 skills/ 缺失或指定技能不存在时兜底。
const defaultSkill = `你是 NexusCRM 的 AI 业务助手，协助客户经理推进客户与供应商沟通。

职责：
1. 围绕客户需求、供应商能力、方案报价展开讨论，帮执行者理清下一步动作
2. 回答简明扼要，给出可执行建议而非空泛分析
3. 涉及金额、周期时主动对齐"预算 / 交付天数 / 技术栈"三要素
4. 不编造系统中不存在的客户或供应商信息`

// registerConversationRoutes 注册会话与技能路由。
func registerConversationRoutes(mux *http.ServeMux, st gitstore.Store, eng chatEngine) {
	// 新建会话
	mux.Handle("POST /api/v1/conversations", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var c core.Conversation
		if err := decode(r, &c); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		if !subjectTypeValid(c.SubjectType) {
			errJSON(w, 400, "subject_type 必须是: customer/supplier/general")
			return
		}
		// 校验关联实体并取名称快照
		switch c.SubjectType {
		case "customer":
			cust, err := st.GetCustomer(c.SubjectID)
			if err != nil {
				writeErr(w, err)
				return
			}
			c.SubjectName = cust.Name
		case "supplier":
			sup, err := st.GetSupplier(c.SubjectID)
			if err != nil {
				writeErr(w, err)
				return
			}
			c.SubjectName = sup.Name
		}
		c.ID = core.NewID("cv")
		c.Owner = u.ID
		c.CreatedAt, c.UpdatedAt = time.Now(), time.Now()
		if err := st.CreateConversation(c, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		okJSON(w, c)
	})))

	// 会话列表：默认自己的，admin ?all=1 看全部
	mux.Handle("GET /api/v1/conversations", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		owner := u.ID
		if r.URL.Query().Get("all") == "1" {
			if !core.IsAdmin(*u) {
				errJSON(w, 403, "仅管理员可查看全部会话")
				return
			}
			owner = ""
		}
		list, err := st.ListConversations(owner)
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		if list == nil {
			list = []core.Conversation{}
		}
		okJSON(w, list)
	})))

	// 会话详情（含全部消息）：owner 或 admin
	mux.Handle("GET /api/v1/conversations/{id}", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		c, ok := visibleConversation(w, st, *u, r.PathValue("id"))
		if !ok {
			return
		}
		okJSON(w, c)
	})))

	// 发消息 → AI 回复 → 落库
	mux.Handle("POST /api/v1/conversations/{id}/chat", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var body struct {
			Content string `json:"content"`
		}
		if err := decode(r, &body); err != nil || strings.TrimSpace(body.Content) == "" {
			errJSON(w, 400, "content 必填")
			return
		}
		c, ok := visibleConversation(w, st, *u, r.PathValue("id"))
		if !ok {
			return
		}
		reply, err := chatWithEngine(r.Context(), st, eng, &c, u.ID, body.Content)
		if err != nil {
			// 用户消息 + 失败标记都落库，会话不丢
			c.Messages = append(c.Messages,
				core.Message{Role: "user", Author: u.ID, Content: body.Content, At: time.Now()},
				core.Message{Role: "system", Author: "system",
					Content: "AI 回复失败: " + err.Error(), At: time.Now()},
			)
			c.UpdatedAt = time.Now()
			_ = st.UpdateConversation(c, u.ID)
			errJSON(w, aiErrStatus(err), err.Error())
			return
		}
		okJSON(w, reply)
	})))

	// 生成/刷新进展摘要
	mux.Handle("POST /api/v1/conversations/{id}/summary", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		c, ok := visibleConversation(w, st, *u, r.PathValue("id"))
		if !ok {
			return
		}
		if len(c.Messages) == 0 {
			errJSON(w, 400, "会话还没有消息")
			return
		}
		summary, _, err := eng.Chat(r.Context(),
			defaultSkill+"\n\n你的任务是：根据以下对话记录，写一份 200 字以内的进展摘要，供管理员全局统筹。摘要需包含：聊到了什么、达成了什么共识、下一步待办。",
			dialogTranscript(c), "")
		if err != nil {
			errJSON(w, aiErrStatus(err), err.Error())
			return
		}
		c.Summary, c.SummaryAt, c.UpdatedAt = strings.TrimSpace(summary), time.Now(), time.Now()
		if err := st.UpdateConversation(c, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, map[string]string{"summary": c.Summary})
	})))

	// 会话绑定对象（MCP 建档后由 nexus-mcp 调用）
	mux.Handle("POST /api/v1/conversations/{id}/link", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		c, ok := visibleConversation(w, st, *u, r.PathValue("id"))
		if !ok {
			return
		}
		var body struct {
			SubjectType string `json:"subject_type"`
			SubjectID   string `json:"subject_id"`
		}
		if err := decode(r, &body); err != nil || !subjectTypeValid(body.SubjectType) || body.SubjectID == "" {
			errJSON(w, 400, "subject_type 与 subject_id 必填")
			return
		}
		// 校验实体并取名称快照
		var name string
		switch body.SubjectType {
		case "customer":
			cust, err := st.GetCustomer(body.SubjectID)
			if err != nil {
				writeErr(w, err)
				return
			}
			name = cust.Name
		case "supplier":
			sup, err := st.GetSupplier(body.SubjectID)
			if err != nil {
				writeErr(w, err)
				return
			}
			name = sup.Name
		}
		c.SubjectType, c.SubjectID, c.SubjectName = body.SubjectType, body.SubjectID, name
		c.Messages = append(c.Messages, core.Message{
			Role: "system", Author: "system",
			Content: fmt.Sprintf("🔗 AI 已创建并绑定%s「%s」（%s）", subjectCN(body.SubjectType), name, body.SubjectID),
			At:      time.Now(),
		})
		c.UpdatedAt = time.Now()
		if err := st.UpdateConversation(c, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, map[string]string{"status": "linked", "subject_id": body.SubjectID})
	})))

	// 技能列表
	mux.Handle("GET /api/v1/skills", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skills, err := st.ListSkills()
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		if skills == nil {
			skills = []gitstore.Skill{}
		}
		okJSON(w, skills)
	})))

	// 技能新建/更新（admin）
	mux.Handle("PUT /api/v1/admin/skills/{name}", auth(adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var body struct {
			Content string `json:"content"`
		}
		if err := decode(r, &body); err != nil || strings.TrimSpace(body.Content) == "" {
			errJSON(w, 400, "content 必填")
			return
		}
		if err := st.PutSkill(r.PathValue("name"), body.Content, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, map[string]string{"status": "saved"})
	}))))
}

// visibleConversation 取会话并校验可见性：owner 或 admin；不可见时已写好响应。
func visibleConversation(w http.ResponseWriter, st gitstore.Store, u core.User, id string) (core.Conversation, bool) {
	c, err := st.GetConversation(id)
	if err != nil {
		writeErr(w, err)
		return core.Conversation{}, false
	}
	if c.Owner != u.ID && !core.IsAdmin(u) {
		errJSON(w, 403, "只能访问自己的会话")
		return core.Conversation{}, false
	}
	return c, true
}

func subjectTypeValid(s string) bool {
	for _, v := range core.SubjectTypes {
		if v == s {
			return true
		}
	}
	return false
}

func subjectCN(s string) string {
	switch s {
	case "customer":
		return "客户"
	case "supplier":
		return "供应商"
	}
	return "主题"
}

// toolGuidance 聊天工具模式的建档规约（附加在技能提示词后）。
const toolGuidance = `

## 建档工具使用规约
对话中出现新客户或供应商信息时：
1. 先用 search_subjects 按名称查重：命中则 upsert 更新画像并可用 link_subject 把会话绑到该对象；未命中才新建
2. 信息不全也可先 upsert 建基础画像（名称即可），后续聊天中持续补充更新
3. upsert 成功后用一句话告知用户建/更新了什么；失败（如无权限）如实转述`

// chatWithEngine 组装上下文 → 调引擎 → 追加消息 → 落库。
func chatWithEngine(ctx context.Context, st gitstore.Store, eng chatEngine, c *core.Conversation, userID, content string) (any, error) {
	systemPrompt := defaultSkill
	if skill, err := st.GetSkill(c.Skill); err == nil && skill != "" {
		systemPrompt = skill
	}
	systemPrompt += toolGuidance
	if ctxInfo := subjectContext(st, c); ctxInfo != "" {
		systemPrompt += "\n\n当前对象信息：\n" + ctxInfo
	}
	if c.Summary != "" {
		systemPrompt += "\n\n此前进展摘要：\n" + c.Summary
	}

	userMsg := core.Message{Role: "user", Author: userID, Content: content, At: time.Now()}
	aiText, sessionID, err := eng.Chat(ctx, systemPrompt, content, c.ClaudeSessionID,
		agent.ChatOpts{ConvID: c.ID, UserID: userID})
	if err != nil {
		return nil, err
	}

	// AI 执行期间 MCP 工具可能已改过会话（如 link 绑定建档对象），
	// 重新拉取最新状态再追加消息，避免用旧快照覆盖掉工具的改动
	if fresh, err := st.GetConversation(c.ID); err == nil {
		*c = fresh
	}

	aiMsg := core.Message{Role: "assistant", Author: "ai", Content: aiText, At: time.Now()}
	c.Messages = append(c.Messages, userMsg, aiMsg)
	if sessionID != "" {
		c.ClaudeSessionID = sessionID
	}
	c.UpdatedAt = time.Now()
	if err := st.UpdateConversation(*c, userID); err != nil {
		return nil, err
	}
	return map[string]any{"user_message": userMsg, "ai_message": aiMsg}, nil
}

// subjectContext 当前关联实体的最新 JSON 快照，帮 AI 掌握业务上下文。
func subjectContext(st gitstore.Store, c *core.Conversation) string {
	switch c.SubjectType {
	case "customer":
		if cust, err := st.GetCustomer(c.SubjectID); err == nil {
			b, _ := core.JSON(cust); return string(b)
		}
	case "supplier":
		if sup, err := st.GetSupplier(c.SubjectID); err == nil {
			b, _ := core.JSON(sup); return string(b)
		}
	}
	return ""
}

// dialogTranscript 全部对话转成"谁说了什么"文本，供摘要任务用。
func dialogTranscript(c core.Conversation) string {
	var b strings.Builder
	for _, m := range c.Messages {
		who := "AI"
		switch m.Role {
		case "user":
			who = m.Author
		case "system":
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", who, m.Content)
	}
	return b.String()
}

// aiErrStatus AI 引擎错误 → HTTP 状态码。
func aiErrStatus(err error) int {
	if err == agent.ErrUnavailable || strings.Contains(err.Error(), agent.ErrUnavailable.Error()) {
		return 503
	}
	return 502
}
