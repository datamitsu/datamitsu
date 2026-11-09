package managedconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/config"
)

// LauncherMarkerName is the file init writes into .datamitsu/ recording how
// the binary that ran init is invoked. It is machine-local (the .datamitsu/
// .gitignore tracks nothing) and is refreshed on every init: the .datamitsu/
// swap drops it and init writes it back afterwards.
const LauncherMarkerName = "launcher.json"

// LauncherMarker records how to invoke the datamitsu binary in this
// repository. facts().binaryCommand falls back to Command when the binary
// runs directly — neither a wrapper injecting --binary-command nor
// DATAMITSU_BINARY_COMMAND set — so hook commands rendered by config JS
// invoke datamitsu the way this repository's init did, independent of how a
// later reconcile happened to be invoked.
type LauncherMarker struct {
	// Command is the effective binaryCommand at init time: the
	// --binary-command flag (wrappers inject it), then
	// DATAMITSU_BINARY_COMMAND, then the executable's absolute path.
	Command string `json:"command"`
	// BinaryPath is the absolute, symlink-resolved path of the running binary.
	BinaryPath string `json:"binaryPath"`
	// CoreVersion is ldflags.Version of the binary that wrote the marker.
	CoreVersion string `json:"coreVersion"`
}

// WriteLauncherMarker writes the marker into .datamitsu/. Under dryRun it
// writes nothing.
func WriteLauncherMarker(rootPath string, marker LauncherMarker, dryRun bool) error {
	if dryRun {
		return nil
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode launcher marker: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Join(rootPath, config.DatamitsuDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	path := filepath.Join(dir, LauncherMarkerName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// copyLauncherMarker preserves an init-written launcher marker across a
// .datamitsu/ rebuild. Both swap builders start from an empty tree and copy
// only configs/; without this, exec's lazy link refresh would silently drop
// the marker and facts().binaryCommand would lose its fallback. init
// rewrites the marker afterwards, so preservation — not freshness — is the
// contract here.
func copyLauncherMarker(datamitsuDir, tmpDir string) error {
	data, err := os.ReadFile(filepath.Join(datamitsuDir, LauncherMarkerName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to read launcher marker: %w", err)
	}
	// Through an os.Root so a symlink planted inside tmpDir cannot make the
	// write land elsewhere (same containment as the internal-config writes).
	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", tmpDir, err)
	}
	writeErr := root.WriteFile(LauncherMarkerName, data, 0o644)
	closeErr := root.Close()
	if writeErr != nil {
		return fmt.Errorf("failed to preserve launcher marker: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to preserve launcher marker: %w", closeErr)
	}
	return nil
}

// ReadLauncherMarker reads the marker written by init. The bool is false when
// the marker is absent, unreadable, or not valid JSON — every caller then
// falls back to its default.
func ReadLauncherMarker(rootPath string) (LauncherMarker, bool) {
	var marker LauncherMarker
	path := filepath.Join(rootPath, config.DatamitsuDirName, LauncherMarkerName)
	data, err := os.ReadFile(path)
	if err != nil {
		return marker, false
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return marker, false
	}
	if marker.Command == "" {
		return marker, false
	}
	return marker, true
}
