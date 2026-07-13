package reconcile

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nidhal/hcm/internal/config"
)

// Artifact is one rendered file the reconcile loop wants to exist on disk.
type Artifact struct {
	Path    string // absolute-ish path under the output root
	Content string
	Service string // daemon to reload when this changes ("" = none)
}

// Render turns desired state (cluster + active nodes) into the full set of
// artifacts. These are PURE functions of the inputs — no I/O — which is what
// makes `check pending` a trustworthy diff and the whole loop idempotent.
func Render(c *config.Cluster, nodes []config.Node) []Artifact {
	var out []Artifact
	out = append(out, renderDnsmasq(c, nodes))
	out = append(out, renderChrony(c))
	if ex := renderExports(c); ex != nil {
		out = append(out, *ex)
	}
	out = append(out, renderIPXE(c, nodes)...)
	if sl := renderSlurmPartitions(c, nodes); sl != nil {
		out = append(out, *sl)
	}
	return out
}

func tftpRoot(c *config.Cluster) string {
	if c.Network.TFTPRoot != "" {
		return c.Network.TFTPRoot
	}
	return "/var/lib/tftpboot"
}

// activeInGroup returns active nodes belonging to a partition's node group.
func activeInGroup(nodes []config.Node, group string) []config.Node {
	var g []config.Node
	for _, n := range nodes {
		if n.Status == "active" && n.Group == group {
			g = append(g, n)
		}
	}
	sort.Slice(g, func(i, j int) bool { return g[i].Hostname < g[j].Hostname })
	return g
}

// partitionForNode finds the on-prem partition owning a node's group.
func partitionForNode(c *config.Cluster, n config.Node) *config.Partition {
	for i := range c.Partitions {
		if c.Partitions[i].Target == "onprem" && c.Partitions[i].NodeGroup == n.Group {
			return &c.Partitions[i]
		}
	}
	return nil
}

func renderDnsmasq(c *config.Cluster, nodes []config.Node) Artifact {
	var b strings.Builder
	b.WriteString("# Managed by hcm — do not edit by hand.\n")
	b.WriteString("# DHCP + TFTP + DNS for the provisioning network.\n\n")
	if c.ControlPlane.MgmtInterface != "" {
		fmt.Fprintf(&b, "interface=%s\n", c.ControlPlane.MgmtInterface)
	}
	if len(c.Network.DHCPRange) == 2 {
		fmt.Fprintf(&b, "dhcp-range=%s,%s,12h\n", c.Network.DHCPRange[0], c.Network.DHCPRange[1])
	}
	for _, fwd := range c.Network.DNSForwarders {
		fmt.Fprintf(&b, "server=%s\n", fwd)
	}
	b.WriteString("\n# PXE / iPXE chainload\n")
	b.WriteString("enable-tftp\n")
	fmt.Fprintf(&b, "tftp-root=%s\n", tftpRoot(c))
	// Classic PXE clients get iPXE; iPXE clients get their per-node script.
	b.WriteString("dhcp-match=set:ipxe,175\n")
	b.WriteString("dhcp-boot=tag:!ipxe,undionly.kpxe\n")
	b.WriteString("dhcp-boot=tag:ipxe,ipxe/boot.ipxe\n\n")

	b.WriteString("# Static reservations (one per known node, matched on MAC)\n")
	active := append([]config.Node(nil), nodes...)
	sort.Slice(active, func(i, j int) bool { return active[i].Hostname < active[j].Hostname })
	for _, n := range active {
		if n.Status != "active" {
			continue
		}
		// hostname resolves via DNS; per-node iPXE script selected by MAC tag.
		fmt.Fprintf(&b, "dhcp-host=%s,%s\n", n.BootMAC, n.Hostname)
	}
	return Artifact{
		Path:    "/etc/dnsmasq.d/hcm.conf",
		Content: b.String(),
		Service: "dnsmasq",
	}
}

