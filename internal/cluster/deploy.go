/*-------------------------------------------------------------------------
 *
 * deploy.go
 *	  deploy.go implements built-in SSH-based batch deployment. Usage:
 *	  opendb cluster deploy --inventory inventory.yaml --binary ./opendb
 *	  Distributes binary + config to all nodes, starts agents in order.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/deploy.go
 *
 *-------------------------------------------------------------------------
 */
// deploy.go implements built-in SSH-based batch deployment.
// Usage: opendb cluster deploy --inventory inventory.yaml --binary ./opendb
// Distributes binary + config to all nodes, starts agents in order.
package cluster

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sqlrush/opendb/internal/odberr"
)

// Inventory defines the cluster nodes for batch deployment.
type Inventory struct {
	Cerebrate InventoryNode   `yaml:"cerebrate"`
	Overlords []InventoryNode `yaml:"overlords"`
	Drones    []InventoryNode `yaml:"drones"`
	SSHUser   string          `yaml:"ssh_user"`
	SSHKey    string          `yaml:"ssh_key"`
}

// InventoryNode defines a single node in the inventory.
type InventoryNode struct {
	Host     string `yaml:"host"`
	Name     string `yaml:"name"`
	Region   string `yaml:"region,omitempty"`
	Overlord string `yaml:"overlord,omitempty"` // for drones: which overlord to join
	DBType   string `yaml:"db_type,omitempty"`
	DBConn   string `yaml:"db_conn,omitempty"`
	Listen   string `yaml:"listen,omitempty"`
	Web      string `yaml:"web,omitempty"`
}

// DeployConfig holds deployment parameters.
type DeployConfig struct {
	InventoryPath string
	BinaryPath    string
	BatchSize     int           // concurrent SSH sessions per batch (default 50)
	BatchDelay    time.Duration // delay between batches (default 10s)
	RemotePath    string        // where to install on remote (default /usr/local/bin/opendb)
}

