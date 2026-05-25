#!/bin/bash
# publish.sh — one-shot release pipeline for opendb (and brand variants)
#
# Builds 4-platform binaries, UPX-compresses Linux, codesigns darwin,
# gzips for download, generates per-file sha256, uploads to opendbcli.org,
# updates latest-version, verifies via HTTPS.
#
# Usage:
#   ./scripts/publish.sh                    # full pipeline (build + upload + verify)
#   ./scripts/publish.sh --build-only       # local build only, no upload
#   ./scripts/publish.sh --no-update-latest # upload but don't bump latest-version
#   ./scripts/publish.sh --version v1.1.32  # override version (default: read from version.go)
#   ./scripts/publish.sh --brand all        # build all 3 brands (opendb/dbaa/linkdb)
#   ./scripts/publish.sh --brand opendb     # opendb only (default)
#   ./scripts/publish.sh --probe            # also build gaussdb-probe standalone tool
#
# Environment:
#   OPENDB_RELEASE_HOST   default 43.110.57.194
#   OPENDB_RELEASE_USER   default root
#   OPENDB_RELEASE_PORT   default 22
#   OPENDB_RELEASE_PASS   if set, uses sshpass; else relies on ssh-agent / key auth
#   OPENDB_RELEASE_BASE   default /var/www/opendbcli.org
#
# Server convention (matches existing opendbcli.org layout):
#   ${BASE}/releases/${VER}/opendb-${OS}-${ARCH}.gz
#   ${BASE}/releases/${VER}/opendb-${OS}-${ARCH}.gz.sha256
#   ${BASE}/latest-version (single line, e.g. "v1.1.31")
#
# Exit codes:
#   0  success
#   1  arg / version error
#   2  build / package error
#   3  upload error
#   4  HTTPS verification failure

set -euo pipefail

# ── Config ───────────────────────────────────────────
HOST="${OPENDB_RELEASE_HOST:-43.110.57.194}"
USER="${OPENDB_RELEASE_USER:-root}"
PORT="${OPENDB_RELEASE_PORT:-22}"
BASE="${OPENDB_RELEASE_BASE:-/var/www/opendbcli.org}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

DIST_DIR="$REPO_ROOT/dist"
PLATFORMS=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64")

# ── Args ─────────────────────────────────────────────
VERSION=""
BUILD_ONLY=0
SKIP_LATEST=0
BRAND="opendb"
INCLUDE_PROBE=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --build-only)         BUILD_ONLY=1; shift ;;
        --no-update-latest)   SKIP_LATEST=1; shift ;;
        --version)            VERSION="$2"; shift 2 ;;
        --brand)              BRAND="$2"; shift 2 ;;
        --probe)              INCLUDE_PROBE=1; shift ;;
        -h|--help)
            sed -n '4,28p' "$0" | sed 's/^# \?//'; exit 0 ;;
        *)
            echo "unknown arg: $1" >&2
            echo "run: $0 --help" >&2
            exit 1 ;;
    esac
done

# ── Version detection ────────────────────────────────
if [[ -z "$VERSION" ]]; then
    VERSION=$(grep -E '^\s*Version\s*=' internal/version/version.go | awk -F'"' '{print $2}')
fi
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "✗ invalid version: $VERSION (expect vX.Y.ZZ)" >&2; exit 1; }

# ── Tools required ───────────────────────────────────
require() {
    command -v "$1" >/dev/null 2>&1 || { echo "✗ required tool missing: $1 ($2)" >&2; exit 1; }
}
require go "install Go 1.24+"
require gzip "install gzip"
require shasum "(macOS built-in) or use sha256sum"

if [[ $BUILD_ONLY -eq 0 ]]; then
    if [[ -n "${OPENDB_RELEASE_PASS:-}" ]]; then
        require sshpass "brew install hudochenkov/sshpass/sshpass"
    else
        require ssh ""
    fi
    require curl ""
fi

# ── Brand expansion ──────────────────────────────────
case "$BRAND" in
    opendb)         BRANDS=("opendb") ;;
    dbaa)           BRANDS=("dbaa") ;;
    linkdb)         BRANDS=("linkdb") ;;
    all)            BRANDS=("opendb" "dbaa" "linkdb") ;;
    *)              echo "✗ unknown brand: $BRAND (use opendb/dbaa/linkdb/all)" >&2; exit 1 ;;
esac

# ── Banner ───────────────────────────────────────────
echo "════════════════════════════════════════════════════"
echo "  OpenDB Release Pipeline"
echo "  Version:  $VERSION"
echo "  Brands:   ${BRANDS[*]}"
echo "  Probe:    $([[ $INCLUDE_PROBE -eq 1 ]] && echo yes || echo no)"
echo "  Mode:     $([[ $BUILD_ONLY -eq 1 ]] && echo 'build-only' || echo 'build + upload')"
[[ $BUILD_ONLY -eq 0 ]] && echo "  Host:     $USER@$HOST:$PORT"
[[ $BUILD_ONLY -eq 0 ]] && echo "  Base:     $BASE"
echo "════════════════════════════════════════════════════"

