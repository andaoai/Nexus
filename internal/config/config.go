// Package config 加载 .env 与环境变量配置。
package config

import (
	"fmt"
	"os"
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
	CacheDir    string // 本地 bare 仓库缓存目录，默认 ~/.cache/nexus

	// HTTP 服务
	Addr string // 默认 :8080

	// EasyTier 组网（未配置则不启用）
	Mesh MeshConfig
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
	}

	if c.GitHubToken == "" {
		return nil, fmt.Errorf("缺少 GITHUB_TOKEN 环境变量（参考 .env.example）")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	c.CacheDir = getEnv("CACHE_DIR", home+"/.cache/nexus")

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
