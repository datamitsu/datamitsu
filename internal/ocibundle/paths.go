package ocibundle

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"

	"go.uber.org/zap"
)

// uvPythonSubtree is the shared managed-CPython subtree every uv app venv
// links its interpreter into (mirrors internal/dockerfile/storepaths.go).
const uvPythonSubtree = ".uv/python"

// storeManagers bundles the two managers used to replicate the store path
// math. They are cheap to construct and stateless for path computation.
type storeManagers struct {
	rm *runtimemanager.RuntimeManager
	bm *binmanager.BinManager
}

func newStoreManagers(cfg *config.Config) storeManagers {
	rm := runtimemanager.New(cfg.Runtimes)
	return storeManagers{rm: rm, bm: binmanager.New(cfg.Apps, cfg.Bundles, rm)}
}

// subtreeRel converts an absolute store path into the store-relative subtree
// string used by the com.datamitsu.subtree annotations.
func subtreeRel(storeRoot, abs string) (string, error) {
	rel, err := filepath.Rel(storeRoot, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q is outside the store root %q", abs, storeRoot)
	}
	return filepath.ToSlash(rel), nil
}

// expectedSubtrees computes the store-relative subtrees the given tools (and
// their transitive store dependencies: the runtime of a runtime app, the
// shared CPython for uv) are expected to occupy.
// neededRuntimes adds standalone runtime targets (install --runtime). The map
// value is a human-readable owner label for diagnostics. Tools whose path
// cannot be computed (unknown name, shell app, platform-unavailable binary)
// contribute nothing — the regular network path reports those.
func expectedSubtrees(cfg *config.Config, storeRoot string, needed, neededRuntimes []string) map[string]string {
	mgrs := newStoreManagers(cfg)
	expected := make(map[string]string)

	addPath := func(abs, owner string) {
		rel, err := subtreeRel(storeRoot, abs)
		if err != nil {
			log.Debug("skipping expected path outside store", zap.String("owner", owner), zap.Error(err))
			return
		}
		expected[rel] = owner
	}

	addRuntime := func(runtimeName string) {
		rc, ok := cfg.Runtimes[runtimeName]
		if !ok {
			return
		}
		if rc.Kind != config.RuntimeKindGo {
			if runtimePath, managed, err := mgrs.rm.ComputeRuntimeStorePath(runtimeName); err != nil {
				log.Debug("cannot compute runtime store path",
					zap.String("runtime", runtimeName), zap.Error(err))
			} else if managed {
				addPath(runtimePath, "runtime "+runtimeName)
			}
		}
	}

	for _, runtimeName := range neededRuntimes {
		addRuntime(runtimeName)
	}

	for _, name := range needed {
		app, ok := cfg.Apps[name]
		if !ok || app.Shell != nil {
			continue
		}

		abs, err := mgrs.bm.ComputeInstallPath(name)
		if err != nil {
			log.Debug("cannot compute install path for needed tool; bundle cannot cover it",
				zap.String("app", name), zap.Error(err))
			continue
		}
		addPath(abs, name)

		kind, runtimeRef, isRuntimeApp := runtimeAppKind(app)
		if !isRuntimeApp {
			continue
		}

		runtimeName, _, err := mgrs.rm.ResolveRuntime(runtimeRef, kind)
		if err != nil {
			log.Debug("cannot resolve runtime for needed tool",
				zap.String("app", name), zap.Error(err))
			continue
		}

		// The Go SDK is build-only and never bundled (storepaths model); all
		// other managed runtimes ship as their own subtree.
		if kind != config.RuntimeKindGo {
			if runtimePath, managed, err := mgrs.rm.ComputeRuntimeStorePath(runtimeName); err != nil {
				log.Debug("cannot compute runtime store path",
					zap.String("runtime", runtimeName), zap.Error(err))
			} else if managed {
				addPath(runtimePath, "runtime "+runtimeName)
			}
		}

		// pnpm deliberately does NOT join the expected set: the bundle never
		// carries it (the final image stage copies only declared runtimes) and
		// executing a seeded node app needs node + the app subtree, not pnpm —
		// pnpm is an install-time tool. Listing it would defeat the zero-network
		// fast path: one permanently-missing subtree forces a manifest fetch on
		// every run.
		if kind == config.RuntimeKindUV {
			expected[uvPythonSubtree] = "shared CPython (uv)"
		}
	}

	return expected
}

// runtimeAppKind reports a runtime-managed app's kind and explicit runtime
// reference, mirroring the App.* sub-config precedence used by runtimemanager.
func runtimeAppKind(app binmanager.App) (kind config.RuntimeKind, runtimeRef string, ok bool) {
	switch {
	case app.Uv != nil:
		return config.RuntimeKindUV, app.Uv.Runtime, true
	case app.Node != nil:
		return config.RuntimeKindNode, app.Node.Runtime, true
	case app.Jvm != nil:
		return config.RuntimeKindJVM, app.Jvm.Runtime, true
	case app.Go != nil:
		return config.RuntimeKindGo, app.Go.Runtime, true
	default:
		return "", "", false
	}
}

// reVerifySpec describes a published-hash re-verification for one subtree:
// the file (relative to the subtree root; empty = the subtree itself is the
// file) and its expected SHA-256.
type reVerifySpec struct {
	relPath string
	sha256  string
	owner   string
}

// buildReVerifyIndex maps subtrees to their published-SHA-256 checks. Only
// artifacts whose stored bytes equal the verified download are re-verifiable:
// single-file binaries (contentType "binary", stored verbatim) and JVM jars
// (stored verbatim as <app>.jar). Archive-typed binaries store the EXTRACTED
// payload while the published hash covers the archive, so the digest chain is
// their boundary — same as runtime app directories.
func buildReVerifyIndex(cfg *config.Config, storeRoot string) map[string]reVerifySpec {
	mgrs := newStoreManagers(cfg)
	index := make(map[string]reVerifySpec)

	names := make([]string, 0, len(cfg.Apps))
	for name := range cfg.Apps {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		app := cfg.Apps[name]
		switch {
		case app.Binary != nil:
			info, err := mgrs.bm.ResolvedBinaryInfo(name)
			if err != nil {
				continue
			}
			if info.ContentType != binmanager.BinContentTypeBinary || info.ExtractDir {
				continue
			}
			abs, err := mgrs.bm.ComputeInstallPath(name)
			if err != nil {
				continue
			}
			rel, err := subtreeRel(storeRoot, abs)
			if err != nil {
				continue
			}
			index[rel] = reVerifySpec{relPath: "", sha256: info.Hash, owner: name}
		case app.Jvm != nil:
			abs, err := mgrs.bm.ComputeInstallPath(name)
			if err != nil {
				continue
			}
			rel, err := subtreeRel(storeRoot, abs)
			if err != nil {
				continue
			}
			index[rel] = reVerifySpec{relPath: name + ".jar", sha256: app.Jvm.JarHash, owner: name}
		}
	}
	return index
}