# ── Build one brand × all platforms ──────────────────
build_brand() {
    local brand="$1"
    local out_dir="$DIST_DIR/releases/$VERSION/$brand"
    mkdir -p "$out_dir"

    local ldflags="-s -w"
    ldflags="$ldflags -X github.com/sqlrush/opendb/internal/version.Version=$VERSION"
    ldflags="$ldflags -X github.com/sqlrush/opendb/internal/version.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    ldflags="$ldflags -X github.com/sqlrush/opendb/internal/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    echo ""
    echo "▶ Build $brand × 4 platforms..."
    for platform in "${PLATFORMS[@]}"; do
        local os="${platform%%/*}"
        local arch="${platform##*/}"
        # darwin can't compile DM driver — drop it; linux+full includes everything
        local tags
        if [[ "$os" == "darwin" ]]; then
            tags="oracle mysql postgres opengauss gaussdb"
        else
            tags="full"
        fi
        # Brand tag added on top (no-op for opendb default)
        if [[ "$brand" != "opendb" ]]; then
            tags="$tags $brand"
        fi
        local out="$out_dir/$brand-$os-$arch"
        printf "  %-6s %-7s tags=[%-50s]" "$os" "$arch" "$tags"
        GOOS="$os" GOARCH="$arch" go build -tags "$tags" -ldflags "$ldflags" -o "$out" ./cmd/opendb/
        printf " ✓ %s bytes\n" "$(stat -f %z "$out" 2>/dev/null || stat -c %s "$out")"
    done
}

# ── UPX Linux + codesign darwin ──────────────────────
finalize_brand() {
    local brand="$1"
    local out_dir="$DIST_DIR/releases/$VERSION/$brand"

    if command -v upx >/dev/null 2>&1; then
        echo "▶ UPX compress $brand Linux binaries..."
        for f in "$out_dir/$brand"-linux-*; do
            upx --best --lzma "$f" >/dev/null 2>&1 && \
                printf "  %-30s ✓ %s bytes\n" "$(basename "$f")" "$(stat -f %z "$f" 2>/dev/null || stat -c %s "$f")"
        done
    else
        echo "  ⚠ upx not installed — Linux binaries stay uncompressed"
    fi

    if [[ "$(uname)" == "Darwin" ]]; then
        echo "▶ codesign $brand darwin binaries..."
        for f in "$out_dir/$brand"-darwin-*; do
            codesign --force -s - "$f" >/dev/null 2>&1 && \
                printf "  %-30s ✓ signed\n" "$(basename "$f")"
        done
    fi
}

# ── Gzip + per-file sha256 ───────────────────────────
package_brand() {
    local brand="$1"
    local out_dir="$DIST_DIR/releases/$VERSION/$brand"
    local gz_dir="$out_dir/upload"

    echo "▶ Package $brand for upload (gzip + sha256)..."
    rm -rf "$gz_dir"
    mkdir -p "$gz_dir"
    for f in "$out_dir/$brand"-{linux,darwin}-{amd64,arm64}; do
        [[ -f "$f" ]] || continue
        local name; name=$(basename "$f")
        gzip -c "$f" > "$gz_dir/$name.gz"
        ( cd "$gz_dir" && shasum -a 256 "$name.gz" > "$name.gz.sha256" )
        printf "  %-25s ✓ %d bytes\n" "$name.gz" "$(stat -f %z "$gz_dir/$name.gz" 2>/dev/null || stat -c %s "$gz_dir/$name.gz")"
    done
}

# ── gaussdb-probe (optional standalone) ──────────────
build_probe() {
    local out_dir="$DIST_DIR/releases/$VERSION/probe"
    mkdir -p "$out_dir"
    echo ""
    echo "▶ Build gaussdb-probe × 4 platforms..."
    for platform in "${PLATFORMS[@]}"; do
        local os="${platform%%/*}"
        local arch="${platform##*/}"
        local out="$out_dir/gaussdb-probe-$os-$arch"
        GOOS="$os" GOARCH="$arch" go build -ldflags "-s -w" -o "$out" ./cmd/gaussdb-probe/ 2>&1 | tail -1
        gzip -f "$out"
        ( cd "$out_dir" && shasum -a 256 "gaussdb-probe-$os-$arch.gz" > "gaussdb-probe-$os-$arch.gz.sha256" )
        printf "  %-30s ✓\n" "gaussdb-probe-$os-$arch.gz"
    done
}

