package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"

	"github.com/spf13/cobra"
)

// Manifest states, as reported by SourceManifestStatus.State. They are constants
// because both the human report and the JSON document print them verbatim and
// the CLI goldens pin the text.
const (
	// ManifestFresh means the recorded watch set still matches the tree, so a
	// tool invocation execs its target directly with no rebake.
	ManifestFresh = "fresh"
	// ManifestStale means the next tool invocation re-bakes the farm first —
	// one visible hiccup, then back to steady state.
	ManifestStale = "stale"
	// ManifestMissing means the farm has never been baked for this root, which
	// is what a shim reports as exit 127 rather than baking implicitly.
	ManifestMissing = "missing"
	// ManifestUnreadable means the file is there but could not be decoded. It is
	// treated exactly like stale; it is reported separately only so the user can
	// tell a corrupted farm from one that has simply aged out.
	ManifestUnreadable = "unreadable"
)

// SourceStatus is the complete machine-readable description of a project's
// source-mode farm.
//
// This struct is the single serialization of a farm: any future JSON surface
// that has to describe what a name runs — an `exec --json`, an editor
// integration, a diagnostic bundle — reuses it rather than growing a second,
// drifting shape. It is deliberately typed rather than a map[string]any so the
// key set is a compile-time fact, and it deliberately carries nothing derived
// from runtimeconfig.Effective: what the farm contains is not a runtime-config
// question, and exposing that snapshot here would make it a hidden input.
//
// Every list is sorted by name by BuildPlan, so marshalling the same farm twice
// is byte-identical.
type SourceStatus struct {
	// Root is the authoritative git root the farm was built for.
	Root string `json:"root"`

	// FarmDir is the directory whose entries go on PATH.
	FarmDir string `json:"farmDir"`

	// Manifest describes the on-disk manifest and whether it still applies.
	Manifest SourceManifestStatus `json:"manifest"`

	// Entries is every declared name the farm makes available, installed or
	// not. An entry with Installed=false is a name whose target has not been
	// downloaded yet — or whose store path has since been deleted, which is the
	// same state and the same repair.
	Entries []sourcefarm.Entry `json:"entries"`

	// Excluded is every declared name that deliberately did not become an
	// entry, each with the reason. A name that simply vanished would be
	// undebuggable, which is why this is never omitted when empty.
	Excluded []sourcefarm.Excluded `json:"excluded"`

	// Shadowed is every farm name that also existed on the pre-activation PATH,
	// with the absolute path it was found at. Omitted when empty: shadowing is
	// the exception, not the normal state.
	Shadowed []sourcefarm.Shadow `json:"shadowed,omitempty"`
}

// SourceManifestStatus is the freshness half of SourceStatus.
type SourceManifestStatus struct {
	// Path is where the manifest lives, whether or not it is there.
	Path string `json:"path"`

	// Exists reports whether the file is present.
	Exists bool `json:"exists"`

	// Fresh reports whether the recorded watch set still matches the tree. It
	// is false whenever State is anything but "fresh".
	Fresh bool `json:"fresh"`

	// State is one of fresh, stale, missing, unreadable.
	State string `json:"state"`

	// Error carries the decode failure for the unreadable state and is empty
	// otherwise.
	Error string `json:"error,omitempty"`
}

var sourceStatusJSON bool

var sourceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show what source mode makes available in this project",
	Long: `Reports the farm for the current project: its root and directory, whether the
baked manifest still matches the tree, every tool the farm provides with how it
is materialized and whether it has been downloaded, every declared name that was
refused and why, and every system binary the farm shadows.

Names are never silently dropped. A declared tool that source mode refuses to put
on PATH — a shell app, a deny-listed name — is listed with its reason, and a
declared tool that is present but resolves to a system binary is listed with the
absolute path it shadows. Those two lists are the reason this command exists:
without them, a missing name looks identical to a name that was never declared.

This command resolves and reports; it never downloads and never re-bakes the
farm. Use ` + ldflags.PackageName + ` source refresh for that.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error { return runSourceStatus(cmd) },
}

func init() {
	sourceStatusCmd.Flags().BoolVar(&sourceStatusJSON, "json", false, "Emit the farm as a JSON document")
}

// runSourceStatus resolves the current project's farm plan and reports it.
//
// It deliberately does not materialize: status is a diagnostic, and a diagnostic
// that repairs what it is describing cannot be used to observe a broken farm.
func runSourceStatus(cmd *cobra.Command) error {
	plan, err := resolveSourcePlan(commandContext(cmd))
	if err != nil {
		return err
	}

	status := buildSourceStatus(plan)

	if sourceStatusJSON {
		// Warnings are still a human's business and still belong on stderr;
		// stdout in this mode is a document a program parses, so not one byte of
		// prose may reach it.
		warnSourceFarm(cmd.ErrOrStderr(), plan)
		return writeJSONIndent(cmd.OutOrStdout(), status)
	}

	return renderSourceStatus(cmd.OutOrStdout(), status)
}

// buildSourceStatus joins the freshly resolved plan with the on-disk manifest's
// freshness.
//
// The entries come from the plan rather than from the manifest on purpose: the
// manifest records what was true at bake time, and the question status answers is
// what is true now — including a store path that has been deleted out from under
// a farm that is otherwise perfectly fresh.
func buildSourceStatus(plan sourcefarm.Plan) SourceStatus {
	status := SourceStatus{
		Root:     plan.Root,
		FarmDir:  plan.FarmDir,
		Entries:  plan.Entries,
		Excluded: plan.Excluded,
		Shadowed: plan.Shadowed,
	}
	if status.Entries == nil {
		status.Entries = []sourcefarm.Entry{}
	}
	if status.Excluded == nil {
		status.Excluded = []sourcefarm.Excluded{}
	}

	manifestPath, err := env.GetProjectManifestPath(plan.Root)
	if err != nil {
		status.Manifest = SourceManifestStatus{State: ManifestMissing, Error: err.Error()}
		return status
	}
	status.Manifest = manifestStatus(manifestPath)
	return status
}

// manifestStatus reads one manifest and classifies it.
func manifestStatus(path string) SourceManifestStatus {
	s := SourceManifestStatus{Path: path}
	if _, err := os.Stat(path); err != nil {
		s.State = ManifestMissing
		return s
	}
	s.Exists = true

	m, err := sourcefarm.Load(path)
	if err != nil {
		s.State = ManifestUnreadable
		s.Error = err.Error()
		return s
	}

	if sourcefarm.Validate(m) {
		s.Fresh = true
		s.State = ManifestFresh
		return s
	}
	s.State = ManifestStale
	return s
}

// renderSourceStatus writes the human report.
//
// Column widths are derived from the content rather than hard-coded so the
// output stays aligned for any app name, and the derivation is deterministic —
// the lists are already sorted, so the same farm always renders byte-identically
// and the CLI goldens hold.
func renderSourceStatus(out io.Writer, s SourceStatus) error {
	var b strings.Builder

	fmt.Fprintf(&b, "root:     %s\n", s.Root)
	fmt.Fprintf(&b, "farm:     %s\n", s.FarmDir)
	fmt.Fprintf(&b, "manifest: %s (%s)\n", s.Manifest.Path, s.Manifest.State)
	if s.Manifest.Error != "" {
		fmt.Fprintf(&b, "          %s\n", s.Manifest.Error)
	}

	fmt.Fprintf(&b, "\nentries (%d):\n", len(s.Entries))
	if len(s.Entries) == 0 {
		b.WriteString("  none\n")
	}
	width := 0
	for _, e := range s.Entries {
		if len(e.Name) > width {
			width = len(e.Name)
		}
	}
	for _, e := range s.Entries {
		state := "not installed"
		if e.Installed {
			state = "installed"
		}
		fmt.Fprintf(&b, "  %-*s  %-7s  %s\n", width, e.Name, e.Strategy, state)
	}

	fmt.Fprintf(&b, "\nexcluded (%d):\n", len(s.Excluded))
	if len(s.Excluded) == 0 {
		b.WriteString("  none\n")
	}
	width = 0
	for _, x := range s.Excluded {
		if len(x.Name) > width {
			width = len(x.Name)
		}
	}
	for _, x := range s.Excluded {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, x.Name, x.Reason)
	}

	fmt.Fprintf(&b, "\nshadowed (%d):\n", len(s.Shadowed))
	if len(s.Shadowed) == 0 {
		b.WriteString("  none\n")
	}
	width = 0
	for _, sh := range s.Shadowed {
		if len(sh.Name) > width {
			width = len(sh.Name)
		}
	}
	for _, sh := range s.Shadowed {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, sh.Name, sh.Path)
	}

	if _, err := io.WriteString(out, b.String()); err != nil {
		return fmt.Errorf("write source status: %w", err)
	}
	return nil
}
