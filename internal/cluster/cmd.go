/*-------------------------------------------------------------------------
 *
 * cmd.go
 *	  cmd.go implements the cluster subcommand parsing and dispatch.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/cmd.go
 *
 *-------------------------------------------------------------------------
 */
// cmd.go implements the cluster subcommand parsing and dispatch.
package cluster

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/sqlrush/opendb/internal/config"
)

// ClusterSubcmd represents a cluster subcommand.
type ClusterSubcmd string

const (
	SubcmdInit   ClusterSubcmd = "init"
	SubcmdJoin   ClusterSubcmd = "join"
	SubcmdStatus ClusterSubcmd = "status"
)

// ClusterArgs holds parsed cluster command arguments.
type ClusterArgs struct {
	Subcmd    ClusterSubcmd
	Role      string // "worker", "memory", "manager"
	Listen    string // gRPC listen address
	Cerebrate string // Cerebrate address
	Overlord  string // Overlord address
	Region    string // Region name
	Token     string // Join token
	Web       string // Web UI listen address
}

// ParseClusterArgs parses "opendb cluster <subcmd> [flags]".
func ParseClusterArgs(args []string) (ClusterArgs, error) {
	if len(args) == 0 {
		return ClusterArgs{}, fmt.Errorf("usage: opendb cluster <init|join|status> [flags]")
	}

	subcmd := ClusterSubcmd(args[0])
	switch subcmd {
	case SubcmdInit, SubcmdJoin, SubcmdStatus:
	default:
		return ClusterArgs{}, fmt.Errorf("unknown cluster subcommand: %s (use init, join, or status)", args[0])
	}

	result := ClusterArgs{
		Subcmd: subcmd,
		Listen: "0.0.0.0:9100",
	}

	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--role" && i+1 < len(args):
			i++
			result.Role = args[i]
		case strings.HasPrefix(args[i], "--role="):
			result.Role = strings.TrimPrefix(args[i], "--role=")
		case args[i] == "--listen" && i+1 < len(args):
			i++
			result.Listen = args[i]
		case strings.HasPrefix(args[i], "--listen="):
			result.Listen = strings.TrimPrefix(args[i], "--listen=")
		case args[i] == "--cerebrate" && i+1 < len(args):
			i++
			result.Cerebrate = args[i]
		case strings.HasPrefix(args[i], "--cerebrate="):
			result.Cerebrate = strings.TrimPrefix(args[i], "--cerebrate=")
		case args[i] == "--overlord" && i+1 < len(args):
			i++
			result.Overlord = args[i]
		case strings.HasPrefix(args[i], "--overlord="):
			result.Overlord = strings.TrimPrefix(args[i], "--overlord=")
		case args[i] == "--region" && i+1 < len(args):
			i++
			result.Region = args[i]
		case strings.HasPrefix(args[i], "--region="):
			result.Region = strings.TrimPrefix(args[i], "--region=")
		case args[i] == "--token" && i+1 < len(args):
			i++
			result.Token = args[i]
		case strings.HasPrefix(args[i], "--token="):
			result.Token = strings.TrimPrefix(args[i], "--token=")
		case args[i] == "--web" && i+1 < len(args):
			i++
			result.Web = args[i]
		case strings.HasPrefix(args[i], "--web="):
			result.Web = strings.TrimPrefix(args[i], "--web=")
		}
	}

	return result, nil
}

// RunCluster dispatches cluster subcommands.
func RunCluster(args ClusterArgs) error {
	switch args.Subcmd {
	case SubcmdInit:
		return runInit(args)
	case SubcmdJoin:
		return runJoin(args)
	case SubcmdStatus:
		return runClusterStatus(args)
	default:
		return fmt.Errorf("unknown subcommand: %s", args.Subcmd)
	}
}

