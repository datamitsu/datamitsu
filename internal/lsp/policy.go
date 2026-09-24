package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

// formatPolicy is the editor's session policy for format-on-save. It comes from
// initialize's options and is resolved again only when the configuration
// reloads, since format.tools is checked against the configured tools. It is
// deliberately not part of config.Config: the whole config is hashed into the
// cache invalidation key, and an editor latency preference must not reset the
// shared cache.
type formatPolicy struct {
	WidenTo   config.WidenTo
	TimeoutMs int
	Tools     map[string]bool
}

const (
	keyWidenTo   = "widenTo"
	keyTimeoutMs = "timeoutMs"
	keyTools     = "tools"
)

// envFormatPolicy is the policy an editor that sends no initializationOptions
// gets: the environment, then the built-in defaults.
func envFormatPolicy() formatPolicy {
	return formatPolicy{
		WidenTo:   editorWidenTo(),
		TimeoutMs: editorTimeoutMs(),
		Tools:     map[string]bool{},
	}
}

// editorWidenTo is the environment's session policy. Default "unit": a "target"
// default would silently disable format-on-save for Go, Python, Rust and
// Terraform, whose fix operations are all per-project with no file arguments —
// and format-on-save for Go is a documented headline feature.
func editorWidenTo() config.WidenTo {
	policy := env.GetLspFormatWidenTo()
	if eff, err := runtimeconfig.Get(); err == nil {
		policy = eff.LspFormatWidenTo
	}
	switch config.WidenTo(policy) {
	case config.WidenToTarget:
		return config.WidenToTarget
	case config.WidenToUnit:
		return config.WidenToUnit
	case config.WidenToRepo:
		// Not a legal editor policy: a repository-wide fix on save is never
		// acceptable, so ask for it and you get the default instead.
		return config.WidenToUnit
	default:
		return config.WidenToUnit
	}
}

func editorTimeoutMs() int {
	if eff, err := runtimeconfig.Get(); err == nil {
		return eff.LspFormatTimeoutMs
	}
	return env.GetLspFormatTimeoutMs()
}

// policyResolution is a resolved policy plus what the echo needs to explain it.
type policyResolution struct {
	Policy formatPolicy
	// FromOptions lists the keys initializationOptions set, in key order.
	FromOptions []string
	// Warnings are the options that were rejected, one notice each.
	Warnings []string
}

// resolveFormatPolicy applies initializationOptions over base. It never fails:
// initialize must succeed whatever the editor sends, so a rejected option only
// warns and leaves its key on the environment or default. isTool reports
// whether a name is a configured tool.
func resolveFormatPolicy(raw json.RawMessage, base formatPolicy, isTool func(string) bool) policyResolution {
	res := policyResolution{Policy: base}
	res.Policy.Tools = map[string]bool{}

	if isNull(raw) {
		return res
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(raw, &options); err != nil {
		res.warn("initializationOptions is not an object; ignored")
		return res
	}

	// "diagnostics" is reserved and every other top-level key belongs to a
	// future version: both are ignored without a notice.
	format, ok := options["format"]
	if !ok || isNull(format) {
		return res
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(format, &fields); err != nil {
		res.warn("initializationOptions.format is not an object; ignored")
		return res
	}

	for _, key := range sortedKeys(fields) {
		value := fields[key]
		if isNull(value) {
			continue // an explicit null reads as "not set"
		}
		switch key {
		case keyWidenTo:
			res.applyWidenTo(value)
		case keyTimeoutMs:
			res.applyTimeoutMs(value)
		case keyTools:
			res.applyTools(value, isTool)
		default:
			res.warn(fmt.Sprintf("format.%s: unknown key; ignored", key))
		}
	}
	return res
}

func (r *policyResolution) applyWidenTo(value json.RawMessage) {
	var level string
	if err := json.Unmarshal(value, &level); err != nil {
		r.warn(fmt.Sprintf(`format.widenTo: expected "target" or "unit", got %s; using %q`, value, r.Policy.WidenTo))
		return
	}
	switch config.WidenTo(level) {
	case config.WidenToTarget, config.WidenToUnit:
		r.Policy.WidenTo = config.WidenTo(level)
		r.FromOptions = append(r.FromOptions, keyWidenTo)
	case config.WidenToRepo:
		r.warn(fmt.Sprintf(`format.widenTo: "repo" is not allowed in the editor: a repository-wide fix never runs on save; using %q`, r.Policy.WidenTo))
	default:
		r.warn(fmt.Sprintf(`format.widenTo: expected "target" or "unit", got %s; using %q`, value, r.Policy.WidenTo))
	}
}

func (r *policyResolution) applyTimeoutMs(value json.RawMessage) {
	var ms float64
	if err := json.Unmarshal(value, &ms); err != nil || ms < 0 || ms != math.Trunc(ms) {
		r.warn(fmt.Sprintf("format.timeoutMs: expected a non-negative integer, got %s; using %d", value, r.Policy.TimeoutMs))
		return
	}
	if ms > float64(env.MaxLspFormatTimeoutMs) {
		r.warn(fmt.Sprintf("format.timeoutMs: %s is more than the largest limit, %d; using %d",
			value, env.MaxLspFormatTimeoutMs, r.Policy.TimeoutMs))
		return
	}
	r.Policy.TimeoutMs = int(ms)
	r.FromOptions = append(r.FromOptions, keyTimeoutMs)
}

func (r *policyResolution) applyTools(value json.RawMessage, isTool func(string) bool) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(value, &entries); err != nil {
		r.warn("format.tools: expected an object of tool names to booleans; ignored")
		return
	}
	for _, name := range sortedKeys(entries) {
		if isNull(entries[name]) {
			continue // an explicit null reads as "not set"
		}
		if isTool == nil || !isTool(name) {
			r.warn(fmt.Sprintf("format.tools.%s: unknown tool; ignored", name))
			continue
		}
		var on bool
		if err := json.Unmarshal(entries[name], &on); err != nil {
			r.warn(fmt.Sprintf("format.tools.%s: expected a boolean, got %s; ignored", name, entries[name]))
			continue
		}
		r.Policy.Tools[name] = on
	}
	r.FromOptions = append(r.FromOptions, keyTools)
}

func (r *policyResolution) warn(msg string) {
	r.Warnings = append(r.Warnings, msg)
}

// summary is the one-line echo of the effective policy, naming the keys that
// came from initializationOptions.
func (r *policyResolution) summary() string {
	tools, _ := json.Marshal(r.Policy.Tools) //nolint:errchkjson // map[string]bool always marshals
	from := "none"
	if len(r.FromOptions) > 0 {
		from = strings.Join(r.FromOptions, ", ")
	}
	return fmt.Sprintf("format policy: widenTo=%s timeoutMs=%d tools=%s (from initializationOptions: %s)",
		r.Policy.WidenTo, r.Policy.TimeoutMs, tools, from)
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
