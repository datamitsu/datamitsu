package report

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/datamitsu/datamitsu/internal/tooling"
)

// Masked is what a secret value becomes in a report.
const Masked = "***"

// minSecretLength keeps short values out of masking: replacing "1" or "true"
// wherever it occurs would mask the report, not the secret.
const minSecretLength = 8

// SecretValues returns the values worth masking: those of the variables among
// environ ("NAME=value" entries) and envs whose names look like they hold a
// secret — containing TOKEN, SECRET, PASSWORD or CREDENTIAL, or ending in
// _KEY, in any letter case — and that are at least eight characters long.
// Sorted longest first, so a secret that contains another is replaced whole.
func SecretValues(environ []string, envs ...map[string]string) []string {
	var s Secrets
	s.Add(environ, envs...)
	return s.Values()
}

// Secrets collects the values to mask as a run reveals them: the host's
// environment and the configuration up front, then the environment of every
// tool process as it is built, where placeholders have their values. It is
// safe for concurrent use; the zero value is empty.
type Secrets struct {
	mu     sync.Mutex
	values map[string]bool
}

// Add keeps the secret-looking values of environ and envs (see SecretValues).
func (s *Secrets) Add(environ []string, envs ...map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]bool{}
	}
	add := func(name, value string) {
		if utf8.RuneCountInString(value) >= minSecretLength && secretName(name) {
			s.values[value] = true
		}
	}
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok {
			add(name, value)
		}
	}
	for _, env := range envs {
		for name, value := range env {
			add(name, value)
		}
	}
}

