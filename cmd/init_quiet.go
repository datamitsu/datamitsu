package cmd

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/managedconfig"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
)

type artifactState struct {
	exists bool
	mode   os.FileMode
	link   string
	data   string
}

func configuredLinkState(root string) (map[string]artifactState, error) {
	state := map[string]artifactState{}
	base := filepath.Join(root, ".datamitsu")
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == base {
				return nil
			}
			return err
		}
		if path == base {
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return fmt.Errorf("relative managed artifact path: %w", err)
		}
		if rel == config.InternalConfigsDir {
			return filepath.SkipDir
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		if rel == managedconfig.LauncherMarkerName {
			return nil
		}
		state[filepath.Clean(rel)] = linkState(root, rel)
		return nil
	})
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return nil, fmt.Errorf("walk managed artifacts: %w", err)
	}
	return state, nil
}

func linkState(root, rel string) artifactState {
	path := filepath.Join(root, ".datamitsu", filepath.Clean(rel))
	info, err := os.Lstat(path)
	if err != nil {
		return artifactState{}
	}
	state := artifactState{exists: true, mode: info.Mode()}
	if info.Mode()&os.ModeSymlink != 0 {
		state.link, _ = os.Readlink(path)
	} else if info.Mode().IsRegular() {
		data, _ := os.ReadFile(path)
		state.data = string(data)
	}
	return state
}

func sameLinkState(a, b map[string]artifactState) bool { return maps.Equal(a, b) }

func sameRuntimeState(a, b map[string]bool) bool { return maps.Equal(a, b) }
func installedToolState(bm *binmanager.BinManager, cfg *config.Config) map[string]bool {
	state := map[string]bool{}
	for name, app := range cfg.Apps {
		if app.Binary == nil {
			continue
		}
		state[name] = bm.AppInstalled(name)
	}
	return state
}

func runtimeInstallState(rm *runtimemanager.RuntimeManager, names []string) map[string]bool {
	state := map[string]bool{}
	for _, name := range names {
		state[name] = rm.Installed(name)
	}
	return state
}

func installedRuntimeAppState(binMgr *binmanager.BinManager, cfg *config.Config) map[string]bool {
	state := map[string]bool{}
	for name, app := range cfg.Apps {
		if app.Bun == nil && app.Uv == nil && app.Node == nil && app.Jvm == nil && app.Go == nil {
			continue
		}
		state[name] = binMgr.AppInstalled(name)
	}
	return state
}

func preparedState(binMgr *binmanager.BinManager, cfg *config.Config, all bool) map[string]bool {
	names, err := initInstallAppNames(cfg, all)
	state := map[string]bool{}
	if err != nil {
		return state
	}
	for _, name := range names {
		app, ok := cfg.Apps[name]
		if !ok || app.Prepare == nil {
			continue
		}
		ready, err := binMgr.PreparedStateReady(name)
		state[name] = err == nil && ready
	}
	return state
}
