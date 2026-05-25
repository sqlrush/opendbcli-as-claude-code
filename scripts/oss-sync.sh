#!/bin/bash
# oss-sync.sh — sync opendb main branch source to the open-source repo
#                https://github.com/sqlrush/opendbcli-as-claude-code
#
# Strict policy: this script is ONLY run when the user says "sync to OSS".
# Regular release flow (publish.sh) does NOT call this. Open-source repo
# carries selected snapshots, not every version.
#
# Usage:
#   ./scripts/oss-sync.sh                # use version from internal/version/version.go
#   ./scripts/oss-sync.sh --version v1.1.32  # override
#   ./scripts/oss-sync.sh --dry-run      # stage only, no commit/push
#   ./scripts/oss-sync.sh --skip-build   # skip the smoke compile check
#
# What gets included:
#   cmd/opendb, cmd/gaussdb-probe         (user CLI tools)
#   cmd/{loadtest,skilltest,viewtest,e2etest,e2e_sentinel}  (example tools)
#   internal/                             (engine, skills, drivers, sqltune,
#                                          sqlfetch, sentinel, scheduler, etc.)
#   scripts/                              (build helpers, publish.sh)
#   configs/, install/, faults/, tests/, website/
#   LICENSE, Makefile, go.mod, go.sum
#
# What gets excluded (private / Claude Code / internal designs):
#   docs/                                 internal design docs (all)
#   CLAUDE.md, .claude/, .memory/         Claude Code project + memory
#   .pipeline/, .superpowers/, .claudeignore
#   internal/brand/dbaa.go                private client brand
#   internal/brand/linkdb.go              private client brand
#   internal/_dmdriver/, internal/dm/     DM proprietary driver
#   cmd/opendb/product_dm.go              DM registration (depends on _dmdriver)
#   cmd/opus-forwarder/                   internal Claude Opus relay
#   generate_ppt_*.py, *.pptx             sales decks
#   release/, dist/                       build artifacts
#
# README handling: target repo's README.md / README_zh.md are PRESERVED;
# this script appends a "Build from Source / 从源码编译" section before
# the closing tagline, and rewrites all sqlrush/opendb URLs to point at
# sqlrush/opendbcli-as-claude-code.

set -euo pipefail

# ── Config ───────────────────────────────────────────
TARGET_REPO="sqlrush/opendbcli-as-claude-code"
TARGET_URL="git@github.com:${TARGET_REPO}.git"
STAGE_DIR="/tmp/opendbcli-os-staging"
TARGET_CLONE_DIR="/tmp/opendbcli-os-clone"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# ── Args ─────────────────────────────────────────────
VERSION=""
DRY_RUN=0
SKIP_BUILD=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)        DRY_RUN=1; shift ;;
        --skip-build)     SKIP_BUILD=1; shift ;;
        --version)        VERSION="$2"; shift 2 ;;
        -h|--help)        sed -n '4,40p' "$0" | sed 's/^# \?//'; exit 0 ;;
        *)                echo "unknown arg: $1" >&2; exit 1 ;;
    esac
done

if [[ -z "$VERSION" ]]; then
    VERSION=$(grep -E '^\s*Version\s*=' internal/version/version.go | awk -F'"' '{print $2}')
fi
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "✗ invalid version: $VERSION" >&2; exit 1; }

# ── Exclude patterns (rsync style, $REPO_ROOT relative) ──
EXCLUDES=(
    # VCS / IDE
    '.git' '.gitignore' '.DS_Store'
    # Claude Code-related
    '.claude' '.memory' '.pipeline' '.superpowers' '.claudeignore'
    'CLAUDE.md'
    # Sales decks
    'generate_ppt*.py' '*.pptx'
    # PRD / sales material (private to brand customers)
    '/prd'
    # Build artifacts (root-anchored)
    '/release' '/dist' '/dbaa' '/linkdb' '/opendb' '/opendb-linux-stripped'
    '/opendb-linux' '/mysql-scenario-test' '/pg-scenario-test'
    '/loadtest-bin' '/loadtest-linux' '/viewtest-linux' '/skilltest-linux'
    '/~'
    # Internal design docs (entire docs/ tree excluded)
    '/docs'
    '/website-old'
    # Brand variants (private clients)
    'internal/brand/dbaa.go'
    'internal/brand/linkdb.go'
    # DM driver (proprietary 达梦 vendor code) and dependents
    'internal/_dmdriver'
    'internal/dm'
    'cmd/opendb/product_dm.go'
    # Internal Claude Opus relay
    'cmd/opus-forwarder'
    # Maintainer tool — references ssh push to OSS repo, only useful to repo owner
    'scripts/oss-sync.sh'
    # Backups and logs
    '*.bak.*'
)

