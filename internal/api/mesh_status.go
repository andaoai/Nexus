package api

import (
	"context"
	"net/http"
	"sync"
)

// mesh_status.go：internal/mesh 与 api 的解耦。
// main 启动 mesh 成功后调用 SetMeshStatus 注入状态函数，未注入则报未启用。

var (
	meshMu      sync.RWMutex
	meshStatusFn func(ctx context.Context) (any, error)
)

// SetMeshStatus 注入 mesh 状态查询函数（由 main 调用）。
func SetMeshStatus(fn func(ctx context.Context) (any, error)) {
	meshMu.Lock()
	defer meshMu.Unlock()
	meshStatusFn = fn
}

func meshStatus(r *http.Request) map[string]any {
	meshMu.RLock()
	fn := meshStatusFn
	meshMu.RUnlock()
	if fn == nil {
		return map[string]any{"enabled": false}
	}
	peers, err := fn(r.Context())
	out := map[string]any{"enabled": true, "peers": peers}
	if err != nil {
		out["error"] = err.Error()
	}
	return out
}