func renderChrony(c *config.Cluster) Artifact {
	var b strings.Builder
	b.WriteString("# Managed by hcm — cluster time sync.\n")
	b.WriteString("# Time skew breaks Kerberos/FreeIPA and SLURM auth, so this is not optional.\n")
	b.WriteString("pool 2.pool.ntp.org iburst\n")
	if c.Network.ProvisionSubnet != "" {
		fmt.Fprintf(&b, "allow %s\n", c.Network.ProvisionSubnet)
	}
	b.WriteString("local stratum 10\n")
	return Artifact{Path: "/etc/chrony.d/hcm.conf", Content: b.String(), Service: "chronyd"}
}

func renderExports(c *config.Cluster) *Artifact {
	var lines []string
	for _, s := range c.Storage {
		if s.Type != "nfs" {
			continue
		}
		subnet := c.Network.ProvisionSubnet
		if subnet == "" {
			subnet = "*"
		}
		lines = append(lines, fmt.Sprintf("%s %s(rw,sync,no_subtree_check,no_root_squash)", s.Export, subnet))
	}
	if len(lines) == 0 {
		return nil
	}
	content := "# Managed by hcm — shared storage exports.\n" + strings.Join(lines, "\n") + "\n"
	return &Artifact{Path: "/etc/exports.d/hcm.exports", Content: content, Service: "nfs-server"}
}

// renderIPXE emits one boot script per active node. The script points the node
// at the OCI image assigned via its partition, then hands off to cloud-init
// (NoCloud) for identity + mounts — the same last mile a cloud node runs.
func renderIPXE(c *config.Cluster, nodes []config.Node) []Artifact {
	var arts []Artifact
	root := tftpRoot(c)
	for _, n := range nodes {
		if n.Status != "active" {
			continue
		}
		p := partitionForNode(c, n)
		image := "UNASSIGNED"
		if p != nil {
			image = p.Image
		}
		var b strings.Builder
		b.WriteString("#!ipxe\n")
		fmt.Fprintf(&b, "# node=%s group=%s image=%s\n", n.Hostname, n.Group, image)
		fmt.Fprintf(&b, "set hostname %s\n", n.Hostname)
		fmt.Fprintf(&b, "set image %s\n", image)
		b.WriteString("kernel http://${next-server}/images/${image}/vmlinuz ")
		b.WriteString("initrd=initrd.img hcm.image=${image} hcm.host=${hostname} ")
		b.WriteString("ds=nocloud-net;s=http://${next-server}/seed/${hostname}/\n")
		b.WriteString("initrd http://${next-server}/images/${image}/initrd.img\n")
		b.WriteString("boot\n")
		arts = append(arts, Artifact{
			Path:    filepath.Join(root, "ipxe", n.Hostname+".ipxe"),
			Content: b.String(),
		})
	}
	return arts
}

// renderSlurmPartitions previews the scheduler tie-in: each hcm partition
// becomes a SLURM partition, and GPU partitions declare Gres=gpu. This
// demonstrates how cpu-vs-gpu partitioning flows from the same model that
// drives provisioning — CUDA/OFED themselves live in the image payload roles,
// not here.
func renderSlurmPartitions(c *config.Cluster, nodes []config.Node) *Artifact {
	if c.Scheduler.Type != "slurm" {
		return nil
	}
	var b strings.Builder
	b.WriteString("# Managed by hcm — SLURM partitions derived from cluster.yaml.\n")
	for _, p := range c.Partitions {
		hosts := activeInGroup(nodes, p.NodeGroup)
		if len(hosts) == 0 {
			continue
		}
		names := make([]string, len(hosts))
		for i, h := range hosts {
			names[i] = h.Hostname
		}
		nodeList := strings.Join(names, ",")
		gres := ""
		if p.GPU {
			gres = " Gres=gpu"
		}
		fmt.Fprintf(&b, "NodeName=%s State=UNKNOWN%s\n", nodeList, gres)
		fmt.Fprintf(&b, "PartitionName=%s Nodes=%s Default=NO State=UP\n", p.Name, nodeList)
	}
	return &Artifact{Path: "/etc/slurm/partitions.conf", Content: b.String(), Service: "slurmctld"}
}
