package report

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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
	MaskAll(run, secrets)
}

// MaskAll masks every exported string that ptr, a pointer, reaches — a Run,
// an event — as Mask does.
func MaskAll(ptr any, secrets []string) {
	v := reflect.ValueOf(ptr)
	if len(secrets) == 0 || v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	maskValue(v.Elem(), replacer(secrets))
}

func replacer(secrets []string) *strings.Replacer {
	pairs := make([]string, 0, 2*len(secrets))
	for _, s := range secrets {
		if s != "" {
			pairs = append(pairs, s, Masked)
		}
	}
	return strings.NewReplacer(pairs...)
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

func maskValue(v reflect.Value, r *strings.Replacer) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(r.Replace(v.String()))
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			maskValue(v.Elem(), r)
		}
	case reflect.Struct:
		if v.Type() == timeType {
			return
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				maskValue(v.Field(i), r)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			maskValue(v.Index(i), r)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(v.MapIndex(key))
			maskValue(value, r)
			v.SetMapIndex(key, value)
		}
	case reflect.Invalid, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
		reflect.Chan, reflect.Func, reflect.UnsafePointer:
	}
}
