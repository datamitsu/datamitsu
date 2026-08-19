// Package sourcefarm computes the source-mode farm plan: which declared apps
// become commands on PATH, how each one is materialized, and which are refused.
//
// The package is pure. BuildPlan reads a config's app map, asks an injected
// side-effect-free resolver where each app would live, asks an injected
// lookPath where the pre-activation PATH would have found the same name, and
// returns a value. It opens no config, touches no network, and creates no
// files — materializing the plan is a separate concern.
//
// Two invariants shape everything here:
//
//   - Every declared app that is not categorically refused gets an Entry, even
//     when it has never been downloaded. A declared name that is absent from
//     the farm falls through PATH to whatever the system happens to have, which
//     exits 0 and prints plausible output from the wrong binary. That failure
//     mode is worse than not shipping the feature, so "not installed yet" is a
//     shim Entry with Installed=false, never an exclusion.
//   - Refusals are recorded, never silent. A name that simply does not appear
//     is undebuggable; every Excluded carries a Reason, and that Reason is what
//     `datamitsu source status` prints.
package sourcefarm

import (
	"sort"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

// Strategy is how a farm entry is materialized on disk.
type Strategy string

const (
	// StrategySymlink is a real symlink from the farm into the
	// content-addressed store. The kernel resolves one hop and runs the target,
	// so datamitsu contributes no measurable overhead to the invocation.
	StrategySymlink Strategy = "symlink"

	// StrategyShim routes the invocation back through the datamitsu executable,
	// which re-resolves and execs the real target. It is required whenever the
	// invocation cannot be expressed as "run this file with the user's argv":
	// an argv prefix (jvm's `java -jar`), an environment overlay (node's PATH
	// and npm_config_*, uv's cache dirs, any app carrying Env), or a target
	// that does not exist yet.
	StrategyShim Strategy = "shim"
)

// Exclusion reasons. They are constants because `source status` prints them
// verbatim and the CLI golden tests pin the text.
const (
	// ReasonShellApp is why shell apps can never enter the farm. A shell app
	// resolves its bare command name through the *inherited* PATH at spawn
	// time, and the farm is prepended to that PATH. A shell app named `echo`
	// would therefore resolve to the farm's own `echo`, which re-enters
	// datamitsu, which spawns `echo` again. Nothing in the repo uses
	// syscall.Exec on that path, so every level is a surviving process: this is
	// a fork bomb, not a slow path. The rule is mechanical on the app kind, not
	// a policy a config can opt out of.
	ReasonShellApp = "shell apps resolve through the inherited PATH"

	// ReasonDenyListed is why a name on denyList never enters the farm,
	// regardless of what it resolves to.
	ReasonDenyListed = "name is on the source-mode deny-list"

	// ReasonNoConfiguration is why an app declaring no recognized kind is
	// refused. Config validation rejects these, so reaching it means the plan
	// was built from an unvalidated map.
	ReasonNoConfiguration = "app declares no recognized kind"
)

// denyList is the set of names datamitsu refuses to put on PATH even when a
// config declares them.
//
// Two distinct hazards share one list. The privilege and remote-execution
// tools (sudo, ssh, …) plus the shells are names where interposing a
// config-controlled binary converts a config into a local privilege-escalation
// or credential-interception surface. `datamitsu` itself is the other hazard
// and is the sharper one: the shim spawns datamitsu to install and to re-bake,
// so a shimmed `datamitsu` makes that spawn find the shim, which spawns
// datamitsu, without bound. aqua-proxy refuses to proxy its own name for
// exactly this reason.
//
// `git` is here because datamitsu shells out to git for root discovery and
// because git's own subprocess protocol (hooks, credential helpers, pagers)
// makes interposition unusually far-reaching.
var denyList = map[string]struct{}{
	"sudo":      {},
	"su":        {},
	"doas":      {},
	"sudoedit":  {},
	"sh":        {},
	"bash":      {},
	"zsh":       {},
	"fish":      {},
	"dash":      {},
	"env":       {},
	"ssh":       {},
	"scp":       {},
	"sftp":      {},
	"git":       {},
	"datamitsu": {},
}

// Entry is one command the farm makes available on PATH.
type Entry struct {
	// Name is the app name, which is also the farm filename and the dispatch
	// key the shim looks up. Config validation guarantees it is a safe
	// filesystem entry.
	Name string `json:"name"`

	// Provider is the resolved CommandInfo type — what actually runs the
	// command. It is recorded separately from Kind because the resolver, not
	// the declaration, is authoritative about the invocation; when an app has
	// not been resolvable at all, Provider is empty while Kind still says what
	// was declared.
	Provider string `json:"provider,omitempty"`

	// Kind is the declared app kind ("binary", "go", "node", "uv", "jvm").
	Kind string `json:"kind"`

	// Strategy is symlink or shim, decided mechanically by strategyFor.
	Strategy Strategy `json:"strategy"`

	// Command is the absolute path (or, for a not-yet-resolvable app, the empty
	// string) the entry ultimately execs.
	Command string `json:"command,omitempty"`

	// Args are prepended to the user's argv by the shim.
	Args []string `json:"args,omitempty"`

	// Env is the overlay merged into the environment by the shim.
	Env map[string]string `json:"env,omitempty"`

	// Installed reports whether Command exists on disk right now. False means
	// the shim installs on first use — this is what makes activation lazy.
	Installed bool `json:"installed"`
}

// Excluded is a declared app that deliberately did not become an Entry.
type Excluded struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Shadow is a farm entry whose name also existed on the pre-activation PATH.
// It is informational: the entry stays in the farm and wins, and the recorded
// path is what the user was getting before activation.
type Shadow struct {
	Name string `json:"name"`
	// Path is the location the injected lookPath reported, verbatim.
	Path string `json:"path"`
}

// Plan is the complete, deterministic description of a farm.
type Plan struct {
	// Root is the git root the plan was built for.
	Root string `json:"root"`
	// FarmDir is the directory the entries are materialized into.
	FarmDir string `json:"farmDir"`
	// Entries are sorted by Name.
	Entries []Entry `json:"entries"`
	// Excluded are sorted by Name.
	Excluded []Excluded `json:"excluded"`
	// Shadowed are sorted by Name.
	Shadowed []Shadow `json:"shadowed,omitempty"`
}

// Resolver answers "where would this app be, and is it there?" without
// installing anything. binmanager.BinManager implements it.
type Resolver interface {
	ResolveCommandInfo(appName string) (*binmanager.CommandInfo, bool, error)
}

// LookPathFunc reports where a bare command name resolves on the
// pre-activation PATH. exec.LookPath is the production implementation; tests
// inject a stub so shadow detection does not depend on the host.
type LookPathFunc func(name string) (string, error)

// BuildPlan computes the farm plan for apps.
//
// It never fails: an app the resolver cannot answer for still becomes a shim
// Entry with an empty Command and Installed=false, so the name is present in
// the farm and the shim can fail loudly with exit 127 instead of letting PATH
// fall through to a system binary of the same name.
//
// resolve and lookPath may both be nil: a nil resolver means nothing is known
// to be installed (every entry is an uninstalled shim), and a nil lookPath
// means shadow detection is skipped.
func BuildPlan(root, farmDir string, apps binmanager.MapOfApps, resolve Resolver, lookPath LookPathFunc) Plan {
	plan := Plan{
		Root:     root,
		FarmDir:  farmDir,
		Entries:  make([]Entry, 0, len(apps)),
		Excluded: make([]Excluded, 0),
	}

	for name, app := range apps {
		if app.Shell != nil {
			plan.Excluded = append(plan.Excluded, Excluded{Name: name, Reason: ReasonShellApp})
			continue
		}
		if _, denied := denyList[name]; denied {
			plan.Excluded = append(plan.Excluded, Excluded{Name: name, Reason: ReasonDenyListed})
			continue
		}
		kind := kindOf(app)
		if kind == "" {
			plan.Excluded = append(plan.Excluded, Excluded{Name: name, Reason: ReasonNoConfiguration})
			continue
		}

		entry := Entry{Name: name, Kind: kind, Strategy: StrategyShim}
		if resolve != nil {
			// A resolve error is not an exclusion: see the D4 note on BuildPlan.
			// The entry stays, minus everything the resolver would have filled in.
			if info, installed, err := resolve.ResolveCommandInfo(name); err == nil && info != nil {
				entry.Provider = info.Type
				entry.Command = info.Command
				entry.Args = info.Args
				entry.Env = info.Env
				entry.Installed = installed
			}
		}
		entry.Strategy = strategyFor(entry)
		plan.Entries = append(plan.Entries, entry)

		if lookPath != nil {
			if found, err := lookPath(name); err == nil && found != "" {
				plan.Shadowed = append(plan.Shadowed, Shadow{Name: name, Path: found})
			}
		}
	}

	sort.Slice(plan.Entries, func(i, j int) bool { return plan.Entries[i].Name < plan.Entries[j].Name })
	sort.Slice(plan.Excluded, func(i, j int) bool { return plan.Excluded[i].Name < plan.Excluded[j].Name })
	sort.Slice(plan.Shadowed, func(i, j int) bool { return plan.Shadowed[i].Name < plan.Shadowed[j].Name })

	return plan
}

// strategyFor decides how an entry is materialized. Every entry is a shim.
//
// A symlink straight into the content-addressed store is the cheapest possible
// entry — the kernel resolves one hop and datamitsu contributes nothing
// measurable — and four conditions would make one safe to emit: kind binary or
// go (any other kind needs an argv prefix or an env overlay, and a node app
// cannot be symlinked at all, because pnpm's .bin wrapper computes its basedir
// from $0), installed (a symlink to a missing file is ENOENT at exec time, not
// a trigger to install it), no Args, and no Env.
//
// It is still wrong, for a reason none of those conditions covers: a symlink
// has nowhere to put the freshness check. Nothing of datamitsu's runs when the
// kernel follows it, so after `git checkout v2` the entry keeps resolving to
// the version the previous branch pinned — silently, exiting 0, printing
// plausible output. That is the exact failure the feature exists to prevent,
// and it defeats transparent branch switching for precisely the tools the
// optimization was meant for: installed binary apps.
//
// The real-shell tier found this by running the case an installed tool actually
// takes (test/shell: TestBranchSwitchOnASingleLine). The check has to happen
// per invocation, so the invocation has to reach a process — which is what a
// shim is. Materialization still implements StrategySymlink and the manifest
// still round-trips it, so re-enabling is one branch here if a cheaper
// revalidation hook ever exists.
func strategyFor(Entry) Strategy {
	return StrategyShim
}

// kindOf returns the declared app kind, or "" when the app declares none.
// Shell is handled by the caller before this point and is deliberately absent.
func kindOf(app binmanager.App) string {
	switch {
	case app.Binary != nil:
		return "binary"
	case app.Go != nil:
		return "go"
	case app.Node != nil:
		return "node"
	case app.Uv != nil:
		return "uv"
	case app.Jvm != nil:
		return "jvm"
	default:
		return ""
	}
}

// DenyListed reports whether name is refused by the hard deny-list. Exported
// so `source status` and the config validator can explain the refusal without
// duplicating the list.
func DenyListed(name string) bool {
	_, ok := denyList[name]
	return ok
}
