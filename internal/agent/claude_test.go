package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude 写一个假 claude 脚本，把收到的参数转储并回显固定 JSON，
// 用于验证参数拼装与输出解析。
func fakeClaude(t *testing.T, output string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "fake-claude")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + filepath.Join(dir, "args.txt") + `"
printf '%s' '` + output + `'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func TestChatArgsAndParse(t *testing.T) {
	out := `{"result":"你好，我是 CRM 助手","session_id":"sess-123"}`
	bin, dir := fakeClaude(t, out)
	e := New(Config{Bin: bin, Timeout: 5 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, sid, err := e.Chat(ctx, "你是 CRM 助手", "客户问预算", "old-sess")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result != "你好，我是 CRM 助手" || sid != "sess-123" {
		t.Fatalf("result=%q sid=%q", result, sid)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	// 必须出现的标记（--bare 为无值布尔开关）
	flags := map[string]bool{
		"--output-format": false, "--append-system-prompt": false,
		"--disallowedTools": false, "--max-turns": false, "--resume": false, "--bare": false,
	}
	for i, a := range args {
		if _, ok := flags[a]; ok {
			flags[a] = true
		}
		if a == "-p" && i+1 < len(args) && args[i+1] == "客户问预算" {
			flags["-p"] = true
		}
	}
	for f, seen := range flags {
		if !seen {
			t.Fatalf("缺少参数: %s", f)
		}
	}
	// 首条消息应紧跟 -p
	if args[0] != "-p" || args[1] != "客户问预算" {
		t.Fatalf("参数顺序错误: %v", args[:2])
	}
}

func TestChatNewSessionNoResume(t *testing.T) {
	out := `{"result":"ok","session_id":"s1"}`
	bin, dir := fakeClaude(t, out)
	e := New(Config{Bin: bin})

	_, _, err := e.Chat(context.Background(), "sys", "hi", "")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if strings.Contains(string(raw), "--resume") {
		t.Fatalf("新会话不应带 --resume: %s", raw)
	}
}

func TestChatBadOutput(t *testing.T) {
	bin, _ := fakeClaude(t, "not json")
	e := New(Config{Bin: bin})
	_, _, err := e.Chat(context.Background(), "sys", "hi", "")
	if err == nil {
		t.Fatal("期望解析失败报错")
	}
}

func TestChatMissingBinary(t *testing.T) {
	e := New(Config{Bin: "/nonexistent/claude"})
	_, _, err := e.Chat(context.Background(), "sys", "hi", "")
	if err == nil || !strings.Contains(err.Error(), "未找到") {
		t.Fatalf("期望未找到报错, got: %v", err)
	}
}

func TestEnvInjection(t *testing.T) {
	e := New(Config{Bin: "claude", BaseURL: "https://gw.example", APIKey: "tok", Model: "m1"})
	env := e.env()
	joined := strings.Join(env, "\n")
	for _, want := range []string{"ANTHROPIC_BASE_URL=https://gw.example", "ANTHROPIC_AUTH_TOKEN=tok", "ANTHROPIC_MODEL=m1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少 %s", want)
		}
	}
}
