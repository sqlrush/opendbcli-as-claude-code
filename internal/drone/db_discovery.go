/*-------------------------------------------------------------------------
 *
 * db_discovery.go
 *	  db_discovery.go implements database-level topology discovery.
 *	  Worker queries its own database for replication role, cluster
 *	  membership, etc.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/db_discovery.go
 *
 *-------------------------------------------------------------------------
 */
// db_discovery.go implements database-level topology discovery.
// Worker queries its own database for replication role, cluster membership, etc.
package drone

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
)

// DBTopology holds discovered database-level topology information.
type DBTopology struct {
	DBType       string   `json:"db_type"`
	Version      string   `json:"version"`
	InstanceName string   `json:"instance_name"`
	Role         string   `json:"role"`         // "primary", "standby", "read-replica", "standalone"
	ClusterName  string   `json:"cluster_name"` // RAC cluster name, replication group, etc.
	PeerNodes    []string `json:"peer_nodes"`   // other nodes in the cluster/replication group
}

// DiscoverDBTopology queries the database for its replication role and cluster info.
func DiscoverDBTopology(ctx context.Context, driver db.Driver, dbType string) DBTopology {
	topo := DBTopology{
		DBType: dbType,
		Role:   "standalone",
	}

	if driver == nil {
		return topo
	}

	// Get basic server info.
	info := driver.ServerInfo()
	topo.Version = info.Version
	topo.InstanceName = info.InstanceName

	// DB-specific role detection.
	switch dbType {
	case "oracle":
		topo = discoverOracleTopology(ctx, driver, topo)
	case "mysql":
		topo = discoverMySQLTopology(ctx, driver, topo)
	case "postgres", "opengauss", "gaussdb":
		topo = discoverPGTopology(ctx, driver, topo)
	}

	return topo
}

func discoverOracleTopology(ctx context.Context, driver db.Driver, topo DBTopology) DBTopology {
	// Query Data Guard role.
	result, err := driver.Query(ctx, "SELECT DATABASE_ROLE FROM V$DATABASE")
	if err == nil && len(result.Rows) > 0 && len(result.Rows[0]) > 0 {
		role := fmt.Sprintf("%v", result.Rows[0][0])
		switch role {
		case "PRIMARY":
			topo.Role = "primary"
		case "PHYSICAL STANDBY", "LOGICAL STANDBY", "SNAPSHOT STANDBY":
			topo.Role = "standby"
		}
	}

	// Check RAC.
	result, err = driver.Query(ctx, "SELECT INSTANCE_NAME FROM GV$INSTANCE ORDER BY INST_ID")
	if err == nil && len(result.Rows) > 1 {
		topo.ClusterName = "RAC"
		for _, row := range result.Rows {
			if len(row) > 0 {
				topo.PeerNodes = append(topo.PeerNodes, fmt.Sprintf("%v", row[0]))
			}
		}
	}

	return topo
}

func discoverMySQLTopology(ctx context.Context, driver db.Driver, topo DBTopology) DBTopology {
	// Check if slave.
	result, err := driver.Query(ctx, "SHOW REPLICA STATUS")
	if err == nil && len(result.Rows) > 0 {
		topo.Role = "standby"
	} else {
		// Check if has replicas.
		result, err = driver.Query(ctx, "SHOW REPLICAS")
		if err == nil && len(result.Rows) > 0 {
			topo.Role = "primary"
			for _, row := range result.Rows {
				if len(row) > 1 {
					topo.PeerNodes = append(topo.PeerNodes, fmt.Sprintf("%v:%v", row[1], row[2]))
				}
			}
		}
	}

	return topo
}

func discoverPGTopology(ctx context.Context, driver db.Driver, topo DBTopology) DBTopology {
	// Check recovery mode.
	result, err := driver.Query(ctx, "SELECT pg_is_in_recovery()")
	if err == nil && len(result.Rows) > 0 && len(result.Rows[0]) > 0 {
		inRecovery := fmt.Sprintf("%v", result.Rows[0][0])
		if inRecovery == "true" || inRecovery == "t" {
			topo.Role = "standby"
		} else {
			topo.Role = "primary"
		}
	}

	// Check replication slots.
	result, err = driver.Query(ctx, "SELECT slot_name, active FROM pg_replication_slots")
	if err == nil && len(result.Rows) > 0 {
		for _, row := range result.Rows {
			if len(row) > 0 {
				topo.PeerNodes = append(topo.PeerNodes, fmt.Sprintf("%v", row[0]))
			}
		}
	}

	return topo
}
