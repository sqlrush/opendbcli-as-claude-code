/*-------------------------------------------------------------------------
 *
 * os_discovery.go
 *	  os_discovery.go implements OS-level topology discovery for Worker
 *	  Agent. Collects storage, network, and hardware info from the OS to
 *	  help Overlord build regional topology (shared storage groups,
 *	  network groups).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/os_discovery.go
 *
 *-------------------------------------------------------------------------
 */
// os_discovery.go implements OS-level topology discovery for Worker Agent.
// Collects storage, network, and hardware info from the OS to help Overlord
// build regional topology (shared storage groups, network groups).
package drone

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
)

// OSTopology holds discovered OS-level topology information.
type OSTopology struct {
	Hostname    string            `json:"hostname"`
	IPAddresses []string          `json:"ip_addresses"`
	Subnets     []string          `json:"subnets"`
	MountPoints []MountInfo       `json:"mount_points"`
	BlockDevices []BlockDeviceInfo `json:"block_devices"`
	StorageIDs  []string          `json:"storage_ids"`  // WWN, serial numbers
}

// MountInfo describes a filesystem mount.
type MountInfo struct {
	Device     string `json:"device"`
	MountPoint string `json:"mount_point"`
	FSType     string `json:"fs_type"`
	Source     string `json:"source"` // NFS server, SAN device, etc.
}

// BlockDeviceInfo describes a block device.
type BlockDeviceInfo struct {
	Name   string `json:"name"`
	Size   string `json:"size"`
	Type   string `json:"type"` // disk, part, lvm
	Serial string `json:"serial"`
}

// DiscoverOSTopology collects OS-level topology information.
func DiscoverOSTopology() OSTopology {
	topo := OSTopology{}

	topo.Hostname, _ = os.Hostname()
	topo.IPAddresses, topo.Subnets = discoverNetwork()
	topo.MountPoints = discoverMounts()
	topo.BlockDevices = discoverBlockDevices()
	topo.StorageIDs = extractStorageIDs(topo.BlockDevices)

	return topo
}

// discoverNetwork collects IP addresses and subnets.
func discoverNetwork() ([]string, []string) {
	var ips, subnets []string

	ifaces, err := net.Interfaces()
	if err != nil {
		return ips, subnets
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
				continue
			}
			if ipNet.IP.To4() != nil {
				ips = append(ips, ipNet.IP.String())
				// Calculate subnet.
				subnet := ipNet.IP.Mask(ipNet.Mask).String()
				ones, _ := ipNet.Mask.Size()
				subnets = append(subnets, fmt.Sprintf("%s/%d", subnet, ones))
			}
		}
	}

	return ips, subnets
}

// discoverMounts reads /etc/mtab or mount output for filesystem mounts.
func discoverMounts() []MountInfo {
	var mounts []MountInfo

	// Read /proc/mounts (Linux) or run mount command.
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		// Fallback to mount command (macOS/other Unix).
		out, err := exec.Command("mount").Output()
		if err != nil {
			return mounts
		}
		data = out
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		device := fields[0]
		mountPoint := fields[1]
		fsType := fields[2]

		// Skip virtual filesystems.
		if strings.HasPrefix(fsType, "sys") || strings.HasPrefix(fsType, "proc") ||
			fsType == "devtmpfs" || fsType == "tmpfs" || fsType == "cgroup" ||
			fsType == "devpts" || fsType == "securityfs" || fsType == "autofs" {
			continue
		}

		source := ""
		if strings.Contains(device, ":") {
			source = device // NFS: server:/path
		}

		mounts = append(mounts, MountInfo{
			Device:     device,
			MountPoint: mountPoint,
			FSType:     fsType,
			Source:     source,
		})
	}

	return mounts
}

// discoverBlockDevices runs lsblk to discover block devices.
func discoverBlockDevices() []BlockDeviceInfo {
	var devices []BlockDeviceInfo

	out, err := exec.Command("lsblk", "-ndo", "NAME,SIZE,TYPE,SERIAL").Output()
	if err != nil {
		return devices
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		dev := BlockDeviceInfo{
			Name: fields[0],
			Size: fields[1],
			Type: fields[2],
		}
		if len(fields) >= 4 {
			dev.Serial = fields[3]
		}
		devices = append(devices, dev)
	}

	return devices
}

// extractStorageIDs collects unique storage identifiers (serials, WWNs).
func extractStorageIDs(devices []BlockDeviceInfo) []string {
	seen := make(map[string]bool)
	var ids []string

	for _, d := range devices {
		if d.Serial != "" && !seen[d.Serial] {
			seen[d.Serial] = true
			ids = append(ids, d.Serial)
		}
	}

	// Also try multipath for SAN devices.
	out, err := exec.Command("multipath", "-ll").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			// Extract WWN from multipath output (first field is usually the mpath name/WWN).
			fields := strings.Fields(line)
			if len(fields) >= 1 && (strings.HasPrefix(fields[0], "3") || strings.HasPrefix(fields[0], "mpath")) {
				wwn := fields[0]
				if !seen[wwn] {
					seen[wwn] = true
					ids = append(ids, wwn)
				}
			}
		}
	}

	return ids
}

// FormatTopology returns a human-readable summary of OS topology.
func (t OSTopology) FormatTopology() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Hostname: %s\n", t.Hostname))
	sb.WriteString(fmt.Sprintf("IPs: %s\n", strings.Join(t.IPAddresses, ", ")))
	sb.WriteString(fmt.Sprintf("Subnets: %s\n", strings.Join(t.Subnets, ", ")))

	if len(t.MountPoints) > 0 {
		sb.WriteString(fmt.Sprintf("Mounts: %d filesystems\n", len(t.MountPoints)))
		for _, m := range t.MountPoints {
			sb.WriteString(fmt.Sprintf("  %s → %s (%s)\n", m.Device, m.MountPoint, m.FSType))
		}
	}

	if len(t.StorageIDs) > 0 {
		sb.WriteString(fmt.Sprintf("Storage IDs: %s\n", strings.Join(t.StorageIDs, ", ")))
	}

	return sb.String()
}
