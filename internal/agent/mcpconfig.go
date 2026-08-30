package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// mcpConfig 传给 claude --mcp-config 的配置结构。
type mcpConfig struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Command string            `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}

// mcpConfig 探测 nexus-mcp 伴生二进制并组装 MCP 配置。
// 未命中（未部署伴生二进制）返回 nil，调用方走纯聊天模式。
func (e *Engine) mcpConfig(opts []ChatOpts) (*mcpConfig, error) {
	bin, err := nexusMCPBin()
	if err != nil {
		return nil, nil
	}
	cfg := &mcpConfig{MCPServers: map[string]mcpServerEntry{
		"nexus": {Command: bin, Env: map[string]string{}},
	}}
	if len(opts) > 0 {
		cfg.MCPServers["nexus"].Env["NEXUS_CONV_ID"] = opts[0].ConvID
		cfg.MCPServers["nexus"].Env["NEXUS_API_USER"] = opts[0].UserID
	}
	return cfg, nil
}

// nexusMCPBin 依次探测：主二进制同目录 → PATH。均未命中报错。
func nexusMCPBin() (string, error) {
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), "nexus-mcp")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath("nexus-mcp"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("nexus-mcp 未部署")
}

// writeMCPConfig 配置落临时文件，返回路径与清理函数。
func writeMCPConfig(cfg *mcpConfig) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "nexus-mcp-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("写 MCP 配置失败: %w", err)
	}
	b, _ := json.Marshal(cfg)
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("写 MCP 配置失败: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}