# Build rsync --exclude args
RSYNC_EXCLUDES=()
for p in "${EXCLUDES[@]}"; do
    RSYNC_EXCLUDES+=(--exclude="$p")
done

# ── Banner ───────────────────────────────────────────
echo "════════════════════════════════════════════════════"
echo "  OpenDB OSS Sync Pipeline"
echo "  Source:    $REPO_ROOT (main branch)"
echo "  Version:   $VERSION"
echo "  Target:    $TARGET_URL"
echo "  Stage:     $STAGE_DIR"
echo "  Mode:      $([[ $DRY_RUN -eq 1 ]] && echo 'dry-run' || echo 'commit + push')"
echo "════════════════════════════════════════════════════"

# Confirm we're on main and clean
local_branch=$(git rev-parse --abbrev-ref HEAD)
if [[ "$local_branch" != "main" ]]; then
    echo "✗ refuse to sync from non-main branch ($local_branch). Run from main." >&2
    exit 1
fi
if ! git diff-index --quiet HEAD --; then
    echo "⚠ working tree has uncommitted changes — they WILL be included in the sync." >&2
    echo "  Press Enter to continue or Ctrl+C to abort..."
    read -r
fi

# ── Step 1: stage ────────────────────────────────────
echo ""
echo "▶ [1/5] Stage source with excludes..."
rm -rf "$STAGE_DIR"
mkdir -p "$STAGE_DIR"
rsync -a "${RSYNC_EXCLUDES[@]}" "$REPO_ROOT/" "$STAGE_DIR/"
echo "  ✓ staged $(find "$STAGE_DIR" -type f | wc -l | tr -d ' ') files / $(du -sh "$STAGE_DIR" | cut -f1)"

# ── Step 2: smoke compile ────────────────────────────
if [[ $SKIP_BUILD -eq 0 ]]; then
    echo ""
    echo "▶ [2/5] Smoke compile in staging..."
    ( cd "$STAGE_DIR" && go build -tags 'oracle mysql postgres opengauss gaussdb' \
        -ldflags='-s -w' -o /tmp/opendb-oss-smoke ./cmd/opendb/ 2>&1 | tail -5 )
    /tmp/opendb-oss-smoke --version
    rm -f /tmp/opendb-oss-smoke
    echo "  ✓ build OK"
else
    echo ""
    echo "▶ [2/5] Smoke compile skipped (--skip-build)"
fi

# ── Step 3: clone target + preserve READMEs ──────────
echo ""
echo "▶ [3/5] Clone target repo..."
rm -rf "$TARGET_CLONE_DIR"
git clone --depth 50 "$TARGET_URL" "$TARGET_CLONE_DIR" 2>&1 | tail -3

# Preserve target's existing READMEs (they're public-facing copy that the user
# carefully crafted — we only append build section, don't replace).
cp "$TARGET_CLONE_DIR/README.md"    "$STAGE_DIR/README.md"
cp "$TARGET_CLONE_DIR/README_zh.md" "$STAGE_DIR/README_zh.md"
echo "  ✓ preserved target README.md and README_zh.md"

# ── Step 4: append build section + fix URLs ──────────
echo ""
echo "▶ [4/5] Append Build-from-Source section + rewrite URLs..."

