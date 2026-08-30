// Package agent AI 聊天引擎：驱动本机 claude CLI 无头模式（claude -p）。
// 多轮靠 --resume <session_id> 续接；换 DeepSeek/火山云等 Anthropic 兼容
// 网关只需配置 LLM_BASE_URL / LLM_API_KEY / LLM_MODEL 环境变量。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrUnavailable 引擎不可用：CLI 不存在或未配置 API key。
var ErrUnavailable = errors.New("AI 引擎不可用")

// Config 引擎配置。
type Config struct {
	Bin     string // claude 可执行文件，默认 "claude"
	BaseURL string // Anthropic 兼容网关地址（空 = 用 claude 自身配置）
	APIKey  string // 网关 token（Bearer）
	Model   string // 模型名（空 = 用 claude 默认）
	Timeout time.Duration
}

// Engine claude CLI 无头引擎。
type Engine struct {
	cfg Config
}

// New 构造引擎。bin 为空取 "claude"。
func New(cfg Config) *Engine {
	if cfg.Bin == "" {
		cfg.Bin = "claude"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 3 * time.Minute
	}
	return &Engine{cfg: cfg}
}

// reply claude --output-format json 的关键字段。
type reply struct {
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
}

// Available 检查 CLI 是否在 PATH 上且已配置凭据。
func (e *Engine) Available() error {
	if _, err := exec.LookPath(e.cfg.Bin); err != nil {
		return fmt.Errorf("%w: 本机未找到 %s", ErrUnavailable, e.cfg.Bin)
	}
	if e.cfg.APIKey == "" {
		// 未显式配置时依赖 claude 自身登录态，不阻断
		return nil
	}
	return nil
}

// ChatOpts 一次聊天调用的会话上下文。
type ChatOpts struct {
	ConvID string // 会话 id（传给 MCP 工具做自动绑定）
	UserID string // 会话 owner（MCP 工具以该身份调 API）
}

// Chat 发送一条消息并返回 AI 回复与新 session id。
// systemPrompt 注入 skill + 上下文；sessionID 为空表示新会话。
// 本机存在 nexus-mcp 伴生二进制时启用工具模式（AI 可自动建档）。
func (e *Engine) Chat(ctx context.Context, systemPrompt, message, sessionID string, opts ...ChatOpts) (result, newSessionID string, err error) {
	if _, err := exec.LookPath(e.cfg.Bin); err != nil {
		return "", "", fmt.Errorf("%w: 本机未找到 %s，请安装 claude CLI", ErrUnavailable, e.cfg.Bin)
	}

	args := []string{"-p", message, "--output-format", "json", "--bare",
		"--append-system-prompt", systemPrompt}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	mcpCfg, err := e.mcpConfig(opts)
	if err != nil {
		return "", "", err
	}
	if mcpCfg != nil {
		// 工具模式：白名单只放 MCP 工具（headless 下其余工具自动拒绝），允许多轮工具循环
		cfgFile, cleanup, err := writeMCPConfig(mcpCfg)
		if err != nil {
			return "", "", err
		}
		defer cleanup()
		args = append(args, "--mcp-config", cfgFile, "--allowedTools", "mcp__nexus",
			"--max-turns", "10")
	} else {
		// 纯聊天模式：禁用全部工具
		args = append(args, "--disallowedTools", "*", "--max-turns", "1")
	}

	cmd := exec.CommandContext(ctx, e.cfg.Bin, args...)
	cmd.Env = e.env()

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", "", fmt.Errorf("claude CLI 执行失败: %s", truncate(msg, 500))
	}

	var r reply
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return "", "", fmt.Errorf("claude CLI 输出解析失败: %w（原始输出: %s）", err, truncate(stdout.String(), 300))
	}
	return r.Result, r.SessionID, nil
}

// env 组装子进程环境：注入网关凭据（存在时）。
func (e *Engine) env() []string {
	env := os.Environ()
	if e.cfg.BaseURL != "" {
		env = append(env, "ANTHROPIC_BASE_URL="+e.cfg.BaseURL)
	}
	if e.cfg.APIKey != "" {
		env = append(env, "ANTHROPIC_AUTH_TOKEN="+e.cfg.APIKey)
	}
	if e.cfg.Model != "" {
		env = append(env, "ANTHROPIC_MODEL="+e.cfg.Model)
	}
	return env
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