// runInit initializes a new cluster (Cerebrate).
func runInit(args ClusterArgs) error {
	if args.Role == "" {
		args.Role = "manager"
	}
	if args.Role != "manager" {
		return fmt.Errorf("cluster init requires --role manager")
	}

	baseDir := defaultBaseDir()

	// Check if already initialized.
	if IsInitialized(baseDir) {
		return fmt.Errorf("cluster already initialized. Use 'opendb cluster status' to check")
	}

	// Generate join token.
	token, err := GenerateToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	if err := SaveToken(baseDir, token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	// Generate node ID.
	nodeID := generateNodeID("cerebrate")

	// Save cluster config.
	cfg := ClusterConfig{
		Role:        args.Role,
		NodeID:      nodeID,
		Listen:      args.Listen,
		WebListen:   args.Web,
		Initialized: true,
	}
	if err := SaveClusterConfig(baseDir, cfg); err != nil {
		return fmt.Errorf("save cluster config: %w", err)
	}

	fmt.Println("Cerebrate initialized successfully.")
	fmt.Printf("  Node ID:    %s\n", nodeID)
	fmt.Printf("  Listen:     %s\n", args.Listen)
	if args.Web != "" {
		fmt.Printf("  Web UI:     %s\n", args.Web)
	}
	fmt.Printf("  Join token: %s (expires in 24h)\n", token.Value)
	fmt.Println()
	fmt.Println("Add Overlord:")
	fmt.Printf("  opendb cluster join --role memory --cerebrate <this_addr>:%s --token %s --region <region>\n", extractPort(args.Listen), token.Value)
	fmt.Println()
	fmt.Println("Add Worker:")
	fmt.Printf("  opendb cluster join --role worker --overlord <overlord_addr> --token %s\n", token.Value)

	return nil
}

// runJoin joins an existing cluster as Overlord or Worker.
func runJoin(args ClusterArgs) error {
	if args.Role == "" {
		return fmt.Errorf("--role is required (worker or memory)")
	}
	if args.Token == "" {
		return fmt.Errorf("--token is required")
	}

	baseDir := defaultBaseDir()

	// Check if already initialized.
	if IsInitialized(baseDir) {
		return fmt.Errorf("this node already joined a cluster. Use 'opendb cluster status' to check")
	}

	switch args.Role {
	case "memory":
		return joinAsOverlord(args, baseDir)
	case "worker":
		return joinAsWorker(args, baseDir)
	default:
		return fmt.Errorf("unknown role %q (use 'memory' or 'worker')", args.Role)
	}
}

func joinAsOverlord(args ClusterArgs, baseDir string) error {
	if args.Cerebrate == "" {
		return fmt.Errorf("--cerebrate is required for memory role")
	}
	if args.Region == "" {
		return fmt.Errorf("--region is required for memory role")
	}

	// Fix ISSUE-002: Overlords default to port 9200 to avoid conflict with Cerebrate (9100).
	if args.Listen == "0.0.0.0:9100" {
		args.Listen = "0.0.0.0:9200"
	}

	nodeID := generateNodeID("overlord")

	cfg := ClusterConfig{
		Role:        args.Role,
		NodeID:      nodeID,
		Listen:      args.Listen,
		Region:      args.Region,
		Cerebrate:   args.Cerebrate,
		JoinToken:   args.Token,
		Initialized: true,
	}
	if err := SaveClusterConfig(baseDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println("Joined cluster as Memory Agent (Overlord).")
	fmt.Printf("  Node ID:   %s\n", nodeID)
	fmt.Printf("  Region:    %s\n", args.Region)
	fmt.Printf("  Cerebrate: %s\n", args.Cerebrate)
	fmt.Printf("  Listen:    %s\n", args.Listen)
	fmt.Println()
	fmt.Println("Start the agent:")
	fmt.Printf("  opendb agent start --role memory\n")

	return nil
}

func joinAsWorker(args ClusterArgs, baseDir string) error {
	if args.Overlord == "" {
		return fmt.Errorf("--overlord is required for worker role")
	}

	// Fix ISSUE-002: Workers default to port 9300 to avoid conflict with Cerebrate (9100) and Overlord (9200).
	if args.Listen == "0.0.0.0:9100" {
		args.Listen = "0.0.0.0:9300"
	}

	nodeID := generateNodeID("drone")

	cfg := ClusterConfig{
		Role:        args.Role,
		NodeID:      nodeID,
		Listen:      args.Listen,
		Overlord:    args.Overlord,
		JoinToken:   args.Token,
		Initialized: true,
	}
	if err := SaveClusterConfig(baseDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println("Joined cluster as Worker Agent (Drone).")
	fmt.Printf("  Node ID:  %s\n", nodeID)
	fmt.Printf("  Overlord: %s\n", args.Overlord)
	fmt.Printf("  Listen:   %s\n", args.Listen)
	fmt.Println()
	fmt.Println("Start the agent:")
	fmt.Printf("  opendb agent start --role worker\n")

	return nil
}

func runClusterStatus(_ ClusterArgs) error {
	baseDir := defaultBaseDir()
	cfg, err := LoadClusterConfig(baseDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if !cfg.Initialized {
		fmt.Println("Cluster: not initialized")
		fmt.Println("Run 'opendb cluster init --role manager' to initialize.")
		return nil
	}

	fmt.Printf("Cluster: initialized\n")
	fmt.Printf("  Role:    %s\n", cfg.Role)
	fmt.Printf("  Node ID: %s\n", cfg.NodeID)
	fmt.Printf("  Listen:  %s\n", cfg.Listen)

	switch cfg.Role {
	case "manager":
		if cfg.WebListen != "" {
			fmt.Printf("  Web UI:  %s\n", cfg.WebListen)
		}
		token, err := LoadToken(baseDir)
		if err == nil && token.IsValid() {
			fmt.Printf("  Token:   %s (expires %s)\n", token.Value, token.ExpiresAt.Format("2006-01-02 15:04"))
		}
	case "memory":
		fmt.Printf("  Region:    %s\n", cfg.Region)
		fmt.Printf("  Cerebrate: %s\n", cfg.Cerebrate)
	case "worker":
		fmt.Printf("  Overlord: %s\n", cfg.Overlord)
	}

	return nil
}

// generateNodeID creates a unique node ID with the given prefix.
func generateNodeID(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	hostname, _ := os.Hostname()
	short := hostname
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s-%s-%s", prefix, short, hex.EncodeToString(b))
}

// extractPort extracts port from "host:port" address.
func extractPort(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i+1:]
		}
	}
	return addr
}

// defaultBaseDir returns the active brand's base directory by delegating
// to the single brand-aware resolver. v1.1.20 (was hardcoded ~/.opendb,
// causing dbaa cluster mode to write into opendb's directory).
func defaultBaseDir() string {
	return config.DefaultOpenDBDir()
}
