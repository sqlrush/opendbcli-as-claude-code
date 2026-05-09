#!/bin/bash
set -euo pipefail

# Build release binaries for all platforms.
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}
echo "Building OpenDB ${VERSION}..."
make release VERSION="${VERSION}"
echo "Done. Binaries in bin/"
