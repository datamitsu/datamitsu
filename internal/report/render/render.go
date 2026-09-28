// Package render turns a report.Run into the files --report asks for. Every
// format is a Renderer over the same model: none reads the run itself, so a
// document rendered offline by `report render` is the one the run would have
// written.
package render

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/json"
	"github.com/datamitsu/datamitsu/internal/report/render/markdown"
)

// Renderer writes one format.
type Renderer interface {
	// Name is the format's name in --report.
	Name() string
	// Options lists the option names the format accepts after "?".
	Options() []string
	Render(w io.Writer, run *report.Run, options map[string]string) error
	// OmitsIncompleteTools reports a format that leaves out a tool whose
	// completeness is not established instead of listing it: such a format is
	// written for a narrowed run, where every other one is refused.
	OmitsIncompleteTools() bool
}

var renderers = []Renderer{json.Renderer{}, markdown.Renderer{}}

// Lookup finds a format by the name --report spells it with.
func Lookup(format string) (Renderer, bool) {
	for _, r := range renderers {
		if r.Name() == format {
			return r, true
		}
	}
	return nil, false
}

// Names lists the formats a report can be asked for, sorted for messages.
func Names() []string {
	names := make([]string, 0, len(renderers))
	for _, r := range renderers {
		names = append(names, r.Name())
	}
	sort.Strings(names)
	return names
}

// Listing returns the formats among specs that list findings: those a run
// narrowed at plan time may not write without --allow-partial, where a format
// that omits incomplete tools is written anyway.
func Listing(specs []Spec) []string {
	var out []string
	for _, s := range specs {
		if r, ok := Lookup(s.Format); ok && !r.OmitsIncompleteTools() {
			out = append(out, s.Format)
		}
	}
	return out
}

// Stdout is the path that writes a report to standard output.
const Stdout = "-"

// Spec is one report a run is asked to write.
type Spec struct {
	Format  string
	Path    string
	Options map[string]string
}

// Stdout reports whether the spec writes to standard output.
func (s Spec) Stdout() bool { return s.Path == Stdout }

// ParseSpec reads one "<format>=<path>[?opt=value[&opt=value…]]".
func ParseSpec(raw string) (Spec, error) {
	format, rest, ok := strings.Cut(strings.TrimSpace(raw), "=")
	format = strings.TrimSpace(format)
	if !ok || format == "" {
		return Spec{}, fmt.Errorf("%q is not <format>=<path>", raw)
	}
	r, known := Lookup(format)
	if !known {
		return Spec{}, fmt.Errorf("unknown report format %q (must be %s)", format, strings.Join(Names(), ", "))
	}
	path, query, _ := strings.Cut(rest, "?")
	if path == "" {
		return Spec{}, fmt.Errorf("report %s needs a path: %s=<path>, or %s=- for stdout", format, format, format)
	}
	spec := Spec{Format: format, Path: path}
	if query == "" {
		return spec, nil
	}
	spec.Options = map[string]string{}
	for pair := range strings.SplitSeq(query, "&") {
		key, value, _ := strings.Cut(pair, "=")
		if !slices.Contains(r.Options(), key) {
			if len(r.Options()) == 0 {
				return Spec{}, fmt.Errorf("report %s takes no option, got %q", format, key)
			}
			return Spec{}, fmt.Errorf("report %s has no option %q (must be %s)", format, key, strings.Join(r.Options(), ", "))
		}
		if _, dup := spec.Options[key]; dup {
			return Spec{}, fmt.Errorf("report %s names option %q twice", format, key)
		}
		spec.Options[key] = value
	}
	return spec, nil
}

// ParseSpecs reads the reports of a run: the --report flags, and the
// comma-separated DATAMITSU_REPORT value, whose entry for a format a flag also
// names is dropped. A format named twice in one source, or two reports on
// stdout, is an error.
func ParseSpecs(flags []string, fromEnv string) ([]Spec, error) {
	fromFlags, err := parseList(flags, "--report")
	if err != nil {
		return nil, err
	}
	var envValues []string
	if strings.TrimSpace(fromEnv) != "" {
		envValues = strings.Split(fromEnv, ",")
	}
	fromEnvSpecs, err := parseList(envValues, "DATAMITSU_REPORT")
	if err != nil {
		return nil, err
	}
	specs := fromFlags
	for _, s := range fromEnvSpecs {
		if !slices.ContainsFunc(fromFlags, func(f Spec) bool { return f.Format == s.Format }) {
			specs = append(specs, s)
		}
	}
	stdout := 0
	for _, s := range specs {
		if s.Stdout() {
			stdout++
		}
	}
	if stdout > 1 {
		return nil, errors.New("only one report can be written to stdout (-)")
	}
	return specs, nil
}

func parseList(values []string, source string) ([]Spec, error) {
	var specs []Spec
	for _, raw := range values {
		spec, err := ParseSpec(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid %s value: %w", source, err)
		}
		if slices.ContainsFunc(specs, func(s Spec) bool { return s.Format == spec.Format }) {
			return nil, fmt.Errorf("invalid %s value: report %s is named twice", source, spec.Format)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}
