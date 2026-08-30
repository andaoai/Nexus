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

	// 会话列表：全员共享，所有人都看全部
	mux.Handle("GET /api/v1/conversations", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := st.ListConversations("")
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		if list == nil {
			list = []core.Conversation{}
		}
		okJSON(w, list)
	})))

	// 会话详情（含全部消息）：全员共享
	mux.Handle("GET /api/v1/conversations/{id}", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := st.GetConversation(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
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
		c, err := st.GetConversation(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		userMsg, aiMsg, err := chatWithEngine(r.Context(), st, eng, &c, *u, body.Content)
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
		okJSON(w, map[string]any{"user_message": userMsg, "ai_message": aiMsg})
	})))

	// 规范化进展总结：手动触发，走完整聊天管线（工具可用，AI 边总结边补档案）
	mux.Handle("POST /api/v1/conversations/{id}/summary", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		c, err := st.GetConversation(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		if len(c.Messages) == 0 {
			errJSON(w, 400, "会话还没有消息")
			return
		}
		instruction := "【规范化进展总结】请基于本会话全部对话与系统内档案，按以下维度输出简报：" +
			"1. 对象概况（公司/规模/行业）；2. 组织与关键联系人（姓名、角色、职责，档案里缺的用工具补档）；" +
			"3. 需求与预算进展；4. 已达成共识；5. 风险与下一步。简报要简洁、信息密度高。"
		_, aiMsg, err := chatWithEngine(r.Context(), st, eng, &c, *u, instruction)
		if err != nil {
			errJSON(w, aiErrStatus(err), err.Error())
			return
		}
		// AI 回复文本同时写入 summary 字段（跨机器/跨会话的压缩记忆）
		c.Summary, c.SummaryAt, c.UpdatedAt = aiMsg.Content, time.Now(), time.Now()
		if err := st.UpdateConversation(c, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, map[string]any{"summary": c.Summary, "ai_message": aiMsg})
	})))

	// 会话绑定对象/联系人（MCP 建档后由 nexus-mcp 调用）
	mux.Handle("POST /api/v1/conversations/{id}/link", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		c, err := st.GetConversation(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var body struct {
			SubjectType string `json:"subject_type"`
			SubjectID   string `json:"subject_id"`
			ContactID   string `json:"contact_id"`
		}
		if err := decode(r, &body); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		if body.ContactID != "" {
			// 绑定联系人：其公司自动成为会话对象
			p, err := st.GetContact(body.ContactID)
			if err != nil {
				writeErr(w, err)
				return
			}
			c.ContactID, c.ContactName = p.ID, p.Name
			if p.CompanyID != "" && subjectTypeValid(p.CompanyType) {
				c.SubjectType, c.SubjectID, c.SubjectName = p.CompanyType, p.CompanyID, p.CompanyName
			}
			c.Messages = append(c.Messages, core.Message{
				Role: "system", Author: "system",
				Content: fmt.Sprintf("🔗 AI 已关联联系人「%s」（%s·%s）", p.Name, p.CompanyName, p.Role),
				At:      time.Now(),
			})
		} else {
			if !subjectTypeValid(body.SubjectType) || body.SubjectID == "" {
				errJSON(w, 400, "subject_type 与 subject_id 必填（或传 contact_id）")
				return
			}
			name, err := companyName(st, body.SubjectType, body.SubjectID)
			if err != nil {
				writeErr(w, err)
				return
			}
			c.SubjectType, c.SubjectID, c.SubjectName = body.SubjectType, body.SubjectID, name
			c.Messages = append(c.Messages, core.Message{
				Role: "system", Author: "system",
				Content: fmt.Sprintf("🔗 AI 已创建并绑定%s「%s」（%s）", subjectCN(body.SubjectType), name, body.SubjectID),
				At:      time.Now(),
			})
		}
		c.UpdatedAt = time.Now()
		if err := st.UpdateConversation(c, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, map[string]string{"status": "linked"})
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

// toolGuidance 聊天工具模式规约：理解迭代 + 出谋划策 + 能力沉淀。
const toolGuidance = `

## 工具使用规约（理解迭代 + 出谋划策 + 沉淀）
1. **每轮修正理解**：对话中出现客户/供应商/联系人的新信息（需求变化、预算调整、新关键人、态度转向），当轮就用 upsert_customer / upsert_supplier / upsert_contact 更新档案，不等总结按钮；更新后在回复里用一句话告知
2. **主动出谋划策**：回复必须落到可执行判断——下一步见谁、聊什么、报价/方案策略、风险点；会话绑定了联系人时，基于其档案与历史给出针对性打法
3. **建档查重**：新建前先 search_subjects / search_contacts 查重，命中则更新而非新建；link_subject / link_contact 把会话绑到对应对象
4. **发现即沉淀**：对话中形成可复用的方法论、话术、流程时，主动用 save_skill 保存（管理员直接生效，其他成员进草稿区待转正）
5. upsert/save 成功后一句话告知用户；失败（如无权限）如实转述`

// chatWithEngine 组装上下文 → 调引擎（含 resume 自愈）→ 追加消息 → 落库。
// 返回本轮的用户消息与 AI 回复消息。
func chatWithEngine(ctx context.Context, st gitstore.Store, eng chatEngine, c *core.Conversation, u core.User, content string) (core.Message, core.Message, error) {
	systemPrompt := defaultSkill
	if skill, err := st.GetSkill(c.Skill); err == nil && skill != "" {
		systemPrompt = skill
	}
	systemPrompt += toolGuidance
	// 会话由 {owner} 发起、团队共享，当前发言者身份注入，多人接力聊 AI 能区分谁在说话
	owner, _ := core.LookupUser(c.Owner)
	systemPrompt += fmt.Sprintf("\n\n本会话由 %s 发起，团队共享可接力沟通。当前发言者：%s（%s，%s）。",
		owner.Name, u.Name, u.ID, u.Role)
	if ctxInfo := subjectContext(st, c); ctxInfo != "" {
		systemPrompt += "\n\n当前对象信息：\n" + ctxInfo
	}
	if contactInfo := contactContext(st, c); contactInfo != "" {
		systemPrompt += "\n\n本会话围绕以下联系人展开，请基于其档案与历史给出针对性建议：\n" + contactInfo
	}
	if c.Summary != "" {
		systemPrompt += "\n\n此前进展摘要：\n" + c.Summary
	}

	userMsg := core.Message{Role: "user", Author: u.ID, Content: content, At: time.Now()}
	opts := agent.ChatOpts{ConvID: c.ID, UserID: u.ID}
	aiText, sessionID, err := eng.Chat(ctx, systemPrompt, content, c.ClaudeSessionID, opts)
	if err != nil && isSessionGone(err) && c.ClaudeSessionID != "" {
		// resume 自愈：本机 claude 缓存丢了/换机器了，session 续不上。
		// 用摘要 + 最近对话重建上下文，以全新 session 重发本轮消息
		systemPrompt += "\n\n注意：原会话上下文已不可续接，以下为历史回顾，请无缝接上："
		if c.Summary != "" {
			systemPrompt += "\n[进展摘要]\n" + c.Summary
		}
		systemPrompt += "\n[最近对话]\n" + recentTranscript(c.Messages, 20)
		aiText, sessionID, err = eng.Chat(ctx, systemPrompt, content, "", opts)
	}
	if err != nil {
		return userMsg, core.Message{}, err
	}

	// AI 执行期间 MCP 工具可能已改过会话（如 link 绑定建档对象），
	// 重新拉取最新状态再追加消息，避免用旧快照覆盖掉工具的改动
	if fresh, ferr := st.GetConversation(c.ID); ferr == nil {
		*c = fresh
	}

	aiMsg := core.Message{Role: "assistant", Author: "ai", Content: aiText, At: time.Now()}
	c.Messages = append(c.Messages, userMsg, aiMsg)
	if sessionID != "" {
		c.ClaudeSessionID = sessionID
	}
	c.UpdatedAt = time.Now()
	if err := st.UpdateConversation(*c, u.ID); err != nil {
		return userMsg, aiMsg, err
	}
	return userMsg, aiMsg, nil
}

// isSessionGone 判断是否 claude session 无法续接（本机缓存丢失/换机）。
func isSessionGone(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no conversation found") ||
		strings.Contains(msg, "session not found") ||
		strings.Contains(msg, "no such session")
}

// recentTranscript 最近 n 条消息转成"谁说了什么"文本（重建上下文用）。
func recentTranscript(msgs []core.Message, n int) string {
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			fmt.Fprintf(&b, "%s: %s\n", m.Author, m.Content)
		case "assistant":
			fmt.Fprintf(&b, "AI: %s\n", m.Content)
		}
	}
	return b.String()
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

// contactContext 当前绑定联系人的完整档案 JSON 快照。
func contactContext(st gitstore.Store, c *core.Conversation) string {
	if c.ContactID == "" {
		return ""
	}
	if p, err := st.GetContact(c.ContactID); err == nil {
		b, _ := core.JSON(p); return string(b)
	}
	return ""
}

// aiErrStatus AI 引擎错误 → HTTP 状态码。
func aiErrStatus(err error) int {
	if err == agent.ErrUnavailable || strings.Contains(err.Error(), agent.ErrUnavailable.Error()) {
		return 503
	}
	return 502
}
