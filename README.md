# NexusCRM

> 连接 · 对齐 · 成交 —— 3 人小团队的轻量级 CRM。Go 单二进制 + GitHub 私有仓库作数据库 + Vue3 前端 + 内嵌 EasyTier 组网。

## 架构

```
┌──────────────────────────────────────────────┐
│                EasyTier 虚拟内网              │
│        （easytier-go 内嵌，可开关、可降级）    │
└──────────────────────────────────────────────┘
                    │
┌──────────────────────────────────────────────┐
│            nexus-server（Go 单二进制）        │
│  internal/api      HTTP 路由 + 权限中间件     │
│  internal/core     领域模型 / 匹配算法 / 权限  │
│  internal/gitstore GitHub 仓库数据层          │
│  internal/mesh     EasyTier 内嵌组网          │
│  internal/api/webdist  前端构建产物（embed）  │
└──────────────────────────────────────────────┘
                    │
┌──────────────────────────────────────────────┐
│      andaoai/nexus-data（GitHub 私有仓库）    │
│  customers/<owner>/*.json  suppliers/        │
│  solutions/  matches/  conversations/        │
└──────────────────────────────────────────────┘
```

- **数据层**：每个实体一个 JSON 文件，写操作 = fetch → commit → push（被拒自动重放重试 ≤3 次），commit message 形如 `user2: create customer c-xxxx`，天然审计日志。读走内存索引，后台 30s 同步。
- **权限**：`X-User-ID` 请求头标识用户（编译期 3 用户表）。管理员（user1）全量读写；客户经理（user2/user3）只能写自己的客户与匹配，供应商/方案只读。
- **匹配度**：预算 40% + 技术 35% + 时间 25%，≥80 绿灯 / 60-79 黄灯 / <60 红灯。
- **组网**：配置 `EASYTIER_*` 环境变量后内嵌 EasyTier（[easytier-go](https://github.com/EasyTier/easytier-go)，wazero 运行 wasm，无 CGO）；不配置则纯 HTTP 服务。

## 快速开始

```bash
# 1. 配置
cp .env.example .env
#    编辑 .env 填入 GITHUB_TOKEN（需 repo 权限）

# 2. 构建（前端产物 embed 进二进制）
make all          # 或: make build（跳过前端）

# 3. 运行
./bin/nexus-server
# 浏览器打开 http://localhost:8080
```

前端开发模式（热更新，/api 代理到 8080）：

```bash
cd web && npm install && npm run dev
```

## API 一览（/api/v1，需 X-User-ID 头）

| 方法 | 路径 | 说明 | 权限 |
|------|------|------|------|
| GET/POST | /customers | 客户列表 / 新建 | 经理仅写自己的 |
| GET/PUT/DELETE | /customers/{id} | 客户详情 / 更新 / 删除 | 归属人或管理员 |
| GET/POST | /suppliers | 供应商 | 经理只读 |
| PUT | /suppliers/{id} | 更新供应商 | 管理员 |
| GET/POST | /solutions | 方案 | 经理只读 |
| GET/POST | /matches | 匹配（POST 带 budget/desired_days/desired_stack，服务端算分） | 经理仅写自己的 |
| PUT | /matches/{id} | 更新匹配状态 | 创建者或管理员 |
| GET | /stats/dashboard | 仪表盘统计 | 全部 |
| POST | /agent/analyze·match·suggest | AI Agent（v0.4.0 占位） | - |
| POST | /admin/sync | 手动同步数据仓库 | 管理员 |
| GET | /admin/mesh/status | 组网状态 | 管理员 |

## 数据一致性取舍

- **最后写赢**：同一实体并发编辑，后提交者覆盖前者（3 人团队可接受；后续可加 updated_at 乐观锁）。
- **写延迟**：每次写 = 一次 git push，公网约 1-2s；读不受影响（内存索引）。
- **push 失败**：本地提交保留，后台定时重推；远端已领先时自动重放到新 head。

## 后续规划

| 版本 | 内容 |
|------|------|
| v0.2.0 | JWT 认证、updated_at 乐观锁 |
| v0.3.0 | Element Plus 三栏布局完整前端 |
| v0.4.0 | Claude Agent：需求分析 / 智能匹配 / 话术建议 |
| v1.0.0 | 安卓 App、业务流程闭环 |
