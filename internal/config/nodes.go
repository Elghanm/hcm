package config

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Node is one row of the columnar inventory file. boot_mac is the primary key:
// DHCP matches on it to hand each node its iPXE script and address.
type Node struct {
	Hostname string
	BootMAC  string
	BootIf   string
	BMCIP    string
	Group    string // maps to a partition's node_group
	Status   string // active | pending
}

// nodeColumns is the canonical header order for the file.
var nodeColumns = []string{"hostname", "boot_mac", "boot_if", "bmc_ip", "group", "status"}

// LoadNodes parses a columnar nodes file. Blank lines and lines starting with
// '#' are ignored. The first non-comment line may be a header (detected by the
// literal token "hostname" in column 1) and is skipped.
func LoadNodes(path string) ([]Node, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // an empty inventory is valid
		}
		return nil, err
	}
	defer f.Close()

	var nodes []Node
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		fields := strings.Fields(raw)
		if fields[0] == "hostname" {
			continue // header row
		}
		if len(fields) < 5 {
			return nil, fmt.Errorf("%s:%d: expected at least 5 columns, got %d", path, line, len(fields))
		}
		n := Node{
			Hostname: fields[0],
			BootMAC:  strings.ToLower(fields[1]),
			BootIf:   fields[2],
			BMCIP:    fields[3],
			Group:    fields[4],
			Status:   "active",
		}
		if len(fields) >= 6 {
			n.Status = fields[5]
		}
		nodes = append(nodes, n)
	}
	return nodes, sc.Err()
}

// WriteNodes writes nodes back in canonical columnar form with a header.
func WriteNodes(path string, nodes []Node) error {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Hostname < nodes[j].Hostname })
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", strings.Join(nodeColumns, "  "))
	for _, n := range nodes {
		fmt.Fprintf(&b, "%-12s %-18s %-8s %-14s %-10s %s\n",
			n.Hostname, n.BootMAC, n.BootIf, n.BMCIP, n.Group, n.Status)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// stagingPath returns the staging file path alongside the main nodes file.
func stagingPath(nodesPath string) string {
	return nodesPath + ".staging"
}

// AddNode appends a node directly to the active inventory (manual entry path).
func AddNode(nodesPath string, n Node) error {
	nodes, err := LoadNodes(nodesPath)
	if err != nil {
		return err
	}
	for _, e := range nodes {
		if e.Hostname == n.Hostname {
			return fmt.Errorf("node %q already exists", n.Hostname)
		}
	}
	n.Status = "active"
	return WriteNodes(nodesPath, append(nodes, n))
}

// StageNode appends a discovered node to the staging file as pending. This is
// the only file the discovery process ever writes, keeping the human-authored
// inventory pristine.
func StageNode(nodesPath string, n Node) error {
	sp := stagingPath(nodesPath)
	staged, err := LoadNodes(sp)
	if err != nil {
		return err
	}
	for _, e := range staged {
		if e.BootMAC == n.BootMAC {
			return nil // already staged, idempotent
		}
	}
	n.Status = "pending"
	return WriteNodes(sp, append(staged, n))
}

// PendingNodes returns nodes currently sitting in staging.
func PendingNodes(nodesPath string) ([]Node, error) {
	return LoadNodes(stagingPath(nodesPath))
}

// ApproveNode promotes one staged node into the active inventory and removes it
// from staging.
func ApproveNode(nodesPath, hostname string) error {
	sp := stagingPath(nodesPath)
	staged, err := LoadNodes(sp)
	if err != nil {
		return err
	}
	var promoted *Node
	var remain []Node
	for _, n := range staged {
		if n.Hostname == hostname {
			c := n
			promoted = &c
			continue
		}
		remain = append(remain, n)
	}
	if promoted == nil {
		return fmt.Errorf("no staged node named %q", hostname)
	}
	promoted.Status = "active"
	if err := AddNode(nodesPath, *promoted); err != nil {
		return err
	}
	return WriteNodes(sp, remain)
}
