package managedconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// PreflightError lists every managed config file that is not where, or not
// what, the configuration says. Each problem names the command that fixes it.
type PreflightError struct {
	Problems []string
}

func (e *PreflightError) Error() string {
	return "managed config files are not in the state the configuration expects:\n  " + strings.Join(e.Problems, "\n  ")
}

// CheckConfigFiles verifies, before any tool runs or any cached result is
// trusted, that each ejectable managed config a task reads is in the place its
// placement says, and that an internal one holds the current render. A tool
// pointed at a missing file fails or silently falls back to its defaults, and
// one pointed at a stale file answers for a configuration that no longer
// exists — neither can be told apart from a real result afterwards.
//
// Entries that are not ejectable are left alone: their files are the
// repository's, as they always were.
func CheckConfigFiles(gitRoot string, configs config.MapOfManagedConfigs, refs []config.ManagedConfigRef) error {
	toolsByKey := make(map[string][]string)
	for _, ref := range refs {
		if !slices.Contains(toolsByKey[ref.Key], ref.Tool) {
			toolsByKey[ref.Key] = append(toolsByKey[ref.Key], ref.Tool)
		}
	}
	keys := make([]string, 0, len(toolsByKey))
	for key := range toolsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var problems []string
	for _, key := range keys {
		mc, ok := configs[key]
		if !ok || !mc.Ejectable {
			continue
		}
		sort.Strings(toolsByKey[key])
		if problem := checkConfigFile(gitRoot, key, mc, toolsByKey[key]); problem != "" {
			problems = append(problems, problem)
		}
		problems = append(problems, ignoredAlternates(gitRoot, key, mc, toolsByKey[key])...)
	}
	if len(problems) > 0 {
		return &PreflightError{Problems: problems}
	}
	return nil
}

func checkConfigFile(gitRoot, key string, mc config.ManagedConfig, tools []string) string {
	repoPath := filepath.Join(gitRoot, filepath.FromSlash(key))
	_, repoErr := os.Lstat(repoPath)
	repoExists := repoErr == nil

	if mc.Placement != config.PlacementInternal {
		if !repoExists {
			return fmt.Sprintf("%s is ejected but missing from the repository; run `datamitsu config reconcile --tools %s` to write it",
				key, strings.Join(mc.Tools, ","))
		}
		return ""
	}

	ownerTool := tools[0]
	if len(mc.Tools) > 0 {
		ownerTool = mc.Tools[0]
	}
	if repoExists {
		return fmt.Sprintf("%s is in the repository, but %s reads %s because %q is not in ejectConfigs; add %q to ejectConfigs to keep using the repository file, or run `datamitsu config reconcile` to remove it",
			key, strings.Join(tools, ", "), config.InternalConfigRelPath(key), ownerTool, ownerTool)
	}

	internalPath := filepath.Join(gitRoot, filepath.FromSlash(config.InternalConfigRelPath(key)))
	data, err := os.ReadFile(internalPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return config.InternalConfigRelPath(key) + " is missing; run `datamitsu init` to write it"
	case err != nil:
		return fmt.Sprintf("%s cannot be read (%v); run `datamitsu init` to rewrite it", config.InternalConfigRelPath(key), err)
	case mc.Render == nil || hashutil.XXH3Hex(data) != mc.Render.Hash:
		return config.InternalConfigRelPath(key) + " is out of date with the configuration; run `datamitsu init` to rewrite it"
	}
	return ""
}

// ignoredAlternates reports files under the entry's other names. The tool is
// handed the canonical path, so anything in them is silently ignored — the
// project's rules in a gitleaks.toml would stop applying without a word.
func ignoredAlternates(gitRoot, key string, mc config.ManagedConfig, tools []string) []string {
	var problems []string
	for _, alt := range mc.OtherFileNameList {
		if alt == key {
			continue
		}
		if _, err := os.Lstat(filepath.Join(gitRoot, filepath.FromSlash(alt))); err != nil {
			continue
		}
		reads := key
		if mc.Placement == config.PlacementInternal {
			reads = config.InternalConfigRelPath(key)
		}
		problems = append(problems, fmt.Sprintf("%s is another name for %s, which %s never reads (it reads %s); run `datamitsu config reconcile` to remove it, or move what it holds into %s",
			alt, key, strings.Join(tools, ", "), reads, key))
	}
	return problems
}