write_build_en() {
    cat <<'EOF'

## Build from Source

Five minutes from clone to a working binary.

### Prerequisites

- **Go 1.24+** (`go version` to verify)
- **Git**

Optional:
- **UPX** — compresses Linux binary ~80% (53 MB → 10 MB). `brew install upx` / `apt install upx-ucl`.
- **codesign** — required for macOS Apple Silicon binaries (built into macOS). Without re-signing after `cp`, the kernel kills the binary with no message.

### Clone & Build

```bash
git clone https://github.com/sqlrush/opendbcli-as-claude-code.git
cd opendbcli-as-claude-code

# Build for current platform with all DB drivers
go build -tags 'oracle mysql postgres opengauss gaussdb' -ldflags='-s -w' -o opendb ./cmd/opendb/

./opendb --version
```

### Build Tags

| Tag | Database |
|---|---|
| `oracle` | Oracle (go-ora pure-Go driver, no Oracle Client needed) |
| `mysql` | MySQL |
| `postgres` | PostgreSQL |
| `opengauss` | openGauss (open-source community, pgx-compatible) |
| `gaussdb` | GaussDB (Huawei commercial, gaussdb-go, SCRAM-SHA256(10)) |
| `full` | All five above |

DM (达梦) driver is proprietary; out-of-scope for this open-source repo.

### Cross-Compile

```bash
# Linux x86_64
GOOS=linux GOARCH=amd64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-linux-amd64 ./cmd/opendb/

# Linux ARM64 (Kunpeng / Phytium)
GOOS=linux GOARCH=arm64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-linux-arm64 ./cmd/opendb/

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-darwin-arm64 ./cmd/opendb/

# macOS Intel
GOOS=darwin GOARCH=amd64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-darwin-amd64 ./cmd/opendb/
```

### Compress (optional, Linux only)

```bash
upx --best --lzma opendb-linux-amd64
# Typical: 53 MB → 10 MB
```

### macOS Apple Silicon: Re-sign After Copy

```bash
codesign --force -s - opendb-darwin-arm64
chmod +x opendb-darwin-arm64
sudo cp opendb-darwin-arm64 /usr/local/bin/opendb
sudo codesign --force -s - /usr/local/bin/opendb
```

Skipping this gives "zsh: killed opendb" with no further explanation.

### One-Shot Pipeline

`scripts/publish.sh` automates four-platform build + UPX + codesign + gzip + sha256:

```bash
./scripts/publish.sh --build-only
# Artifacts: dist/releases/<version>/opendb/upload/*.gz + .sha256
```

### Run Tests

```bash
go test -tags 'oracle mysql postgres opengauss gaussdb' ./...
```

---
EOF
}

write_build_zh() {
    cat <<'EOF'

## 从源码编译

5 分钟从 clone 到可运行二进制。

### 前置依赖

- **Go 1.24+**（`go version` 验证）
- **Git**

可选：
- **UPX** — 压缩 Linux 二进制 ~80%（53 MB → 10 MB）。`brew install upx` / `apt install upx-ucl`。
- **codesign** — macOS Apple Silicon 必须（系统自带）。`cp` 后不重签会被内核 kill，无任何错误提示。

### Clone 与编译

```bash
git clone https://github.com/sqlrush/opendbcli-as-claude-code.git
cd opendbcli-as-claude-code

# 当前平台 + 全部数据库驱动
go build -tags 'oracle mysql postgres opengauss gaussdb' -ldflags='-s -w' -o opendb ./cmd/opendb/

./opendb --version
```

### Build Tags（按需挑驱动）

| Tag | 数据库 |
|---|---|
| `oracle` | Oracle（go-ora 纯 Go，免装 Oracle Client）|
| `mysql` | MySQL |
| `postgres` | PostgreSQL |
| `opengauss` | openGauss（开源社区版，pgx 兼容）|
| `gaussdb` | GaussDB（华为商业版，gaussdb-go，SCRAM-SHA256(10)）|
| `full` | 上面 5 个全包含 |

> 达梦 DM 驱动是商业代码，不在本开源仓范围。

### 交叉编译

```bash
# Linux x86_64
GOOS=linux GOARCH=amd64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-linux-amd64 ./cmd/opendb/

# Linux ARM64（鲲鹏/飞腾国产化）
GOOS=linux GOARCH=arm64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-linux-arm64 ./cmd/opendb/

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-darwin-arm64 ./cmd/opendb/

# macOS Intel
GOOS=darwin GOARCH=amd64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-darwin-amd64 ./cmd/opendb/
```

### 压缩（可选，仅 Linux）

```bash
upx --best --lzma opendb-linux-amd64
# 实测: 53 MB → 10 MB
```

### macOS Apple Silicon：cp 后重签名

```bash
codesign --force -s - opendb-darwin-arm64
chmod +x opendb-darwin-arm64
sudo cp opendb-darwin-arm64 /usr/local/bin/opendb
sudo codesign --force -s - /usr/local/bin/opendb
```

不重签会得到 `zsh: killed opendb`，无任何错误提示。

### 一键流水线

`scripts/publish.sh` 自动 4 平台编译 + UPX + codesign + gzip + sha256：

```bash
./scripts/publish.sh --build-only
# 产物: dist/releases/<version>/opendb/upload/*.gz + .sha256
```

### 跑测试

```bash
go test -tags 'oracle mysql postgres opengauss gaussdb' ./...
```

---
EOF
}

