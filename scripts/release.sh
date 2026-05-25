#!/bin/bash
set -euo pipefail

# release.sh — 自动版本管理：patch+1 → 更新version.go → commit → tag → 可选部署
#
# 用法:
#   ./scripts/release.sh "commit message"          # patch+1, commit, tag
#   ./scripts/release.sh "commit message" --deploy  # + 编译部署到测试服务器
#   ./scripts/release.sh --rollback v0.7.02         # 回滚到指定版本并部署
#   ./scripts/release.sh --list                     # 列出所有版本

VERSION_FILE="internal/version/version.go"
REMOTE_HOST="root@8.160.176.23"
REMOTE_BIN="/usr/local/bin/opendb"

# ── 列出所有版本 ──
if [[ "${1:-}" == "--list" ]]; then
    echo "所有版本:"
    git tag -l "v*" --sort=-version:refname
    echo ""
    echo "当前: $(grep 'Version.*=' "$VERSION_FILE" | sed 's/.*"\(.*\)"/\1/')"
    exit 0
fi

# ── 回滚到指定版本 ──
if [[ "${1:-}" == "--rollback" ]]; then
    TARGET="${2:?用法: release.sh --rollback v0.7.XX}"
    if ! git tag -l "$TARGET" | grep -q .; then
        echo "错误: 版本 $TARGET 不存在"
        echo "可用版本:"
        git tag -l "v*" --sort=-version:refname
        exit 1
    fi
    echo "回滚到 $TARGET ..."
    git checkout "$TARGET" -- .
    git checkout "$TARGET" -- "$VERSION_FILE"
    echo "已切换到 $TARGET 的代码（工作区已更新，未创建新提交）"
    echo ""
    read -p "是否编译部署到测试服务器? [y/N] " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        GOOS=linux GOARCH=amd64 go build -o opendb-linux ./cmd/opendb/
        scp opendb-linux "$REMOTE_HOST:/home/oracle/opendb"
        ssh "$REMOTE_HOST" "pkill opendb 2>/dev/null; sleep 1; cp /home/oracle/opendb $REMOTE_BIN"
        echo "已部署 $TARGET 到测试服务器"
    fi
    exit 0
fi

# ── 正常发版: patch+1 → commit → tag ──
MSG="${1:?用法: release.sh \"commit message\" [--deploy]}"
DEPLOY="${2:-}"

# 读取当前版本
CURRENT=$(grep 'Version.*=' "$VERSION_FILE" | sed 's/.*"\(.*\)"/\1/')
echo "当前版本: $CURRENT"

# 解析版本号: vX.Y.ZZ
if [[ "$CURRENT" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    MAJOR="${BASH_REMATCH[1]}"
    MINOR="${BASH_REMATCH[2]}"
    PATCH="${BASH_REMATCH[3]}"
else
    echo "错误: 无法解析版本号 '$CURRENT'"
    exit 1
fi

# patch+1
NEW_PATCH=$(printf "%02d" $((10#$PATCH + 1)))
NEW_VERSION="v${MAJOR}.${MINOR}.${NEW_PATCH}"
echo "新版本: $NEW_VERSION"

# 更新 version.go
sed -i.bak "s/Version.*=.*/Version   = \"${NEW_VERSION}\"/" "$VERSION_FILE"
rm -f "${VERSION_FILE}.bak"

# 提交
git add -A
git commit -m "${NEW_VERSION}: ${MSG}"
git tag "$NEW_VERSION"

echo "已提交并打标签: $NEW_VERSION"

# 可选部署
if [[ "$DEPLOY" == "--deploy" ]]; then
    echo "编译部署中..."
    GOOS=linux GOARCH=amd64 go build -o opendb-linux ./cmd/opendb/
    # 检查SSH隧道
    if ! ssh "$REMOTE_HOST" "ss -tlnp | grep 11434" &>/dev/null; then
        echo "SSH隧道已断，重建中..."
        ssh "$REMOTE_HOST" "fuser -k 11434/tcp 2>/dev/null" || true
        pkill -f "ssh -f -N -R 11434" 2>/dev/null || true
        sleep 1
        ssh -f -N -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -R 11434:localhost:11434 "$REMOTE_HOST"
        echo "隧道已恢复"
    fi
    scp opendb-linux "$REMOTE_HOST:/home/oracle/opendb"
    ssh "$REMOTE_HOST" "pkill opendb 2>/dev/null; sleep 1; cp /home/oracle/opendb $REMOTE_BIN"
    echo "已部署 $NEW_VERSION 到测试服务器"
fi

echo "完成!"
