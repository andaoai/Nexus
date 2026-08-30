#!/usr/bin/env bash
# 初始化 nexus-data 私有数据仓库：创建 → 目录结构 → 首个提交
set -euo pipefail

OWNER="${NEXUS_DATA_OWNER:-andaoai}"
REPO="${NEXUS_DATA_REPO:-nexus-data}"
BRANCH="${NEXUS_DATA_BRANCH:-main}"
WORK="$(mktemp -d)/$REPO"

echo "==> 创建私有仓库 $OWNER/$REPO"
gh repo create "$OWNER/$REPO" --private \
  --description "NexusCRM 数据仓库（客户/供应商/方案/匹配 JSON，由 Nexus 服务读写）" 2>/dev/null || {
  echo "    仓库已存在，跳过创建"
}

echo "==> 克隆并初始化目录结构"
gh repo clone "$OWNER/$REPO" "$WORK" -- -b "$BRANCH" 2>/dev/null || gh repo clone "$OWNER/$REPO" "$WORK"
cd "$WORK"
git config user.name  "${GIT_AUTHOR_NAME:-claude}"
git config user.email "${GIT_AUTHOR_EMAIL:-noreply@anthropic.com}"

for d in customers/user1 customers/user2 customers/user3 suppliers solutions matches conversations; do
  mkdir -p "$d"
  touch "$d/.gitkeep"
done

cat > README.md <<'EOF'
# nexus-data

NexusCRM 的数据仓库（必须保持私有）。由 [Nexus](https://github.com/andaoai/Nexus) 服务自动读写，请勿手工编辑。

- `customers/<user>/*.json` — 客户（按负责人分目录）
- `suppliers/*.json` — 供应商
- `solutions/*.json` — 技术方案
- `matches/*.json` — 需求-方案匹配记录
- `conversations/` — AI 对话摘要（预留）

每次业务写操作都会产生一个 commit（`<user>: create customer c-xxxx` 形式），即审计日志。
EOF

if [ -n "$(git status -s)" ]; then
  git add -A
  git commit -qm "chore: init nexus-data directory structure"
  git push origin "$BRANCH"
  echo "==> 已推送初始结构"
else
  echo "==> 无变更"
fi