# Insert build section before the LAST <p align="center"> (closing tagline)
insert_before_last_tagline() {
    local readme="$1"
    local section_func="$2"
    local last_p
    last_p=$(grep -n '<p align="center">' "$readme" | tail -1 | cut -d: -f1)
    if [[ -z "$last_p" ]]; then
        echo "  ⚠ no closing <p align=\"center\"> found in $readme — appending at EOF" >&2
        $section_func >> "$readme"
        return
    fi
    local tmp; tmp=$(mktemp)
    head -n $((last_p - 1)) "$readme" > "$tmp"
    $section_func >> "$tmp"
    sed -n "${last_p},\$p" "$readme" >> "$tmp"
    mv "$tmp" "$readme"
}

# Skip if README already has a "Build from Source" section (idempotent re-run)
if ! grep -q '^## Build from Source' "$STAGE_DIR/README.md"; then
    insert_before_last_tagline "$STAGE_DIR/README.md"    write_build_en
    echo "  ✓ inserted EN build section"
else
    echo "  ✓ EN build section already present, skipping"
fi
if ! grep -q '^## 从源码编译' "$STAGE_DIR/README_zh.md"; then
    insert_before_last_tagline "$STAGE_DIR/README_zh.md" write_build_zh
    echo "  ✓ inserted ZH build section"
else
    echo "  ✓ ZH build section already present, skipping"
fi

# Rewrite private repo URLs to OSS repo URLs
# (a) "github.com/sqlrush/opendb/" → "github.com/sqlrush/opendbcli-as-claude-code/"
# (b) shields.io badge "release/sqlrush/opendb?" → "release/sqlrush/opendbcli-as-claude-code?"
for f in "$STAGE_DIR/README.md" "$STAGE_DIR/README_zh.md"; do
    sed -i.bak \
        -e 's|github.com/sqlrush/opendb/|github.com/sqlrush/opendbcli-as-claude-code/|g' \
        -e 's|release/sqlrush/opendb?|release/sqlrush/opendbcli-as-claude-code?|g' \
        "$f"
    rm -f "$f.bak"
done
echo "  ✓ rewrote sqlrush/opendb URLs to sqlrush/opendbcli-as-claude-code"

# .gitignore for the OSS repo (don't carry over opendb's .gitignore which
# excludes pptx/ppt scripts that don't exist in OSS)
cat > "$STAGE_DIR/.gitignore" <<'EOF'
# Build outputs
/opendb
/opendb-*
/dbaa
/linkdb
/dist/
/release/
*.test

# OS / IDE
.DS_Store
.vscode/
.idea/

# Credentials / secrets — never commit
*.bin
*.env
.env.*

# Logs and run-time
*.log
*.pid

# Temporary build artifacts
/tmp_*
*.tmp
EOF

# ── Step 5: sync into clone + commit + push ──────────
echo ""
echo "▶ [5/5] Sync staging → target clone..."
rsync -a --delete --exclude='.git' "$STAGE_DIR/" "$TARGET_CLONE_DIR/"

cd "$TARGET_CLONE_DIR"
if git diff-index --quiet HEAD -- 2>/dev/null && [[ -z "$(git ls-files --others --exclude-standard)" ]]; then
    echo "  ✓ no changes vs target — already up to date"
    exit 0
fi

git add -A
changes=$(git status --short | wc -l | tr -d ' ')
echo "  ✓ $changes file(s) to commit"

if [[ $DRY_RUN -eq 1 ]]; then
    echo ""
    echo "▶ DRY RUN — would commit + push, but stopping here."
    echo "  Inspect changes: cd $TARGET_CLONE_DIR && git diff --stat HEAD"
    exit 0
fi

short_sha=$(git -C "$REPO_ROOT" rev-parse --short HEAD)
git commit -m "release: open-source ${VERSION} source code

Snapshot of opendb main @ ${short_sha} → public source.

Includes: cmd/opendb, cmd/gaussdb-probe, internal/, scripts/,
LICENSE, Makefile, configs/, install/, faults/, tests/, website/.

Excludes: docs/ (internal designs), CLAUDE.md / .claude / .memory
(Claude Code project + memory), .pipeline / .superpowers
(RemoteTrigger + skills), internal/brand/{dbaa,linkdb}.go (private
client variants), internal/_dmdriver / internal/dm (DM proprietary
driver), cmd/opus-forwarder (internal Claude relay).

README.md / README_zh.md: 'Build from Source / 从源码编译' section
present, sqlrush/opendb URLs rewritten to sqlrush/opendbcli-as-claude-code.

Generated by scripts/oss-sync.sh." 2>&1 | tail -3

git push origin main 2>&1 | tail -3

echo ""
echo "════════════════════════════════════════════════════"
echo "  ✓ Synced to https://github.com/$TARGET_REPO"
echo "  Version: $VERSION (from main @ $short_sha)"
echo "════════════════════════════════════════════════════"
