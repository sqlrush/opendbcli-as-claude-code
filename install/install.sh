#!/bin/bash
# OpenDB Installer
# Usage: curl -fsSL https://www.opendbcli.org/install.sh | bash
set -euo pipefail

# ── Colors ───────────────────────────────────────────

PURPLE='\033[0;35m'
CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
DIM='\033[2m'
BOLD='\033[1m'
NC='\033[0m'

# ── Banner ───────────────────────────────────────────

print_banner() {
    echo ""
    echo -e "  ${PURPLE}${BOLD}OpenDB Installer${NC}"
    echo -e "  ${DIM}DB CLI Agent as Claude Code${NC}"
    echo ""
}

# ── Detect Platform ──────────────────────────────────

detect_platform() {
    local os arch

    case "$(uname -s)" in
        Linux)  os="linux" ;;
        Darwin) os="darwin" ;;
        *)
            echo -e "${RED}✗ Unsupported OS: $(uname -s)${NC}"
            echo "  OpenDB supports Linux and macOS."
            exit 1
            ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64)  arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *)
            echo -e "${RED}✗ Unsupported architecture: $(uname -m)${NC}"
            echo "  OpenDB supports amd64 and arm64."
            exit 1
            ;;
    esac

    echo "${os}/${arch}"
}

# ── Select Mirror ────────────────────────────────────

select_mirror() {
    local CN_MIRROR="${OPENDB_CN_MIRROR:-https://www.opendbcli.org}"
    local INTL_MIRROR="${OPENDB_MIRROR:-https://www.opendbcli.org}"

    # Simple geo-detection: try China mirror first with short timeout
    if curl -s --noproxy 'localhost,127.0.0.1' --connect-timeout 2 "${CN_MIRROR}/ping" >/dev/null 2>&1; then
        echo "${CN_MIRROR}"
        return
    fi
    echo "${INTL_MIRROR}"
}

# ── Get Latest Version ───────────────────────────────

get_latest_version() {
    local mirror="$1"
    local version

    version=$(curl -fsSL --noproxy 'localhost,127.0.0.1' --connect-timeout 5 "${mirror}/latest-version" 2>/dev/null || echo "")
    if [ -z "${version}" ]; then
        echo -e "${RED}✗ Failed to fetch latest version${NC}" >&2
        exit 1
    fi
    echo "${version}"
}

# ── Download & Install ───────────────────────────────

