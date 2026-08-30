// Package config 加载 .env 与环境变量配置。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// Config 服务全部配置，来源：环境变量 + 可选 .env 文件。
type Config struct {
	// 数据仓库（GitHub / Gitee 等支持 git+token 的托管）
	GitHost     string // 托管主机，默认 github.com（可设 gitee.com）
	GitHubToken string // 访问数据仓库的 token
	DataOwner   string // 数据仓库 owner，默认 andaoai
	DataRepo    string // 数据仓库名，默认 nexus-data
	DataBranch  string // 数据仓库分支，默认 main
	CacheDir    string // 本地 bare 仓库缓存根目录，默认 <运行目录>/data

	// HTTP 服务
	Addr string // 默认 :8080

	// AI 聊天引擎（claude CLI 无头模式；换 DeepSeek/火山云等 Anthropic 兼容
	// 网关只需改这三个值，通过 ANTHROPIC_BASE_URL/AUTH_TOKEN/MODEL 注入子进程）
	LLM LLMConfig

	// EasyTier 组网（未配置则不启用）
	Mesh MeshConfig
}

// LLMConfig claude CLI 聊天引擎配置。
type LLMConfig struct {
	Bin     string // claude 可执行文件，默认 "claude"
	BaseURL string // Anthropic 兼容网关地址，空 = 用 claude 自身配置
	APIKey  string // 网关 token（Bearer），空 = 用 claude 自身登录态
	Model   string // 模型名，空 = 用 claude 默认
}

// MeshConfig EasyTier 内嵌组网配置。
type MeshConfig struct {
	Enabled     bool
	NetworkName string
	Secret      string
	IPv4        string // 如 10.144.144.1/24
	Peers       []string
}

// Load 读取配置。cwd 存在 .env 时先加载（不覆盖已有环境变量）。
func Load() (*Config, error) {
	_ = godotenv.Load()

	c := &Config{
		GitHost:     getEnv("GIT_HOST", "github.com"),
		GitHubToken: os.Getenv("GITHUB_TOKEN"),
		DataOwner:   getEnv("NEXUS_DATA_OWNER", "andaoai"),
		DataRepo:    getEnv("NEXUS_DATA_REPO", "nexus-data"),
		DataBranch:  getEnv("NEXUS_DATA_BRANCH", "main"),
		Addr:        getEnv("ADDR", ":8080"),
		LLM: LLMConfig{
			Bin:     getEnv("CLAUDE_BIN", "claude"),
			BaseURL: os.Getenv("LLM_BASE_URL"),
			APIKey:  os.Getenv("LLM_API_KEY"),
			Model:   os.Getenv("LLM_MODEL"),
		},
	}

	if c.GitHubToken == "" {
		return nil, fmt.Errorf("缺少 GITHUB_TOKEN 环境变量（参考 .env.example）")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	c.CacheDir = getEnv("CACHE_DIR", filepath.Join(cwd, "data"))

	if name := os.Getenv("EASYTIER_NETWORK_NAME"); name != "" {
		c.Mesh = MeshConfig{
			Enabled:     true,
			NetworkName: name,
			Secret:      os.Getenv("EASYTIER_NETWORK_SECRET"),
			IPv4:        os.Getenv("EASYTIER_IPV4"),
			Peers:       splitCSV(os.Getenv("EASYTIER_PEERS")),
		}
	}
	return c, nil
}

// DataRepoURL 数据仓库的 https 地址（不含凭据）。
func (c *Config) DataRepoURL() string {
	return fmt.Sprintf("https://%s/%s/%s.git", c.GitHost, c.DataOwner, c.DataRepo)
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
