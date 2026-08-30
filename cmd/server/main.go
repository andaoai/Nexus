// NexusCRM 服务入口：配置 → gitstore → (可选)内嵌组网 → HTTP。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andaoai/Nexus/internal/agent"
	"github.com/andaoai/Nexus/internal/api"
	"github.com/andaoai/Nexus/internal/config"
	"github.com/andaoai/Nexus/internal/gitstore"
	"github.com/andaoai/Nexus/internal/mesh"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[config] %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 数据层：clone/打开 nexus-data 本地缓存
	store, err := gitstore.Open(ctx, gitstore.Options{
		RepoURL: cfg.DataRepoURL(),
		Branch:  cfg.DataBranch,
		Token:   cfg.GitHubToken,
		CacheDir: cfg.CacheDir + "/" + cfg.DataRepo + ".git",
	})
	if err != nil {
		log.Fatalf("[gitstore] %v", err)
	}
	go store.SyncLoop(ctx, 30*time.Second)

	// AI 聊天引擎（claude CLI 无头模式）
	eng := agent.New(agent.Config{
		Bin:     cfg.LLM.Bin,
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
	})

	// 内嵌 EasyTier 组网：失败仅告警，不影响 HTTP
	if cfg.Mesh.Enabled {
		m, err := mesh.Start(ctx, mesh.Config{
			NetworkName: cfg.Mesh.NetworkName,
			Secret:      cfg.Mesh.Secret,
			IPv4:        cfg.Mesh.IPv4,
			Peers:       cfg.Mesh.Peers,
		})
		if err != nil {
			log.Printf("[mesh] 已禁用（启动失败不影响服务）: %v", err)
		} else {
			defer m.Close()
			log.Printf("[mesh] EasyTier 已启动: network=%s ipv4=%s peers=%v",
				cfg.Mesh.NetworkName, cfg.Mesh.IPv4, cfg.Mesh.Peers)
			api.SetMeshStatus(m.Status)
		}
	}

	srv := &http.Server{Addr: cfg.Addr, Handler: api.Mux(store, eng), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("[nexus] 监听 %s（数据仓库 %s/%s）", cfg.Addr, cfg.DataOwner, cfg.DataRepo)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[http] %v", err)
	}
	log.Println("[nexus] 已退出")
}
