package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// nexus-mcp 放到 PATH 上时应被探测到，配置含身份与会话上下文。
func TestMCPConfigDetected(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "nexus-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	e := New(Config{})
	cfg, err := e.mcpConfig([]ChatOpts{{ConvID: "cv-1", UserID: "user2"}})
	if err != nil || cfg == nil {
		t.Fatalf("应探测到 nexus-mcp: cfg=%v err=%v", cfg, err)
	}
	entry := cfg.MCPServers["nexus"]
	if entry.Command != bin || entry.Env["NEXUS_CONV_ID"] != "cv-1" || entry.Env["NEXUS_API_USER"] != "user2" {
		t.Fatalf("MCP 配置不完整: %+v", entry)
	}

	// 序列化后是合法 JSON，顶层为 mcpServers
	b, _ := json.Marshal(cfg)
	var generic map[string]any
	if json.Unmarshal(b, &generic) != nil || generic["mcpServers"] == nil {
		t.Fatalf("配置应含 mcpServers: %s", b)
	}
}

func TestMCPConfigAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // 空 PATH
	// 主二进制同目录（测试二进制所在目录）无 nexus-mcp
	e := New(Config{})
	cfg, err := e.mcpConfig(nil)
	if err != nil {
		t.Fatalf("未部署不应报错: %v", err)
	}
	if cfg != nil {
		t.Fatalf("未部署应返回 nil 走纯聊天: %+v", cfg)
	}
}

func TestWriteMCPConfig(t *testing.T) {
	cfg := &mcpConfig{MCPServers: map[string]mcpServerEntry{
		"nexus": {Command: "/x/nexus-mcp", Env: map[string]string{"NEXUS_API_USER": "user2"}},
	}}
	path, cleanup, err := writeMCPConfig(cfg)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !json.Valid(b) {
		t.Fatalf("配置文件应是合法 JSON: %s %v", b, err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cleanup 应删除临时配置")
	}
}
