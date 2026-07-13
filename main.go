package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/nidhal/hcm/internal/config"
	"github.com/nidhal/hcm/internal/reconcile"
	"github.com/nidhal/hcm/internal/store"
)

const usage = `hcm — hybrid cluster manager (v0 scaffold)

Usage:
  hcm init [--dir .]                 scaffold an example cluster.yaml + nodes.conf
  hcm validate                       run pre-flight checks (parity, refs, cloud)
  hcm check pending                  diff desired state vs last applied
  hcm apply [--reload]               converge: write configs (+reload daemons)
  hcm node list                      show active + pending nodes
  hcm node add --hostname ...        add a node directly (manual entry)
  hcm node discover --hostname ...   simulate dynamic discovery -> staging
  hcm node approve <hostname>        promote a staged node into the inventory
  hcm serve                          run the control plane (not yet implemented)
  hcm node add-ha <host>             add an HA control-plane peer (planned)

Common flags:
  -c, --config   path to cluster.yaml (default ./cluster.yaml)
      --root     output root for generated configs (default ./hcm-out)
      --state    state file (default ./.hcm/state.json)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}
	// Support "check pending" as a two-word subcommand.
	args := os.Args[1:]
	cmd := args[0]
	rest := args[1:]
	if cmd == "check" && len(rest) > 0 && rest[0] == "pending" {
		cmd, rest = "check-pending", rest[1:]
	}
	if cmd == "node" && len(rest) > 0 {
		nodeDispatch(rest)
		return
	}

	var err error
	switch cmd {
	case "init":
		err = cmdInit(rest)
	case "validate":
		err = cmdValidate(rest)
	case "check-pending":
		err = cmdPlan(rest)
	case "apply":
		err = cmdApply(rest)
	case "serve":
		fmt.Println("hcm serve: the control-plane daemon (API + reconcile loop) lands after the CLI core is proven. Stub for now.")
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Printf("unknown command %q\n\n%s", cmd, usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// commonFlags wires the shared -c/--root/--state flags onto a flag set.
func commonFlags(fs *flag.FlagSet) (*string, *string, *string) {
	cfg := fs.String("config", "cluster.yaml", "path to cluster.yaml")
	fs.StringVar(cfg, "c", "cluster.yaml", "path to cluster.yaml (shorthand)")
	root := fs.String("root", "hcm-out", "output root for generated configs")
	state := fs.String("state", ".hcm/state.json", "state file path")
	return cfg, root, state
}

func loadAll(cfgPath string) (*config.Cluster, []config.Node, error) {
	c, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	nodes, err := config.LoadNodes(c.NodesPath())
	if err != nil {
		return nil, nil, err
	}
	return c, nodes, nil
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	cfg, _, _ := commonFlags(fs)
	fs.Parse(args)
	c, _, err := loadAll(*cfg)
	if err != nil {
		return err
	}
	errs := c.Validate()
	if len(errs) == 0 {
		fmt.Println("✓ config valid")
		return nil
	}
	for _, e := range errs {
		fmt.Printf("✗ %s\n", e)
	}
	return fmt.Errorf("%d validation error(s)", len(errs))
}

func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("check pending", flag.ExitOnError)
	cfg, root, statePath := commonFlags(fs)
	fs.Parse(args)
	c, nodes, err := loadAll(*cfg)
	if err != nil {
		return err
	}
	if errs := c.Validate(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Printf("✗ %s\n", e)
		}
		return fmt.Errorf("fix validation errors before planning")
	}
	st, err := store.Open(*statePath)
	if err != nil {
		return err
	}
	changes := reconcile.Plan(c, nodes, st, *root)
	fmt.Print(reconcile.Summarize(changes))
	if svcs := reconcile.ReloadServices(changes); len(svcs) > 0 {
		fmt.Printf("Would reload: %s\n", strings.Join(svcs, ", "))
	}
	return nil
}

func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	cfg, root, statePath := commonFlags(fs)
	reload := fs.Bool("reload", false, "reload affected daemons after writing")
	fs.Parse(args)
	c, nodes, err := loadAll(*cfg)
	if err != nil {
		return err
	}
	if errs := c.Validate(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Printf("✗ %s\n", e)
		}
		return fmt.Errorf("refusing to apply an invalid config")
	}
	st, err := store.Open(*statePath)
	if err != nil {
		return err
	}
	changes := reconcile.Plan(c, nodes, st, *root)
	fmt.Print(reconcile.Summarize(changes))
	if err := reconcile.Apply(changes, st, *root, *reload); err != nil {
		return err
	}
	fmt.Printf("Applied. Configs written under %s\n", *root)
	if !*reload {
		fmt.Println("(run with --reload to bounce daemons; omitted so a test VM stays quiet)")
	}
	return nil
}
