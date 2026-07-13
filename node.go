package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/nidhal/hcm/internal/config"
)

func nodeDispatch(args []string) {
	sub := args[0]
	rest := args[1:]
	var err error
	switch sub {
	case "list":
		err = cmdNodeList(rest)
	case "add":
		err = cmdNodeAdd(rest, false)
	case "discover":
		err = cmdNodeAdd(rest, true)
	case "approve":
		err = cmdNodeApprove(rest)
	case "add-ha":
		fmt.Println("hcm node add-ha: registers a second control-plane peer for HA.")
		fmt.Println("Planned design: peers share the datastore (external Postgres backend)")
		fmt.Println("and sit behind a VIP; the reconcile loop runs active/standby. The")
		fmt.Println("single-binary + embedded-store default stays for non-HA installs.")
	default:
		fmt.Printf("unknown node subcommand %q\n", sub)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func nodeFlags(fs *flag.FlagSet) *string {
	cfg := fs.String("config", "cluster.yaml", "path to cluster.yaml")
	fs.StringVar(cfg, "c", "cluster.yaml", "path to cluster.yaml (shorthand)")
	return cfg
}

func cmdNodeList(args []string) error {
	fs := flag.NewFlagSet("node list", flag.ExitOnError)
	cfg := nodeFlags(fs)
	fs.Parse(args)
	c, err := config.Load(*cfg)
	if err != nil {
		return err
	}
	active, err := config.LoadNodes(c.NodesPath())
	if err != nil {
		return err
	}
	pending, err := config.PendingNodes(c.NodesPath())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "HOSTNAME\tMAC\tIF\tBMC\tGROUP\tSTATUS")
	for _, n := range active {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", n.Hostname, n.BootMAC, n.BootIf, n.BMCIP, n.Group, n.Status)
	}
	for _, n := range pending {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", n.Hostname, n.BootMAC, n.BootIf, n.BMCIP, n.Group, "pending")
	}
	w.Flush()
	if len(pending) > 0 {
		fmt.Printf("\n%d node(s) awaiting approval — run: hcm node approve <hostname>\n", len(pending))
	}
	return nil
}

func cmdNodeAdd(args []string, discover bool) error {
	name := "node add"
	if discover {
		name = "node discover"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	cfg := nodeFlags(fs)
	host := fs.String("hostname", "", "node hostname")
	mac := fs.String("mac", "", "boot NIC MAC address")
	iface := fs.String("if", "eno1", "boot interface name")
	bmc := fs.String("bmc", "", "iDRAC/BMC IP")
	group := fs.String("group", "", "node group (maps to a partition)")
	fs.Parse(args)
	if *host == "" || *mac == "" || *group == "" {
		return fmt.Errorf("--hostname, --mac and --group are required")
	}
	c, err := config.Load(*cfg)
	if err != nil {
		return err
	}
	n := config.Node{Hostname: *host, BootMAC: *mac, BootIf: *iface, BMCIP: *bmc, Group: *group}
	if discover {
		if err := config.StageNode(c.NodesPath(), n); err != nil {
			return err
		}
		fmt.Printf("Discovered %s -> staging (pending). Approve with: hcm node approve %s\n", *host, *host)
		return nil
	}
	if err := config.AddNode(c.NodesPath(), n); err != nil {
		return err
	}
	fmt.Printf("Added %s (active). Run: hcm check pending\n", *host)
	return nil
}

func cmdNodeApprove(args []string) error {
	fs := flag.NewFlagSet("node approve", flag.ExitOnError)
	cfg := nodeFlags(fs)
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: hcm node approve <hostname>")
	}
	c, err := config.Load(*cfg)
	if err != nil {
		return err
	}
	if err := config.ApproveNode(c.NodesPath(), rest[0]); err != nil {
		return err
	}
	fmt.Printf("Approved %s -> active inventory. Run: hcm check pending\n", rest[0])
	return nil
}
