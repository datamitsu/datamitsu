package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/spf13/cobra"
)

type runtimeListDocument struct {
	Schema   string             `json:"schema"`
	Runtimes []runtimeListEntry `json:"runtimes"`
}

type runtimeListEntry struct {
	Name              string                       `json:"name"`
	Kind              config.RuntimeKind           `json:"kind"`
	ConfiguredMode    config.RuntimeMode           `json:"configuredMode"`
	EffectiveMode     config.RuntimeMode           `json:"effectiveMode"`
	Version           string                       `json:"version"`
	Runnable          bool                         `json:"runnable"`
	NotRunnableReason string                       `json:"notRunnableReason,omitempty"`
	Status            runtimemanager.RuntimeStatus `json:"status"`
	Executable        *string                      `json:"executable,omitempty"`
	Reason            string                       `json:"reason,omitempty"`
}

type runtimeRunDocument struct {
	Schema       string                       `json:"schema"`
	Name         string                       `json:"name"`
	Kind         config.RuntimeKind           `json:"kind"`
	Executable   *string                      `json:"executable"`
	Args         []string                     `json:"args"`
	Status       runtimemanager.RuntimeStatus `json:"status"`
	WouldInstall bool                         `json:"wouldInstall"`
}

type runtimeExecRequest struct {
	Executable string
	Kind       config.RuntimeKind
	Args       []string
	Env        map[string]string
}

type runtimeExecHandoffError struct{ request runtimeExecRequest }

func (runtimeExecHandoffError) Error() string { return "runtime execution handoff" }

var (
	runtimeCmd                     = &cobra.Command{Use: "runtime", Short: "Inspect and run configured runtimes"}
	runtimeListJSON, runtimeRunDry bool
	runtimeListCmd                 = &cobra.Command{
		Use:   "list",
		Short: "List configured runtimes",
		Args:  usageArgs(cobra.NoArgs),
		RunE:  runRuntimeList,
	}
)

var runtimeRunCmd = &cobra.Command{
	Use:                   "run [--dry-run] <name> -- <args...>",
	Short:                 "Run a configured language interpreter",
	DisableFlagsInUseLine: true,
	RunE:                  runRuntimeRun,
}

func init() {
	runtimeListCmd.Flags().BoolVar(&runtimeListJSON, "json", false, "Write runtime inventory as JSON")
	runtimeRunCmd.Flags().BoolVar(&runtimeRunDry, "dry-run", false, "Describe execution without installing or running")
	runtimeCmd.AddCommand(runtimeListCmd, runtimeRunCmd)
	rootCmd.AddCommand(runtimeCmd)
}

func runtimeManager(ctx context.Context) (*runtimemanager.RuntimeManager, error) {
	cfg, _, _, err := loadConfigWithPaths(ctx, BeforeConfigPaths, NoAutoConfig, ConfigPaths)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return runtimemanager.New(cfg.Runtimes), nil
}

