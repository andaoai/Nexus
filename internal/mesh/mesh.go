package mesh

import (
	"context"
	"fmt"
	"net/netip"

	corehost "github.com/EasyTier/easytier-go"
)

// mesh.go：内嵌 EasyTier 组网（easytier-go，wazero 运行 wasm，无 CGO）。
// 定位：让服务参与 mesh、报告 peers/routes 状态；API 流量仍走普通 HTTP。

// Config 组网配置（来自 EASYTIER_* 环境变量）。
type Config struct {
	NetworkName string
	Secret      string
	IPv4        string   // 本机虚拟 IP，如 10.144.144.1/24（不带掩码时补 /24）
	Peers       []string // 如 tcp://public.easytier.cn:11010
}

// Mesh 运行中的组网实例。
type Mesh struct {
	host     *corehost.Host
	instance *corehost.Instance
}

// Start 启动组网实例。
func Start(ctx context.Context, cfg Config) (*Mesh, error) {
	host, err := corehost.New(ctx, corehost.Options{})
	if err != nil {
		return nil, fmt.Errorf("创建 easytier host: %w", err)
	}

	b := corehost.NewInstanceConfigBuilder(cfg.NetworkName).
		NetworkSecret(cfg.Secret).
		AddPeers(cfg.Peers...)
	if cfg.IPv4 != "" {
		ipp, err := parsePrefix(cfg.IPv4)
		if err != nil {
			return nil, fmt.Errorf("EASYTIER_IPV4 无效: %w", err)
		}
		b = b.IPv4(ipp)
	}
	iconf, err := b.Build()
	if err != nil {
		return nil, fmt.Errorf("构建实例配置: %w", err)
	}

	inst, err := host.CreateInstance(ctx, iconf)
	if err != nil {
		return nil, fmt.Errorf("创建实例: %w", err)
	}
	if err := inst.Start(ctx); err != nil {
		return nil, fmt.Errorf("启动实例: %w", err)
	}
	return &Mesh{host: host, instance: inst}, nil
}

// Status 返回 peers 与 routes（用于 /api/v1/admin/mesh/status）。
func (m *Mesh) Status(ctx context.Context) (any, error) {
	peers, err := m.instance.ListPeer(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := m.instance.ListRoute(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"peers": peers, "routes": routes}, nil
}

// Close 停止实例与 host。
func (m *Mesh) Close() {
	ctx := context.Background()
	if m.instance != nil {
		m.instance.Close(ctx)
	}
	if m.host != nil {
		m.host.Close(ctx)
	}
}

// parsePrefix 解析虚拟 IP，缺省补 /24。
func parsePrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err == nil {
		return p, nil
	}
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(ip, 24), nil
}