download_and_install() {
    local mirror="$1"
    local version="$2"
    local platform="$3"
    local os="${platform%%/*}"
    local arch="${platform##*/}"

    local binary_name="opendb-${os}-${arch}"
    local gz_name="${binary_name}.gz"
    local url="${mirror}/releases/${version}/${gz_name}"
    local checksum_url="${url}.sha256"
    local install_dir="${OPENDB_INSTALL_DIR:-/usr/local/bin}"
    local tmp_dir

    tmp_dir=$(mktemp -d)
    trap 'rm -rf "${tmp_dir:-}"' EXIT

    # Download binary (with resume + retry)
    echo -e "  ${DIM}·${NC} Downloading opendb ${version}..."
    local max_retries=5
    local attempt=1
    while [ ${attempt} -le ${max_retries} ]; do
        if curl -fSL -C - --noproxy 'localhost,127.0.0.1' --connect-timeout 10 --max-time 120 --progress-bar -o "${tmp_dir}/${gz_name}" "${url}"; then
            break
        fi
        if [ ${attempt} -eq ${max_retries} ]; then
            echo -e "  ${RED}✗ Download failed after ${max_retries} attempts${NC}"
            echo "  URL: ${url}"
            exit 1
        fi
        echo -e "  ${YELLOW}⚠ Attempt ${attempt} failed, resuming...${NC}"
        attempt=$((attempt + 1))
        sleep 1
    done
    echo -e "  ${GREEN}✓${NC} Download complete"

    # Verify checksum if available
    echo -e "  ${DIM}·${NC} Verifying checksum..."
    if curl -fsSL --noproxy 'localhost,127.0.0.1' -o "${tmp_dir}/checksum.txt" "${checksum_url}" 2>/dev/null; then
        # sha256 file contains original filename (e.g. opendb-darwin-arm64),
        # but we saved as "opendb". Extract hash and compare directly.
        local expected_hash
        expected_hash=$(awk '{print $1}' "${tmp_dir}/checksum.txt")
        local actual_hash
        if command -v sha256sum >/dev/null 2>&1; then
            actual_hash=$(sha256sum "${tmp_dir}/${gz_name}" | awk '{print $1}')
        elif command -v shasum >/dev/null 2>&1; then
            actual_hash=$(shasum -a 256 "${tmp_dir}/${gz_name}" | awk '{print $1}')
        else
            echo -e "  ${YELLOW}⚠${NC} No checksum tool available (skipping)"
            actual_hash=""
        fi
        if [ -n "${actual_hash}" ]; then
            if [ "${expected_hash}" = "${actual_hash}" ]; then
                echo -e "  ${GREEN}✓${NC} Checksum verified"
            else
                echo -e "  ${YELLOW}⚠${NC} Checksum mismatch (continuing)"
            fi
        fi
    else
        echo -e "  ${YELLOW}⚠${NC} Checksum not available (skipping)"
    fi

    # Decompress and install binary
    gunzip -f "${tmp_dir}/${gz_name}"
    mv "${tmp_dir}/${binary_name}" "${tmp_dir}/opendb"
    chmod +x "${tmp_dir}/opendb"
    if mkdir -p "${install_dir}" 2>/dev/null && [ -w "${install_dir}" ]; then
        mv "${tmp_dir}/opendb" "${install_dir}/opendb"
    else
        echo -e "  ${DIM}·${NC} Installing to ${install_dir} (requires sudo)..."
        sudo mkdir -p "${install_dir}"
        sudo mv "${tmp_dir}/opendb" "${install_dir}/opendb"
    fi
    echo -e "  ${GREEN}✓${NC} OpenDB installed to ${install_dir}/opendb"
}

# ── Verify Installation ─────────────────────────────

verify_install() {
    local install_dir="${OPENDB_INSTALL_DIR:-/usr/local/bin}"
    local opendb_bin="${install_dir}/opendb"

    if [ ! -x "${opendb_bin}" ]; then
        echo -e "  ${RED}✗ opendb not found at ${opendb_bin}${NC}"
        exit 1
    fi
    local ver
    ver=$("${opendb_bin}" --version 2>&1 | head -1)
    echo -e "  ${GREEN}✓${NC} ${ver}"
}

# ── Main ─────────────────────────────────────────────

main() {
    print_banner

    # Detect platform
    local platform
    platform=$(detect_platform)
    local os="${platform%%/*}"
    local arch="${platform##*/}"
    echo -e "${GREEN}✓${NC} Detected: ${os}/${arch}"
    echo ""

    # Select mirror
    echo -e "  ${DIM}·${NC} Selecting mirror..."
    local mirror
    mirror=$(select_mirror)
    local mirror_label="International"
    if [[ "${mirror}" == *"-cn"* ]]; then
        mirror_label="China"
    fi
    echo -e "  ${GREEN}✓${NC} Using ${mirror_label} mirror"

    # Get latest version
    local version
    version=$(get_latest_version "${mirror}")

    # Show install plan
    echo ""
    echo -e "${BOLD}Install plan${NC}"
    echo -e "  OS:         ${os}"
    echo -e "  Arch:       ${arch}"
    echo -e "  Version:    ${version}"
    local install_dir="${OPENDB_INSTALL_DIR:-/usr/local/bin}"
    echo -e "  Install to: ${install_dir}/opendb"
    echo ""

    # Download
    echo -e "${BOLD}[1/3] Downloading${NC}"
    download_and_install "${mirror}" "${version}" "${platform}"
    echo ""

    # Verify
    echo -e "${BOLD}[2/3] Verifying${NC}"
    verify_install
    echo ""

    # Launch setup — clear screen so logo displays from top
    echo -e "${BOLD}[3/3] Setup${NC}"
    echo -e "  ${GREEN}${BOLD}Starting setup wizard...${NC}"
    echo -e "  ${DIM}Run 'opendb setup' anytime to reconfigure.${NC}"
    sleep 1
    clear

    "${install_dir}/opendb" --setup
}

trap 'echo ""; exit 0' INT
main "$@"
