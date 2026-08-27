package tooling

import (
	"context"
	"maps"
	"sync"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/trace"
)

// Command-info counters. A resolve count above the number of distinct apps in a
// plan means the executor is re-resolving a binary path it already knows.
var (
	cntCmdInfoResolved = trace.NewCounter("exec.command_info_resolved")
	cntCmdInfoMemoHits = trace.NewCounter("exec.command_info_memo_hits")
)

// commandInfoMemo resolves an app's CommandInfo once per Execute and hands out
// a copy to every task that asks for it.
//
// EnsureTools has already installed everything in the plan before the executor
// runs, so within one Execute the answer is constant: resolving it per task only
// re-walks the store and rebuilds the merged app env. The memo is not a cache
// across runs — Execute resets it, so an install that happens between two runs
// is always seen.
//
// Callers get a copy, never the shared pointer: CommandInfo carries an Env map
// and two slices, and one task mutating them would silently rewrite another
// task's command line.
type commandInfoMemo struct {
	mu      sync.Mutex
	entries map[string]*commandInfoEntry
}

// commandInfoEntry holds one app's resolution. once is what makes the resolve
// single-flighted: the executor's worker pool starts many tasks for the same app
// at the same moment, and each must wait for the first resolve rather than start
// its own.
type commandInfoEntry struct {
	once sync.Once
	info *binmanager.CommandInfo
	err  error
}

func newCommandInfoMemo() *commandInfoMemo {
	return &commandInfoMemo{entries: map[string]*commandInfoEntry{}}
}

// get returns app's CommandInfo, calling resolve at most once per app. A failure
// is memoized too: within one Execute a tool that cannot be resolved cannot
// become resolvable, and retrying it per task turns one error into N installs.
func (m *commandInfoMemo) get(
	ctx context.Context,
	app string,
	resolve func(context.Context, string) (*binmanager.CommandInfo, error),
) (*binmanager.CommandInfo, error) {
	m.mu.Lock()
	entry, ok := m.entries[app]
	if !ok {
		entry = &commandInfoEntry{}
		m.entries[app] = entry
	}
	m.mu.Unlock()

	resolved := false
	entry.once.Do(func() {
		resolved = true
		cntCmdInfoResolved.Add(1)
		entry.info, entry.err = resolve(ctx, app)
	})
	if !resolved {
		cntCmdInfoMemoHits.Add(1)
	}

	if entry.err != nil {
		return nil, entry.err
	}
	return cloneCommandInfo(entry.info), nil
}

// cloneCommandInfo deep-copies everything a caller could reach and write
// through: the shared entry must stay exactly as it was resolved.
func cloneCommandInfo(src *binmanager.CommandInfo) *binmanager.CommandInfo {
	if src == nil {
		return nil
	}
	dst := *src
	if src.Args != nil {
		dst.Args = append([]string(nil), src.Args...)
	}
	if src.RequiredPaths != nil {
		dst.RequiredPaths = append([]string(nil), src.RequiredPaths...)
	}
	if src.Env != nil {
		dst.Env = make(map[string]string, len(src.Env))
		maps.Copy(dst.Env, src.Env)
	}
	return &dst
}

// commandInfo resolves task's app through the per-Execute memo when one is
// active. FormatContent (the LSP path) runs outside Execute and has no memo, so
// it resolves directly.
func (e *Executor) commandInfo(ctx context.Context, app string) (*binmanager.CommandInfo, error) {
	memo := e.cmdInfos.Load()
	if memo == nil {
		return e.appManager.GetCommandInfo(ctx, app)
	}
	return memo.get(ctx, app, e.appManager.GetCommandInfo)
}
