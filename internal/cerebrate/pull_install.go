/*-------------------------------------------------------------------------
 *
 * pull_install.go
 *	  pull_install.go implements the Pull-based installation server.
 *	  Cerebrate serves the OpenDB binary and an install script via HTTP.
 *	  Nodes can bootstrap with: curl http://cerebrate:8080/install | sh
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/pull_install.go
 *
 *-------------------------------------------------------------------------
 */
// pull_install.go implements the Pull-based installation server.
// Cerebrate serves the OpenDB binary and an install script via HTTP.
// Nodes can bootstrap with: curl http://cerebrate:8080/install | sh
package cerebrate

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// PullInstallServer serves the OpenDB binary and install script for Pull-based deployment.
type PullInstallServer struct {
	binaryPath string // path to the opendb binary to serve
	listenAddr string // Cerebrate listen address (for install script)
	joinToken  string // current join token
}

// NewPullInstallServer creates a Pull install server.
func NewPullInstallServer(binaryPath, listenAddr, joinToken string) *PullInstallServer {
	return &PullInstallServer{
		binaryPath: binaryPath,
		listenAddr: listenAddr,
		joinToken:  joinToken,
	}
}

// RegisterRoutes adds Pull install routes to an existing HTTP mux.
func (ps *PullInstallServer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/install", ps.handleInstallScript)
	mux.HandleFunc("/binary", ps.handleBinary)
	mux.HandleFunc("/install/worker", ps.handleWorkerInstall)
	mux.HandleFunc("/install/memory", ps.handleMemoryInstall)
}

// handleInstallScript serves the bootstrap script.
func (ps *PullInstallServer) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	if role == "" {
		role = "worker"
	}
	overlord := r.URL.Query().Get("overlord")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, installScript, ps.listenAddr, role, overlord, ps.joinToken)
}

// handleBinary serves the opendb binary for download.
func (ps *PullInstallServer) handleBinary(w http.ResponseWriter, _ *http.Request) {
	// Determine binary path: explicit or self.
	binaryPath := ps.binaryPath
	if binaryPath == "" {
		exe, err := os.Executable()
		if err != nil {
			http.Error(w, "cannot determine binary path", http.StatusInternalServerError)
			return
		}
		binaryPath = exe
	}

	data, err := os.ReadFile(binaryPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("read binary: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(binaryPath)))
	w.Write(data)
}

func (ps *PullInstallServer) handleWorkerInstall(w http.ResponseWriter, r *http.Request) {
	r.URL.RawQuery = "role=worker&" + r.URL.RawQuery
	ps.handleInstallScript(w, r)
}

func (ps *PullInstallServer) handleMemoryInstall(w http.ResponseWriter, r *http.Request) {
	r.URL.RawQuery = "role=memory&" + r.URL.RawQuery
	ps.handleInstallScript(w, r)
}

// installScript is the bootstrap script template served to clients.
// Usage: curl http://cerebrate:8080/install?role=worker&overlord=10.0.2.1:9200 | sh
const installScript = `#!/bin/bash
set -e

CEREBRATE_ADDR="%s"
ROLE="%s"
OVERLORD="%s"
TOKEN="%s"

echo "OpenDB Autopilot — Pull Install"
echo "  Cerebrate: $CEREBRATE_ADDR"
echo "  Role:      $ROLE"
echo ""

# Download binary.
INSTALL_DIR="/usr/local/bin"
if [ ! -w "$INSTALL_DIR" ]; then
    INSTALL_DIR="$HOME/.local/bin"
    mkdir -p "$INSTALL_DIR"
fi

echo "Downloading opendb binary..."
curl -sL "http://${CEREBRATE_ADDR}/binary" -o "${INSTALL_DIR}/opendb"
chmod +x "${INSTALL_DIR}/opendb"
echo "Installed to ${INSTALL_DIR}/opendb"

# Join cluster.
if [ "$ROLE" = "worker" ] && [ -n "$OVERLORD" ]; then
    echo "Joining cluster as Worker..."
    "${INSTALL_DIR}/opendb" cluster join --role worker --overlord "$OVERLORD" --token "$TOKEN"
elif [ "$ROLE" = "memory" ]; then
    echo "Joining cluster as Overlord..."
    "${INSTALL_DIR}/opendb" cluster join --role memory --cerebrate "$CEREBRATE_ADDR" --token "$TOKEN" --region "${REGION:-default}"
else
    echo "Binary installed. Run 'opendb cluster join' to join the cluster."
fi

echo ""
echo "Done. Start with: opendb agent start --role $ROLE"
`