# ── SSH / SCP wrappers ───────────────────────────────
ssh_cmd() {
    if [[ -n "${OPENDB_RELEASE_PASS:-}" ]]; then
        sshpass -p "$OPENDB_RELEASE_PASS" ssh -p "$PORT" \
            -o StrictHostKeyChecking=accept-new \
            -o PreferredAuthentications=password \
            -o PubkeyAuthentication=no \
            "$USER@$HOST" "$@"
    else
        ssh -p "$PORT" "$USER@$HOST" "$@"
    fi
}
scp_cmd() {
    if [[ -n "${OPENDB_RELEASE_PASS:-}" ]]; then
        sshpass -p "$OPENDB_RELEASE_PASS" scp -P "$PORT" \
            -o StrictHostKeyChecking=accept-new \
            -o PreferredAuthentications=password \
            -o PubkeyAuthentication=no \
            "$@"
    else
        scp -P "$PORT" "$@"
    fi
}

# ── Upload one brand ─────────────────────────────────
upload_brand() {
    local brand="$1"
    local gz_dir="$DIST_DIR/releases/$VERSION/$brand/upload"
    local dest="$BASE/releases/$VERSION"

    echo ""
    echo "▶ Upload $brand → $USER@$HOST:$dest/"
    ssh_cmd "mkdir -p $dest"
    scp_cmd "$gz_dir"/* "$USER@$HOST:$dest/"
    echo "  ✓ $(ls "$gz_dir" | wc -l | tr -d ' ') files uploaded"
}

upload_probe() {
    [[ $INCLUDE_PROBE -eq 0 ]] && return
    local out_dir="$DIST_DIR/releases/$VERSION/probe"
    local dest="$BASE/releases/$VERSION"
    echo "▶ Upload gaussdb-probe..."
    scp_cmd "$out_dir"/gaussdb-probe-* "$USER@$HOST:$dest/"
}

# ── Update latest-version ────────────────────────────
update_latest() {
    [[ $SKIP_LATEST -eq 1 ]] && { echo "▶ Skip latest-version bump (--no-update-latest)"; return; }
    echo "▶ Update latest-version → $VERSION"
    ssh_cmd "echo '$VERSION' > $BASE/latest-version" && \
        echo "  ✓ server now reports: $(ssh_cmd "cat $BASE/latest-version" | tr -d '\r\n')"
}

# ── HTTPS verify ─────────────────────────────────────
verify_https() {
    echo ""
    echo "▶ Verify via HTTPS (https://www.opendbcli.org/)..."
    if [[ $SKIP_LATEST -eq 0 ]]; then
        local served
        served=$(curl -sS https://www.opendbcli.org/latest-version | tr -d '\r\n')
        if [[ "$served" == "$VERSION" ]]; then
            echo "  ✓ latest-version → $served"
        else
            echo "  ✗ latest-version mismatch (server: $served, expected: $VERSION)" >&2
            return 4
        fi
    fi
    local fails=0
    # Verify primary brand only (opendb), or first brand if opendb not in list
    local prim="opendb"
    [[ " ${BRANDS[*]} " == *" opendb "* ]] || prim="${BRANDS[0]}"
    for arch in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
        local code
        code=$(curl -sI -o /dev/null -w "%{http_code}" "https://www.opendbcli.org/releases/$VERSION/$prim-$arch.gz")
        if [[ "$code" == "200" ]]; then
            printf "  ✓ %-35s HTTP %s\n" "$prim-$arch.gz" "$code"
        else
            printf "  ✗ %-35s HTTP %s\n" "$prim-$arch.gz" "$code"
            ((fails++))
        fi
    done
    [[ $fails -gt 0 ]] && { echo "  ✗ $fails asset(s) failed HTTPS check" >&2; return 4; }
    echo "  ✓ all 4 binaries reachable via HTTPS"
}

# ── Main ─────────────────────────────────────────────
for brand in "${BRANDS[@]}"; do
    build_brand "$brand"
    finalize_brand "$brand"
    package_brand "$brand"
done

[[ $INCLUDE_PROBE -eq 1 ]] && build_probe

if [[ $BUILD_ONLY -eq 1 ]]; then
    echo ""
    echo "✓ Local build complete. Skipped upload (--build-only)."
    echo "  Local artifacts: $DIST_DIR/releases/$VERSION/<brand>/upload/"
    exit 0
fi

for brand in "${BRANDS[@]}"; do
    upload_brand "$brand"
done
upload_probe
update_latest
verify_https

echo ""
echo "════════════════════════════════════════════════════"
echo "  ✓ Release $VERSION published"
echo "  https://www.opendbcli.org/releases/$VERSION/"
echo "  https://www.opendbcli.org/latest-version → $VERSION"
echo "════════════════════════════════════════════════════"
