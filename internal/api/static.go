package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// static 挂载 embed 的前端构建产物（internal/web/dist 由 `make frontend` 生成）。
// dist 不存在时（未构建前端）返回 nil，路由不挂载。
//
//go:embed all:webdist
var static embed.FS

func staticHandler() http.Handler {
	sub, err := fs.Sub(static, "webdist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil // 尚未构建前端
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SPA：非 API 路径一律回 index.html
		if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