func runRuntimeList(cmd *cobra.Command, _ []string) error {
	rm, err := runtimeManager(commandContext(cmd))
	if err != nil {
		return err
	}
	entries, err := rm.RuntimeInventory()
	if err != nil {
		return err
	}
	rows := make([]runtimeListEntry, 0, len(entries))
	for _, entry := range entries {
		var executable *string
		if entry.Executable != "" {
			value := entry.Executable
			executable = &value
		}
		rows = append(rows, runtimeListEntry{
			Name: entry.Name, Kind: entry.Kind, ConfiguredMode: entry.ConfiguredMode,
			EffectiveMode: entry.EffectiveMode, Version: entry.Version,
			Runnable: entry.Runnable, Status: entry.Status, Executable: executable,
			NotRunnableReason: entry.NotRunnableReason,
			Reason:            entry.Reason,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	if runtimeListJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(runtimeListDocument{Schema: "datamitsu.runtimes/1", Runtimes: rows}); err != nil {
			return fmt.Errorf("encode runtime inventory: %w", err)
		}
		return nil
	}
	fmt.Printf("%-24s %-8s %-9s %-16s %-12s %-8s %s\n", "NAME", "KIND", "MODE", "VERSION", "STATUS", "RUNNABLE", "REASON")
	for _, row := range rows {
		runnable := "yes"
		if !row.Runnable {
			runnable = "no"
		}
		reason := row.NotRunnableReason
		if row.Reason != "" {
			if reason != "" {
				reason += "; "
			}
			reason += row.Reason
		}
		fmt.Printf("%-24s %-8s %-9s %-16s %-12s %-8s %s\n", row.Name, row.Kind, row.EffectiveMode, row.Version, row.Status, runnable, reason)
	}
	return nil
}

func runRuntimeRun(cmd *cobra.Command, args []string) error {
	separator := cmd.ArgsLenAtDash()
	if separator != 1 || len(args) <= separator {
		return usageError("usage: datamitsu runtime run [--dry-run] <name> -- <interpreter-args...>")
	}
	name := args[0]
	childArgs := args[separator:]
	if resolveLogFormat() == "jsonl" {
		return usageError("runtime run cannot use --log-format jsonl because child stderr is not JSON-L")
	}

	ctx := commandContext(cmd)
	rm, err := runtimeManager(ctx)
	if err != nil {
		return err
	}
	entry, found, err := runtimeInventoryEntry(rm, name)
	if err != nil {
		return err
	}
	if !found {
		return usageError(fmt.Sprintf("unknown runtime %q", name))
	}
	if !entry.Runnable {
		return usageError(fmt.Sprintf("runtime %q cannot be run directly: %s", name, entry.NotRunnableReason))
	}
	if entry.Status == runtimemanager.RuntimeStatusUnavailable {
		return fmt.Errorf("runtime %q unavailable: %s", name, entry.Reason)
	}

	if runtimeRunDry {
		doc := runtimeRunDocument{
			Schema: "datamitsu.runtime-run/1", Name: name, Kind: entry.Kind,
			Args: childArgs, Status: entry.Status,
			WouldInstall: entry.Status == runtimemanager.RuntimeStatusMissing &&
				(entry.EffectiveMode == config.RuntimeModeManaged || entry.Kind == config.RuntimeKindUV),
		}
		if entry.Executable != "" {
			value := entry.Executable
			doc.Executable = &value
		}
		if err := json.NewEncoder(os.Stdout).Encode(doc); err != nil {
			return fmt.Errorf("encode runtime run plan: %w", err)
		}
		return nil
	}

	execution, err := rm.EnsureExecution(ctx, name)
	if err != nil {
		if errors.Is(err, runtimemanager.ErrUnknownRuntime) {
			return usageError(err.Error())
		}
		return err
	}
	if execution.Executable == "" {
		return fmt.Errorf("runtime %q has no executable", name)
	}
	childEnv := make(map[string]string, len(execution.Env)+1)
	maps.Copy(childEnv, execution.Env)
	childEnv["PATH"] = filepath.Dir(execution.Executable) + string(os.PathListSeparator) + env.Path()
	return runtimeExecHandoffError{request: runtimeExecRequest{
		Executable: execution.Executable, Kind: execution.Kind, Args: childArgs, Env: childEnv,
	}}
}

func runtimeInventoryEntry(rm *runtimemanager.RuntimeManager, name string) (runtimemanager.RuntimeInventoryEntry, bool, error) {
	entries, err := rm.RuntimeInventory()
	if err != nil {
		return runtimemanager.RuntimeInventoryEntry{}, false, err
	}
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true, nil
		}
	}
	return runtimemanager.RuntimeInventoryEntry{}, false, nil
}

func usageError(message string) error {
	return exitcode.UsageError{Err: errors.New(strings.TrimSpace(message))}
}
