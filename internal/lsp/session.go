package lsp

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// Loader resolves the repository the server serves and loads its
// configuration. cmd implements it: this package cannot import the config
// loader.
type Loader interface {
	// Root returns the datamitsu root — the topmost git root — containing dir.
	Root(ctx context.Context, dir string) (string, error)
	// Load evaluates the configuration for root. watch lists the files whose
	// content decides the result, as far as they are known; it is returned on
	// failure too, so a fix to a broken file is noticed.
	Load(ctx context.Context, root string) (cfg *config.Config, watch []string, err error)
	// Watch is the part of Load's watch list known before loading.
	Watch(root string) []string
}

// session is everything the server builds from one loaded configuration. A
// reload replaces it whole.
type session struct {
	planner  *tooling.Planner
	binMgr   *binmanager.BinManager
	executor *tooling.Executor
	cache    *cache.Cache // nil when the cache could not be built (formatting still works)

	// fixWidenTo is the project's execution.widenTo for fix. The editor policy is
	// clamped to it: a session default must not out-scope what the repository
	// asked for.
	fixWidenTo config.WidenTo

	// managedConfigs is what the preflight check compares the files a fix reads
	// against.
	managedConfigs config.MapOfManagedConfigs

	// tools is the configured tool set, which initializationOptions.format.tools
	// is checked against.
	tools config.MapOfTools

	// foreignCacheReported keeps the "cache belongs to another configuration"
	// notice to one per loaded configuration.
	foreignCacheReported bool
}

// newSession assembles the server's OWN lightweight planner+binManager+executor
// for cfg (no parser, no UI), so a format request never parses diagnostics or
// prints to stdout.
//
// The planner's cwd is the root, NOT the process launch directory: an editor
// selects which file to format from anywhere in the workspace, so the CLI's
// CWD-subtree restriction (which would silently drop files outside the launch
// dir) is wrong here — any file under the repo root is formattable.
//
// It shares the SAME execution cache the CLI uses (keyed on the git root, with
// matching invalidation key), so formatting a file in the editor warms the
// per-file fix cache and a later `datamitsu fix`/`check` can skip that unchanged
// file. Only per-file/globbed tools benefit — whole-project tools (e.g.
// golangci-lint fmt) the CLI always runs in bulk and never looks up per file.
func newSession(cfg *config.Config, root string) *session {
	rm := runtimemanager.New(cfg.Runtimes)
	binMgr := binmanager.New(cfg.Apps, cfg.Bundles, rm)

	planner := tooling.NewPlanner(root, root, nil, cfg.Tools, cfg.ProjectTypes, cfg.IgnoreRules)
	planner.SetPlatformChecker(binMgr)
	// Planned at unit, the widest level a save can reach; editorDecision applies
	// the project's own fix policy, because a task the planner drops reaches no
	// left-out notice and no skipped count.
	planner.SetWidenPolicy(cfg.Execution, config.WidenToUnit)

	// Build the same cache the CLI runner does so keys/paths align. selectedTools
	// is nil — the LSP, like the lefthook `check`, runs the full tool set, so the
	// invalidation keys match and entries are shared. A build failure is non-fatal:
	// formatting just runs without caching.
	projectCache, err := cache.NewCache(env.GetCachePath(), root, *cfg, nil, logger.Logger)
	if err != nil {
		emitLog(uievent.NextOpID("lsp"), uievent.LevelWarn, "cache unavailable, formatting without it: "+err.Error())
		projectCache = nil
	} else {
		// The session keeps the config it loaded; once the CLI writes a newer key,
		// overwriting it would reset both caches in a loop.
		projectCache.SetYieldToForeignKey(true)
	}

	return &session{
		planner:        planner,
		binMgr:         binMgr,
		executor:       tooling.NewExecutor(root, false, false, binMgr, projectCache),
		cache:          projectCache,
		fixWidenTo:     cfg.Execution.ResolveWidenTo(config.OpFix, ""),
		managedConfigs: cfg.ManagedConfigs,
		tools:          cfg.Tools,
	}
}

// close flushes the session's execution cache and stops its debounce timer.
func (ss *session) close() {
	if ss.cache != nil {
		ss.cache.Shutdown()
	}
}

// openSession serves the workspace initialize names: it resolves the root and
// loads its configuration. A failure leaves the session empty and says why; it
// never fails initialize, because a client that sees initialize fail gives up
// on the server for good.
func (s *Server) openSession(ctx context.Context, params initializeParams) {
	dir, err := s.workspaceDir(params)
	var root string
	if err == nil {
		dir = canonicalPath(dir)
		root, err = s.loader.Root(ctx, dir)
		if err == nil && root == "" {
			err = errors.New("no git repository contains it")
		}
	}
	if err != nil {
		what := dir
		if what == "" {
			what = "the workspace"
		}
		s.noSession = fmt.Sprintf("cannot serve %s: %v; this language server formats nothing until it is restarted "+
			"for a git repository", what, err)
		emitLog(uievent.NextOpID("lsp"), uievent.LevelError, s.noSession)
		return
	}
	s.root = canonicalPath(root)
	s.reportUnservedFolders(ctx, params.WorkspaceFolders)

	// Digested before loading, as a reload is: a checkout that lands while the
	// configuration is evaluated must still read as a change.
	if s.load(ctx, digestInputs(s.loader.Watch(s.root))) != nil {
		emitLog(uievent.NextOpID("lsp"), uievent.LevelError, s.noSession)
	}
}