// Values returns what was collected, longest first.
func (s *Secrets) Values() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	out := make([]string, 0, len(s.values))
	for v := range s.values {
		out = append(out, v)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

func secretName(name string) bool {
	upper := strings.ToUpper(name)
	for _, word := range []string{"TOKEN", "SECRET", "PASSWORD", "CREDENTIAL"} {
		if strings.Contains(upper, word) {
			return true
		}
	}
	return strings.HasSuffix(upper, "_KEY")
}

// Mask replaces every occurrence of each secret with Masked in every string
// the run holds, in one pass over the whole document, so that no field — one
// added later included — can carry a value it was not checked for. Masking is
// best effort: it catches what the environment names, not a secret a tool
// prints that no variable ever held.
func Mask(run *Run, secrets []string) {
	if run == nil || len(secrets) == 0 {
		return
	}
	replace := replacer(secrets).Replace
	run.RedactedReasons = recordRedactedReasons(run.Incomplete, run.RedactedReasons, replace)
	for i := range run.Operations {
		for j := range run.Operations[i].Tools {
			tr := &run.Operations[i].Tools[j]
			tr.RedactedReasons = recordRedactedReasons(tr.Incomplete, tr.RedactedReasons, replace)
			for k := range tr.Invocations {
				inv := &tr.Invocations[k]
				if replace(inv.State) != inv.State {
					inv.RedactedState = labelCode(inv.State, executionLabels[:])
				}
				if replace(inv.Extraction) != inv.Extraction {
					inv.RedactedExtraction = labelCode(inv.Extraction, extractionLabels[:])
				}
				for k := range inv.Files {
					fr := &inv.Files[k]
					if replace(fr.State) != fr.State {
						fr.RedactedState = labelCode(fr.State, executionLabels[:])
					}
				}
			}
		}
	}
	RewriteAll(run, replace)
}

// These indices are wire codes. Keep existing entries in order.
var executionLabels = [...]string{
	"", string(tooling.ProcessRan), string(tooling.FileCached), string(tooling.FileVerdictHit),
	string(tooling.ProcessCancelled), string(tooling.ProcessNotStarted), string(tooling.ProcessSetupFailed),
}

var extractionLabels = [...]string{
	"", string(tooling.ExtractionParsedClean), string(tooling.ExtractionParsedFindings),
	string(tooling.ExtractionParserUnavailable), string(tooling.ExtractionParseFailed),
	string(tooling.ExtractionTruncated), string(tooling.ExtractionNone),
}

// Indices are stable wire codes, as for executionLabels and extractionLabels.
var reasonLabels = [...]string{
	"", string(ReasonNarrowedSelection), string(ReasonPartialUnit), string(ReasonCancelled),
	string(ReasonNotStarted), string(ReasonSetupFailed), string(ReasonPlatformSkip),
	string(ReasonNoExtraction), string(ReasonParserUnavailable), string(ReasonParseFailed),
	string(ReasonTruncated), string(ReasonUnparsedCacheHit), string(ReasonFailedWithoutFindings),
	string(ReasonToolsFilter), string(ReasonNotNarrowable), string(ReasonOperationSkipped),
	string(ReasonMissingCompleteness),
}

func recordRedactedReasons(reasons []Reason, codes []uint16, replace func(string) string) []uint16 {
	for i, reason := range reasons {
		if label := string(reason); replace(label) != label {
			if code := labelCode(label, reasonLabels[:]); code != 0 {
				if i >= len(codes) {
					codes = append(codes, make([]uint16, i+1-len(codes))...)
				}
				codes[i] = uint16(code)
			}
		}
	}
	return codes
}

func effectiveReason(label Reason, codes []uint16, i int) Reason {
	if i < len(codes) && int(codes[i]) < len(reasonLabels) {
		return Reason(effectiveLabel(string(label), uint8(codes[i]), reasonLabels[:]))
	}
	return label
}

func labelCode(label string, labels []string) uint8 {
	for i := 1; i < len(labels); i++ {
		if labels[i] == label {
			return uint8(i)
		}
	}
	return 0
}

func effectiveLabel(label string, code uint8, labels []string) string {
	if code != 0 && int(code) < len(labels) && strings.Contains(label, Masked) {
		return labels[code]
	}
	return label
}

// ExecutionState is the state for decisions. Display and serialization use State.
func (inv Invocation) ExecutionState() string {
	return effectiveLabel(inv.State, inv.RedactedState, executionLabels[:])
}

// ExecutionState is the file's state for decisions, not for display.
func (fr FileResult) ExecutionState() string {
	return effectiveLabel(fr.State, fr.RedactedState, executionLabels[:])
}

// ExtractionState is the extraction result for decisions, not for display.
func (inv Invocation) ExtractionState() tooling.Extraction {
	return tooling.Extraction(effectiveLabel(inv.Extraction, inv.RedactedExtraction, extractionLabels[:]))
}

// MaskAll masks every exported string that ptr, a pointer, reaches — a Run,
// an event — as Mask does.
func MaskAll(ptr any, secrets []string) {
	v := reflect.ValueOf(ptr)
	if len(secrets) == 0 || v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	rewriteValue(v.Elem(), replacer(secrets).Replace)
}

// RewriteAll passes every exported string ptr points to, however deeply
// nested, through rewrite.
func RewriteAll(ptr any, rewrite func(string) string) {
	v := reflect.ValueOf(ptr)
	if rewrite == nil || v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	rewriteValue(v.Elem(), rewrite)
}

// replacer masks each secret as it is, and as JSON spells it inside a string:
// a log line routed to the stream carries its fields JSON-encoded, where a
// quote or a backslash in a secret is escaped.
func replacer(secrets []string) *strings.Replacer {
	spellings := make([]string, 0, 3*len(secrets))
	for _, s := range secrets {
		if s == "" {
			continue
		}
		spellings = append(spellings, s)
		if escaped := jsonSpelling(s); escaped != s {
			spellings = append(spellings, escaped)
		}
		quoted := strconv.Quote(s)
		if escaped := quoted[1 : len(quoted)-1]; escaped != s {
			spellings = append(spellings, escaped)
		}
	}
	sort.Slice(spellings, func(i, j int) bool { return len(spellings[i]) > len(spellings[j]) })
	pairs := make([]string, 0, 2*len(spellings))
	for _, spelling := range spellings {
		pairs = append(pairs, spelling, Masked)
	}
	return strings.NewReplacer(pairs...)
}

func jsonSpelling(s string) string {
	quoted, err := json.Marshal(s)
	if err != nil {
		return s
	}
	return string(quoted[1 : len(quoted)-1])
}

// withoutFragment masks text, which a cut at a byte count started, and drops
// what it begins with that could be the end of a secret the cut went
// through: a part of a secret is not a secret masking can recognize.
func withoutFragment(text string, secrets []string) string {
	if len(secrets) == 0 {
		return text
	}
	text = replacer(secrets).Replace(text)
	drop := 0
	for _, s := range secrets {
		for k := min(len(s)-1, len(text)); k > drop; k-- {
			if strings.HasPrefix(text, s[len(s)-k:]) {
				drop = k
				break
			}
		}
	}
	return text[drop:]
}

var timeType = reflect.TypeFor[time.Time]()

func rewriteValue(v reflect.Value, r func(string) string) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(r(v.String()))
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			rewriteValue(v.Elem(), r)
		}
	case reflect.Struct:
		if v.Type() == timeType {
			return
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				rewriteValue(v.Field(i), r)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			rewriteValue(v.Index(i), r)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(v.MapIndex(key))
			rewriteValue(value, r)
			v.SetMapIndex(key, value)
		}
	case reflect.Invalid, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
		reflect.Chan, reflect.Func, reflect.UnsafePointer:
	}
}
