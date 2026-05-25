/*-------------------------------------------------------------------------
 *
 * validate.go
 *	  validate.go provides inventory validation and dry-run preview for
 *	  deployment.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/validate.go
 *
 *-------------------------------------------------------------------------
 */
// validate.go provides inventory validation and dry-run preview for deployment.
package cluster

import (
	"fmt"
	"strings"
)

// ValidateInventory checks an Inventory for configuration errors.
// Returns a slice of errors (empty if valid). Does not mutate the input.
func ValidateInventory(inv *Inventory) []error {
	if inv == nil {
		return []error{fmt.Errorf("inventory is nil")}
	}

	var errs []error
	errs = append(errs, validateCerebrate(inv.Cerebrate)...)
	errs = append(errs, validateOverlords(inv.Overlords)...)
	errs = append(errs, validateDrones(inv.Drones)...)
	errs = append(errs, validateNoDuplicateHosts(inv)...)
	errs = append(errs, validateSSH(inv)...)
	return errs
}

func validateCerebrate(node InventoryNode) []error {
	if strings.TrimSpace(node.Host) == "" {
		return []error{fmt.Errorf("cerebrate: host is required")}
	}
	return nil
}

func validateOverlords(overlords []InventoryNode) []error {
	var errs []error
	for i, o := range overlords {
		name := o.Name
		if name == "" {
			name = fmt.Sprintf("overlord[%d]", i)
		}
		if strings.TrimSpace(o.Host) == "" {
			errs = append(errs, fmt.Errorf("%s: host is required", name))
		}
		if strings.TrimSpace(o.Region) == "" {
			errs = append(errs, fmt.Errorf("%s: region is required", name))
		}
	}
	return errs
}

func validateDrones(drones []InventoryNode) []error {
	var errs []error
	for i, d := range drones {
		name := d.Name
		if name == "" {
			name = fmt.Sprintf("drone[%d]", i)
		}
		if strings.TrimSpace(d.Host) == "" {
			errs = append(errs, fmt.Errorf("%s: host is required", name))
		}
		if strings.TrimSpace(d.Overlord) == "" {
			errs = append(errs, fmt.Errorf("%s: overlord is required", name))
		}
	}
	return errs
}

func validateNoDuplicateHosts(inv *Inventory) []error {
	seen := make(map[string]string) // host -> first node name
	var errs []error

	record := func(host, name string) {
		if host == "" {
			return
		}
		if first, exists := seen[host]; exists {
			errs = append(errs, fmt.Errorf("duplicate host %q: used by %q and %q", host, first, name))
		} else {
			seen[host] = name
		}
	}

	cName := inv.Cerebrate.Name
	if cName == "" {
		cName = "cerebrate"
	}
	record(inv.Cerebrate.Host, cName)

	for i, o := range inv.Overlords {
		name := o.Name
		if name == "" {
			name = fmt.Sprintf("overlord[%d]", i)
		}
		record(o.Host, name)
	}

	for i, d := range inv.Drones {
		name := d.Name
		if name == "" {
			name = fmt.Sprintf("drone[%d]", i)
		}
		record(d.Host, name)
	}

	return errs
}

func validateSSH(inv *Inventory) []error {
	if strings.TrimSpace(inv.SSHUser) == "" {
		return []error{fmt.Errorf("ssh_user is required")}
	}
	return nil
}

// DryRun returns a formatted deployment plan without executing any SSH commands.
// Does not mutate the input.
func DryRun(inv *Inventory) string {
	if inv == nil {
		return "Error: inventory is nil\n"
	}

	var sb strings.Builder
	sb.WriteString("=== Deployment Plan (Dry Run) ===\n\n")

	sb.WriteString(fmt.Sprintf("SSH User: %s\n", inv.SSHUser))
	if inv.SSHKey != "" {
		sb.WriteString(fmt.Sprintf("SSH Key:  %s\n", inv.SSHKey))
	}
	sb.WriteString("\n")

	sb.WriteString("Phase 1: Cerebrate\n")
	sb.WriteString(formatPlanNode(inv.Cerebrate, "manager"))
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("Phase 2: Overlords (%d nodes)\n", len(inv.Overlords)))
	for _, o := range inv.Overlords {
		sb.WriteString(formatPlanNode(o, "memory"))
	}
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("Phase 3: Drones (%d nodes)\n", len(inv.Drones)))
	for _, d := range inv.Drones {
		sb.WriteString(formatPlanNode(d, "worker"))
	}
	sb.WriteString("\n")

	total := 1 + len(inv.Overlords) + len(inv.Drones)
	sb.WriteString(fmt.Sprintf("Total: %d nodes\n", total))

	return sb.String()
}

func formatPlanNode(node InventoryNode, role string) string {
	name := node.Name
	if name == "" {
		name = node.Host
	}
	line := fmt.Sprintf("  %-20s %-15s role=%-7s", name, node.Host, role)
	if node.Region != "" {
		line += fmt.Sprintf(" region=%s", node.Region)
	}
	if node.Overlord != "" {
		line += fmt.Sprintf(" overlord=%s", node.Overlord)
	}
	if node.DBType != "" {
		line += fmt.Sprintf(" db=%s", node.DBType)
	}
	return line + "\n"
}
