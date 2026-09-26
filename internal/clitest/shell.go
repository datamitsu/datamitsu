package clitest

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// MarkerDirName is the project-relative directory where shell tools record that
// they ran. ShellTool hands its absolute path to every tool as $MARKERS.
const MarkerDirName = ".markers"

// RecordRun is the script fragment that marks a run: it appends the tool's name
// and its first argument, as one line, to the tool's marker file. A ShellTool
// script receives its tool name as $0 and the operation's arguments from $1.
const RecordRun = `echo "$0 $1" >> "$MARKERS/$0"`

// ToolOpSpec is the one operation a ShellTool declares. Zero values mean the
// common case: a lint operation, repository scope, no arguments.
type ToolOpSpec struct {
	// Operation is "lint" when empty.
	Operation string
	// Scope is "repository" when empty.
	Scope string
	// Args is the operation's argv after the script; {file} and {files} are the
	// executor's placeholders.
	Args         []string
	Globs        []string
	Priority     int
	ProjectTypes []string
	// Parser, when set, declares an outputParser dispatching to this parser of
	// the module ParserModule (SeededParserModule when empty).
	Parser       string
	ParserModule string
}

// ShellTool returns the config JS that declares a tool whose app is
// `sh -c <script> <name>`, so the script sees the tool name as $0, the
// operation's arguments from $1 and the marker directory as $MARKERS. It is a
// fragment for ShellConfig, which binds the config object it mutates.
func ShellTool(name, script string, op ToolOpSpec) string {
	operation := op.Operation
	if operation == "" {
		operation = "lint"
	}
	scope := op.Scope
	if scope == "" {
		scope = "repository"
	}
	args := op.Args
	if args == nil {
		args = []string{}
	}

	opJS := map[string]any{
		"app":   name,
		"args":  args,
		"scope": scope,
		"env":   map[string]string{"MARKERS": "{root}/" + MarkerDirName},
	}
	if len(op.Globs) > 0 {
		opJS["globs"] = op.Globs
	}
	if op.Priority != 0 {
		opJS["priority"] = op.Priority
	}
	tool := map[string]any{
		"name":       name,
		"operations": map[string]any{operation: opJS},
	}
	if len(op.ProjectTypes) > 0 {
		tool["projectTypes"] = op.ProjectTypes
	}
	if op.Parser != "" {
		module := op.ParserModule
		if module == "" {
			module = SeededParserModule
		}
		tool["outputParser"] = map[string]string{"module": module, "parser": op.Parser}
	}
	app := map[string]any{"shell": map[string]any{"name": "sh", "args": []string{"-c", script, name}}}

	return "c.apps[" + jsString(name) + "] = " + jsLiteral(app) + ";\n" +
		"c.tools[" + jsString(name) + "] = " + jsLiteral(tool) + ";\n"
}

// ShellConfigSpec carries what a ShellConfig declares besides its tools.
type ShellConfigSpec struct {
	// ProjectTypes maps a project type to its marker files. A run with no
	// detected project type plans nothing, so a scenario declares at least one
	// and writes one of its markers.
	ProjectTypes map[string][]string
	// Parsers is the JS literal of the parsers field (see SeedParserModule);
	// empty declares none.
	Parsers string
	// Extra is JS run after the tools, with the config object bound to c.
	Extra string
}

// ShellConfig returns a complete, self-contained config (no inherited layers)
// declaring the given ShellTool fragments.
func ShellConfig(spec ShellConfigSpec, tools ...string) string {
	types := make(map[string]any, len(spec.ProjectTypes))
	for name, markers := range spec.ProjectTypes {
		types[name] = map[string]any{"markers": markers}
	}
	parsers := spec.Parsers
	if parsers == "" {
		parsers = "{}"
	}

	var b strings.Builder
	b.WriteString("globalThis.getBeforeConfigs = () => [];\n")
	b.WriteString("globalThis.getConfig = () => {\n")
	b.WriteString("const c = { apps: {}, runtimes: {}, managedConfigs: {}, tools: {}, projectTypes: " +
		jsLiteral(types) + ", parsers: " + parsers + " };\n")
	for _, tool := range tools {
		b.WriteString(tool)
	}
	b.WriteString(spec.Extra)
	b.WriteString("return c;\n};\n")
	b.WriteString("globalThis.getMinVersion = () => \"0.0.0\";\n")
	return b.String()
}

// jsLiteral renders v as a JS literal. JSON is one; json.Marshal also escapes
// U+2028/U+2029, the two characters where older JS and JSON disagreed.
func jsLiteral(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		panic("clitest: config fragment is not JSON-encodable: " + err.Error())
	}
	return string(out)
}

// MarkerDir creates the project's marker directory and returns its absolute
// path. The directory ignores its own content, so markers written by one run
// never enter the file set, the unit members or the cache keys of the next.
func MarkerDir(p *Project) string {
	p.tb.Helper()
	p.WriteFile(filepath.Join(MarkerDirName, ".gitignore"), "*\n")
	return filepath.Join(p.Dir, MarkerDirName)
}

// Marker reports whether tool recorded a run in the project's marker directory,
// with the lines it recorded.
func (p *Project) Marker(tool string) (bool, string) {
	p.tb.Helper()
	data, err := os.ReadFile(filepath.Join(p.Dir, MarkerDirName, tool))
	if errors.Is(err, os.ErrNotExist) {
		return false, ""
	}
	if err != nil {
		p.tb.Fatalf("clitest: read marker of %s: %v", tool, err)
	}
	return true, string(data)
}

// Markers lists the tools that recorded a run, sorted.
func (p *Project) Markers() []string {
	p.tb.Helper()
	entries, err := os.ReadDir(filepath.Join(p.Dir, MarkerDirName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		p.tb.Fatalf("clitest: list markers: %v", err)
	}
	var tools []string
	for _, entry := range entries {
		if name := entry.Name(); name != ".gitignore" {
			tools = append(tools, name)
		}
	}
	sort.Strings(tools)
	return tools
}

// RequireShell skips the test on Windows, and wherever no POSIX sh is on PATH,
// naming the property the skip leaves unverified. Windows is skipped even with
// an sh installed: the scripts and the expectations around them assume POSIX
// paths.
func RequireShell(tb testing.TB, unverified string) {
	tb.Helper()
	if runtime.GOOS == "windows" {
		tb.Skipf("clitest: shell tools are POSIX-only; skipping on Windows (leaves unverified: %s)", unverified)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		tb.Skipf("clitest: no sh on PATH; skipping (leaves unverified: %s)", unverified)
	}
}