// DeployResult holds the result of a deployment.
type DeployResult struct {
	Node    string `json:"node"`
	Host    string `json:"host"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// RunDeploy executes the batch deployment.
func RunDeploy(ctx context.Context, cfg DeployConfig) ([]DeployResult, error) {
	// Load inventory.
	inv, err := loadInventory(cfg.InventoryPath)
	if err != nil {
		return nil, fmt.Errorf("load inventory: %w", err)
	}

	// Validate binary exists.
	if _, err := os.Stat(cfg.BinaryPath); err != nil {
		return nil, fmt.Errorf("binary not found: %w", err)
	}

	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.BatchDelay <= 0 {
		cfg.BatchDelay = 10 * time.Second
	}
	if cfg.RemotePath == "" {
		cfg.RemotePath = "/usr/local/bin/opendb"
	}

	var allResults []DeployResult
	sshOpts := buildSSHOpts(inv.SSHUser, inv.SSHKey)

	// Phase 1: Deploy Cerebrate.
	fmt.Println("Phase 1: Deploying Cerebrate...")
	result := deployNode(ctx, inv.Cerebrate, cfg, sshOpts, "manager", "")
	allResults = append(allResults, result)
	if !result.Success {
		return allResults, fmt.Errorf("cerebrate deployment failed: %s", result.Message)
	}

	// Phase 2: Deploy Overlords (parallel).
	fmt.Printf("Phase 2: Deploying %d Overlords...\n", len(inv.Overlords))
	overlordResults := deployBatch(ctx, inv.Overlords, cfg, sshOpts, "memory", inv.Cerebrate.Host)
	allResults = append(allResults, overlordResults...)

	// Phase 3: Deploy Drones (batched).
	fmt.Printf("Phase 3: Deploying %d Workers (batch=%d)...\n", len(inv.Drones), cfg.BatchSize)
	for i := 0; i < len(inv.Drones); i += cfg.BatchSize {
		end := i + cfg.BatchSize
		if end > len(inv.Drones) {
			end = len(inv.Drones)
		}
		batch := inv.Drones[i:end]
		fmt.Printf("  Batch %d-%d...\n", i+1, end)
		batchResults := deployBatch(ctx, batch, cfg, sshOpts, "worker", "")
		allResults = append(allResults, batchResults...)

		if i+cfg.BatchSize < len(inv.Drones) {
			time.Sleep(cfg.BatchDelay)
		}
	}

	// Summary.
	success, fail := 0, 0
	for _, r := range allResults {
		if r.Success {
			success++
		} else {
			fail++
		}
	}
	fmt.Printf("\nDeployment complete: %d success, %d failed (total %d)\n", success, fail, len(allResults))

	return allResults, nil
}

// deployBatch deploys to multiple nodes in parallel.
func deployBatch(ctx context.Context, nodes []InventoryNode, cfg DeployConfig, sshOpts []string, role, cerebrateHost string) []DeployResult {
	results := make([]DeployResult, len(nodes))
	var wg sync.WaitGroup

	for i, node := range nodes {
		wg.Add(1)
		idx, n := i, node
		odberr.SafeGo(odberr.ErrClusterRPC, func() {
			defer wg.Done()
			overlord := n.Overlord
			if overlord == "" && cerebrateHost != "" {
				overlord = cerebrateHost
			}
			results[idx] = deployNode(ctx, n, cfg, sshOpts, role, overlord)
		})
	}

	wg.Wait()
	return results
}

// deployNode deploys to a single node via SSH.
func deployNode(_ context.Context, node InventoryNode, cfg DeployConfig, baseSshOpts []string, role, upstream string) DeployResult {
	host := node.Host
	name := node.Name
	if name == "" {
		name = host
	}

	// Step 1: Copy binary via SCP.
	scpArgs := append(append([]string{}, baseSshOpts...), cfg.BinaryPath, fmt.Sprintf("%s:%s", host, cfg.RemotePath))
	if out, err := exec.Command("scp", scpArgs...).CombinedOutput(); err != nil {
		return DeployResult{Node: name, Host: host, Success: false, Message: fmt.Sprintf("scp failed: %s %v", string(out), err)}
	}

	// Step 2: chmod +x.
	sshArgs := append(append([]string{}, baseSshOpts...), host, fmt.Sprintf("chmod +x %s", cfg.RemotePath))
	if out, err := exec.Command("ssh", sshArgs...).CombinedOutput(); err != nil {
		return DeployResult{Node: name, Host: host, Success: false, Message: fmt.Sprintf("chmod failed: %s %v", string(out), err)}
	}

	// Step 3: Join cluster (if upstream is provided).
	if upstream != "" && role != "manager" {
		joinCmd := fmt.Sprintf("%s cluster join --role %s", cfg.RemotePath, role)
		if role == "worker" {
			joinCmd += fmt.Sprintf(" --overlord %s", upstream)
		} else if role == "memory" {
			joinCmd += fmt.Sprintf(" --cerebrate %s --region %s", upstream, node.Region)
		}
		joinCmd += " --token auto"

		joinArgs := append(append([]string{}, baseSshOpts...), host, joinCmd)
		if out, err := exec.Command("ssh", joinArgs...).CombinedOutput(); err != nil {
			return DeployResult{Node: name, Host: host, Success: false, Message: fmt.Sprintf("join failed: %s %v", string(out), err)}
		}
	}

	return DeployResult{Node: name, Host: host, Success: true, Message: "deployed"}
}

func loadInventory(path string) (*Inventory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var inv Inventory
	if err := yaml.Unmarshal(data, &inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

func buildSSHOpts(user, keyPath string) []string {
	opts := []string{"-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10"}
	if keyPath != "" {
		opts = append(opts, "-i", keyPath)
	}
	if user != "" {
		opts = append(opts, "-l", user)
	}
	return opts
}

// FormatDeployResults returns a human-readable deployment report.
func FormatDeployResults(results []DeployResult) string {
	var sb strings.Builder
	for _, r := range results {
		status := "✓"
		if !r.Success {
			status = "✗"
		}
		sb.WriteString(fmt.Sprintf("  %s %-20s %-15s %s\n", status, r.Node, r.Host, r.Message))
	}
	return sb.String()
}
