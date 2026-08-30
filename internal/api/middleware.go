package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/andaoai/Nexus/internal/core"
)

type ctxKey string

const userKey ctxKey = "user"

// errJSON 统一错误响应。
func errJSON(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func okJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// auth 校验 X-User-ID 并把 *core.User 放进 context。
func auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-User-ID")
		u, ok := core.LookupUser(id)
		if !ok {
			errJSON(w, http.StatusUnauthorized, "缺少或未知的 X-User-ID")
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r, &u)))
	})
}

func withUser(r *http.Request, u *core.User) context.Context {
	return context.WithValue(r.Context(), userKey, u)
}

func currentUser(r *http.Request) *core.User {
	u, _ := r.Context().Value(userKey).(*core.User)
	return u
}

// adminOnly 仅管理员可过。
func adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !core.IsAdmin(*currentUser(r)) {
			errJSON(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// decode 请求体 JSON 到 v。
func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	return d.Decode(v)
}