// workspaceDir is the directory initialize names as the workspace: the first
// workspace folder, then rootUri, then the deprecated rootPath, then the
// directory the server was started in.
func (s *Server) workspaceDir(params initializeParams) (string, error) {
	switch {
	case len(params.WorkspaceFolders) > 0 && params.WorkspaceFolders[0].URI != "":
		return uriToPath(params.WorkspaceFolders[0].URI)
	case params.RootURI != nil && *params.RootURI != "":
		return uriToPath(*params.RootURI)
	case params.RootPath != nil && *params.RootPath != "":
		return *params.RootPath, nil
	case s.launchDir != "":
		return s.launchDir, nil
	}
	return "", errors.New("initialize names no workspace and the launch directory is unknown")
}

// reportUnservedFolders names every further workspace folder this server does
// not format: one server serves one repository.
func (s *Server) reportUnservedFolders(ctx context.Context, folders []workspaceFolder) {
	for i, folder := range folders {
		if i == 0 {
			continue
		}
		path, err := uriToPath(folder.URI)
		if err == nil {
			path = canonicalPath(path)
			var root string
			if root, err = s.loader.Root(ctx, path); err == nil && canonicalPath(root) == s.root {
				continue
			}
		} else {
			path = folder.URI
		}
		emitLog(uievent.NextOpID("lsp"), uievent.LevelInfo, fmt.Sprintf(
			"workspace folder %s is not formatted by this language server, which serves %s", path, s.root))
	}
}

// refreshSession reloads the configuration when a file it was loaded from has
// changed. It runs before each format, never during one: formatting the config
// file itself changes it.
//
// Bytes that failed to load are not retried: a half-edited config is the normal
// state between two saves, and it would otherwise be re-evaluated and re-warned
// about on every one.
func (s *Server) refreshSession(ctx context.Context) {
	if s.loader == nil || s.root == "" {
		return
	}
	current := digestInputs(s.watch)
	fingerprint := current.fingerprint()
	if (s.loaded != nil && fingerprint == s.loadedInputs) || fingerprint == s.failedInputs {
		return
	}

	hadSession := s.loaded != nil
	if err := s.load(ctx, current); err != nil {
		if hadSession {
			emitLog(uievent.NextOpID("lsp"), uievent.LevelWarn,
				"the configuration changed but failed to load; formatting continues with the previous one: "+err.Error())
		}
		return
	}
	emitLog(uievent.NextOpID("lsp"), uievent.LevelInfo, "configuration reloaded")
	s.resolvePolicy()
}

// load evaluates the configuration and, when it loads, replaces the session.
//
// prior is what the files held when the caller decided to load. A file that
// changes while the load runs keeps that digest, so the next request loads
// again rather than trusting bytes this load may never have read.
func (s *Server) load(ctx context.Context, prior inputDigests) error {
	cfg, watch, err := s.loader.Load(ctx, s.root)
	digests := digestInputs(watch)
	for path, digest := range prior {
		if _, ok := digests[path]; ok {
			digests[path] = digest
		}
	}
	s.watch = watch
	fingerprint := digests.fingerprint()

	if err != nil {
		s.failedInputs = fingerprint
		if s.loaded == nil {
			s.noSession = "the configuration failed to load: " + err.Error() +
				"; formatting does nothing until it loads, and it is read again on the next format after it changes"
		}
		return err
	}

	s.loadedInputs, s.failedInputs, s.noSession = fingerprint, "", ""
	// Flushed before the new cache reads the same file.
	if s.loaded != nil {
		if old := s.loaded.cache; old != nil && !slices.Contains(s.supersededKeys, old.InvalidationKey()) {
			s.supersededKeys = append(s.supersededKeys, old.InvalidationKey())
		}
		s.loaded.close()
	}
	s.loaded = newSession(cfg, s.root)
	if s.loaded.cache != nil {
		// A file still keyed by a session this one replaced is its own to take
		// over, not a newer configuration to yield to.
		s.loaded.cache.SetSupersededKeys(s.supersededKeys)
	}
	return nil
}

// reportNoSession repeats why nothing is formatted, once per distinct cause, so
// a save does not raise the same error every time.
func (s *Server) reportNoSession() {
	if s.noSession == "" || s.noSession == s.noSessionReported {
		return
	}
	s.noSessionReported = s.noSession
	emitLog(uievent.NextOpID("lsp"), uievent.LevelError, s.noSession)
}

// reportOutsideRoot names a document this server will not format, once per
// document.
func (s *Server) reportOutsideRoot(path string) {
	if s.refused == nil {
		s.refused = map[string]struct{}{}
	}
	if _, seen := s.refused[path]; seen {
		return
	}
	s.refused[path] = struct{}{}
	emitLog(uievent.NextOpID("lsp"), uievent.LevelInfo, fmt.Sprintf(
		"format %s: outside %s, the repository this language server serves; not formatted", path, s.root))
}

func (s *Server) closeSession() {
	if s.loaded != nil {
		s.loaded.close()
	}
}
