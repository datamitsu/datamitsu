package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

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
	// Origin is how the farm's identity was established: "git-root" for a farm
	// discovered from a repository, "explicit-config" for one named with
	// --config. It is never omitted — which of the two a farm is decides what
	// the shim does with it, so a reader must not have to infer it from the
	// presence of another field.
	Origin sourcefarm.Origin `json:"origin"`

	// Root is the authoritative git root the farm was built for, and is empty
	// for an explicit-config farm.
	Root string `json:"root"`

	// ConfigPaths is the resolved config chain an explicit-config farm was baked
	// from — its identity, and what the user would pass to --config again.
	// Omitted for a git-root farm, which has none.
	ConfigPaths []string `json:"configPaths,omitempty"`

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

	// Error carries the decode failure for the unreadable state, or the reason
	// an otherwise-fresh manifest was demoted to stale, and is empty otherwise.
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
	ctx := commandContext(cmd)
	target, err := resolveSourceTarget(ctx)
	if err != nil {
		return err
	}
	plan, err := resolveSourcePlanFor(ctx, target)
	if err != nil {
		return err
	}

	status := buildSourceStatus(plan, target)

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
func buildSourceStatus(plan sourcefarm.Plan, target sourceTarget) SourceStatus {
	status := SourceStatus{
		Origin:      target.Origin,
		Root:        plan.Root,
		ConfigPaths: target.ConfigPaths,
		FarmDir:     plan.FarmDir,
		Entries:     plan.Entries,
		Excluded:    plan.Excluded,
		Shadowed:    plan.Shadowed,
	}
	if status.Entries == nil {
		status.Entries = []sourcefarm.Entry{}
	}
	if status.Excluded == nil {
		status.Excluded = []sourcefarm.Excluded{}
	}

	if target.ManifestPath == "" {
		status.Manifest = SourceManifestStatus{State: ManifestMissing}
		return status
	}
	status.Manifest = manifestStatus(target.ManifestPath, target)
	return status
}

// manifestStatus reads one manifest and classifies it.
func manifestStatus(path string, target sourceTarget) SourceManifestStatus {
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

	// A manifest baked from a different config chain than this invocation
	// selected is stale *for this invocation* even when every watched file is
	// unchanged: it describes a farm built from apps this chain never declared.
	// Reporting it fresh would make status disagree with the entries printed
	// beside it, which come from the chain resolved now.
	// A manifest recording the other origin is stale for the same reason: the
	// farm it describes is not the one this invocation selected.
	if m.Origin == target.Origin && manifestChainMatches(m, target) && sourcefarm.Validate(m) {
		// Freshness watches the *tree*, never the farm, so a manifest whose farm
		// directory was deleted — or which is missing one entry — validates
		// clean. That is the one shadowing failure no shim can report: with no
		// farm entry for the name, nothing of datamitsu's runs and the shell
		// falls through PATH to the system binary, exit 0. Reporting it fresh
		// here would confirm the wrong conclusion in the command that exists to
		// diagnose exactly this. Activation already refuses such a manifest
		// (loadSourcePlan), and `source refresh` already re-bakes it, so stale is
		// the state the rest of source mode agrees on.
		if reason := farmDefect(m); reason != "" {
			s.State = ManifestStale
			s.Error = reason
			return s
		}
		s.Fresh = true
		s.State = ManifestFresh
		return s
	}
	s.State = ManifestStale
	return s
}

// farmDefect returns why m's farm cannot be served as-is, or "" when it can.
func farmDefect(m sourcefarm.Manifest) string {
	if !sourcefarm.FarmUsable(m.FarmDir, "") {
		return fmt.Sprintf("farm directory %s is missing or not safely owned", m.FarmDir)
	}
	if !farmEntriesPresent(m) {
		return fmt.Sprintf("farm directory %s is missing one or more entries", m.FarmDir)
	}
	return ""
}

// writeStatusSection writes one titled, count-annotated block of name/detail
// rows, left-padding the name column to the widest name in that block. An empty
// block still prints its header, so "no shadowed binaries" and "this section
// does not exist" cannot be confused.
func writeStatusSection(b *strings.Builder, title string, rows [][2]string) {
	fmt.Fprintf(b, "\n%s (%d):\n", title, len(rows))
	if len(rows) == 0 {
		b.WriteString("  none\n")
		return
	}
	width := 0
	for _, row := range rows {
		if len(row[0]) > width {
			width = len(row[0])
		}
	}
	for _, row := range rows {
		fmt.Fprintf(b, "  %-*s  %s\n", width, row[0], row[1])
	}
}

// renderSourceStatus writes the human report.
//
// Column widths are derived from the content rather than hard-coded so the
// output stays aligned for any app name, and the derivation is deterministic —
// the lists are already sorted, so the same farm always renders byte-identically
// and the CLI goldens hold.
func renderSourceStatus(out io.Writer, s SourceStatus) error {
	var b strings.Builder

	fmt.Fprintf(&b, "origin:   %s\n", s.Origin)
	// A farm has one identity or the other, never both, and the line printed is
	// the one that exists: an empty "root:" for a machine-level farm would read
	// as a repository that could not be determined.
	if s.Origin == sourcefarm.OriginExplicitConfig {
		for _, p := range s.ConfigPaths {
			fmt.Fprintf(&b, "config:   %s\n", p)
		}
	} else {
		fmt.Fprintf(&b, "root:     %s\n", s.Root)
	}
	fmt.Fprintf(&b, "farm:     %s\n", s.FarmDir)
	fmt.Fprintf(&b, "manifest: %s (%s)\n", s.Manifest.Path, s.Manifest.State)
	if s.Manifest.Error != "" {
		fmt.Fprintf(&b, "          %s\n", s.Manifest.Error)
	}

	entries := make([][2]string, len(s.Entries))
	for i, e := range s.Entries {
		state := "not installed"
		if e.Installed {
			state = "installed"
		}
		// The strategy column is padded to a fixed width rather than to the
		// content's, so it stays aligned across a farm where every entry happens
		// to share one strategy.
		entries[i] = [2]string{e.Name, fmt.Sprintf("%-7s  %s", e.Strategy, state)}
	}
	writeStatusSection(&b, "entries", entries)

	excluded := make([][2]string, len(s.Excluded))
	for i, x := range s.Excluded {
		excluded[i] = [2]string{x.Name, x.Reason}
	}
	writeStatusSection(&b, "excluded", excluded)

	shadowed := make([][2]string, len(s.Shadowed))
	for i, sh := range s.Shadowed {
		shadowed[i] = [2]string{sh.Name, sh.Path}
	}
	writeStatusSection(&b, "shadowed", shadowed)

	if _, err := io.WriteString(out, b.String()); err != nil {
		return fmt.Errorf("write source status: %w", err)
	}
	return nil
}
