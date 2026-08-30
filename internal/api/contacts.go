package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/andaoai/Nexus/internal/core"
	"github.com/andaoai/Nexus/internal/gitstore"
)

// registerContactRoutes 注册联系人与技能草稿路由。
func registerContactRoutes(mux *http.ServeMux, st gitstore.Store) {
	mux.Handle("GET /api/v1/contacts", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		list, err := st.ListContacts(q.Get("company_type"), q.Get("company_id"))
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		if kw := strings.TrimSpace(q.Get("q")); kw != "" {
			filtered := list[:0]
			for _, p := range list {
				if strings.Contains(p.Name, kw) || strings.Contains(p.Role, kw) ||
					strings.Contains(p.CompanyName, kw) || strings.Contains(p.Notes, kw) {
					filtered = append(filtered, p)
				}
			}
			list = filtered
		}
		if list == nil {
			list = []core.Contact{}
		}
		okJSON(w, list)
	})))

	mux.Handle("POST /api/v1/contacts", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var p core.Contact
		if err := decode(r, &p); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		if p.Name == "" {
			errJSON(w, 400, "name 必填")
			return
		}
		if p.CompanyType != "customer" && p.CompanyType != "supplier" {
			errJSON(w, 400, "company_type 必须是 customer/supplier")
			return
		}
		name, err := companyName(st, p.CompanyType, p.CompanyID)
		if err != nil {
			writeErr(w, err)
			return
		}
		p.CompanyName = name
		if p.ID == "" {
			p.ID = core.NewID("p")
		}
		p.Owner = u.ID
		now := time.Now()
		p.CreatedAt, p.UpdatedAt = now, now
		if err := st.CreateContact(p, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		okJSON(w, p)
	})))

	mux.Handle("PUT /api/v1/contacts/{id}", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		id := r.PathValue("id")
		old, err := st.GetContact(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		var p core.Contact
		if err := decode(r, &p); err != nil {
			errJSON(w, 400, "请求体不是合法 JSON: "+err.Error())
			return
		}
		p.ID = id
		p.Owner = old.Owner
		p.CompanyType = old.CompanyType
		p.CompanyID = old.CompanyID
		if p.CompanyName == "" {
			p.CompanyName = old.CompanyName
		}
		p.CreatedAt = old.CreatedAt
		p.UpdatedAt = time.Now()
		if err := st.UpdateContact(p, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, p)
	})))

	// 技能草稿区：manager 可写，admin 转正
	mux.Handle("PUT /api/v1/skill-drafts/{name}", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var body struct {
			Content string `json:"content"`
		}
		if err := decode(r, &body); err != nil || strings.TrimSpace(body.Content) == "" {
			errJSON(w, 400, "content 必填")
			return
		}
		if err := st.PutSkillDraft(r.PathValue("name"), body.Content, u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, map[string]string{"status": "draft-saved"})
	})))
	mux.Handle("GET /api/v1/admin/skill-drafts", auth(adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drafts, err := st.ListSkillDrafts()
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		if drafts == nil {
			drafts = []gitstore.Skill{}
		}
		okJSON(w, drafts)
	}))))
	mux.Handle("POST /api/v1/admin/skill-drafts/{name}/approve", auth(adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		if err := st.ApproveSkillDraft(r.PathValue("name"), u.ID); err != nil {
			writeErr(w, err)
			return
		}
		okJSON(w, map[string]string{"status": "approved"})
	}))))
}

// companyName 查联系人所属公司名称快照。
func companyName(st gitstore.Store, companyType, companyID string) (string, error) {
	switch companyType {
	case "customer":
		c, err := st.GetCustomer(companyID)
		if err != nil {
			return "", err
		}
		return c.Name, nil
	case "supplier":
		s, err := st.GetSupplier(companyID)
		if err != nil {
			return "", err
		}
		return s.Name, nil
	}
	return "", nil
}
