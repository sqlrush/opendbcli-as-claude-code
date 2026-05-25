/*-------------------------------------------------------------------------
 *
 * config.go
 *	  config.go manages the cluster configuration file
 *	  (~/.opendb/cluster/config.yaml).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/config.go
 *
 *-------------------------------------------------------------------------
 */
// config.go manages the cluster configuration file (~/.opendb/cluster/config.yaml).
package cluster

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ClusterConfig holds the persistent cluster configuration.
type ClusterConfig struct {
	Role       string `yaml:"role"`        // "worker", "memory", "manager"
	NodeID     string `yaml:"node_id"`     // Unique node identifier
	Listen     string `yaml:"listen"`      // gRPC listen address
	Region     string `yaml:"region"`      // Region name (for memory role)
	Cerebrate  string `yaml:"cerebrate"`   // Cerebrate address (for memory/worker)
	Overlord   string `yaml:"overlord"`    // Overlord address (for worker)
	WebListen  string `yaml:"web_listen"`  // Web UI listen address (for manager)
	JoinToken  string `yaml:"join_token"`  // Token used during join
	Initialized bool  `yaml:"initialized"` // Whether cluster init/join has been done
}

// clusterConfigPath returns the path to cluster config file.
func clusterConfigPath(baseDir string) string {
	return filepath.Join(baseDir, "cluster", "config.yaml")
}

// SaveClusterConfig writes the cluster config to disk.
func SaveClusterConfig(baseDir string, cfg ClusterConfig) error {
	dir := filepath.Join(baseDir, "cluster")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cluster dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(clusterConfigPath(baseDir), data, 0644)
}

// LoadClusterConfig reads the cluster config from disk.
func LoadClusterConfig(baseDir string) (ClusterConfig, error) {
	data, err := os.ReadFile(clusterConfigPath(baseDir))
	if err != nil {
		if os.IsNotExist(err) {
			return ClusterConfig{}, nil // not initialized
		}
		return ClusterConfig{}, fmt.Errorf("read config: %w", err)
	}

	var cfg ClusterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ClusterConfig{}, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// IsInitialized checks if cluster has been initialized.
func IsInitialized(baseDir string) bool {
	cfg, err := LoadClusterConfig(baseDir)
	if err != nil {
		return false
	}
	return cfg.Initialized
}
