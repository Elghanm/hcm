package reconcile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nidhal/hcm/internal/config"
	"github.com/nidhal/hcm/internal/store"
)

// ChangeKind classifies a planned change.
type ChangeKind string

const (
	Create ChangeKind = "create"
	Update ChangeKind = "update"
	NoOp   ChangeKind = "unchanged"
)

// Change is a single artifact's delta between desired and last-applied state.
type Change struct {
	Kind     ChangeKind
	Artifact Artifact
}

// Plan compares desired artifacts against what was last applied. It is
// read-only: this is exactly what `hcm check pending` reports.
func Plan(c *config.Cluster, nodes []config.Node, st *store.State, root string) []Change {
	desired := Render(c, nodes)
	var changes []Change
	for _, a := range desired {
		full := filepath.Join(root, a.Path)
		prev, existed := st.LastApplied(full)
		switch {
		case !existed:
			changes = append(changes, Change{Create, a})
		case prev != a.Content:
			changes = append(changes, Change{Update, a})
		default:
			changes = append(changes, Change{NoOp, a})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Artifact.Path < changes[j].Artifact.Path })
	return changes
}

// Apply writes changed artifacts under root, records them in state, and reloads
// affected services (unless dryRun). Unchanged artifacts are skipped, so apply
// is idempotent — a second run with no config change touches nothing.
func Apply(changes []Change, st *store.State, root string, reload bool) error {
	services := map[string]bool{}
	for _, ch := range changes {
		if ch.Kind == NoOp {
			continue
		}
		full := filepath.Join(root, ch.Artifact.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(ch.Artifact.Content), 0o644); err != nil {
			return err
		}
		st.Record(full, ch.Artifact.Content)
		if ch.Artifact.Service != "" {
			services[ch.Artifact.Service] = true
		}
	}
	if err := st.Save(); err != nil {
		return err
	}
	if reload {
		for svc := range services {
			// Best-effort; on a test VM without the daemon this simply warns.
			_ = exec.Command("systemctl", "reload-or-restart", svc).Run()
		}
	}
	return nil
}

// Summarize renders a human diff summary for `check pending`.
func Summarize(changes []Change) string {
	var b strings.Builder
	var create, update, noop int
	for _, ch := range changes {
		switch ch.Kind {
		case Create:
			create++
			fmt.Fprintf(&b, "  + %s\n", ch.Artifact.Path)
		case Update:
			update++
			fmt.Fprintf(&b, "  ~ %s\n", ch.Artifact.Path)
		}
	}
	for _, ch := range changes {
		if ch.Kind == NoOp {
			noop++
		}
	}
	if create+update == 0 {
		return "No pending changes. Cluster matches desired state.\n"
	}
	fmt.Fprintf(&b, "\nPending: %d to create, %d to update (%d unchanged).\n", create, update, noop)
	return b.String()
}

// ReloadServices lists the distinct services a set of changes would bounce.
func ReloadServices(changes []Change) []string {
	set := map[string]bool{}
	for _, ch := range changes {
		if ch.Kind != NoOp && ch.Artifact.Service != "" {
			set[ch.Artifact.Service] = true
		}
	}
	var out []string
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
