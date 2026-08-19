package shellquote

import (
	"os/exec"
	"strings"
	"testing"
)

// vectors is the hostile-input corpus shared by the table tests, the invariant
// tests and the real-shell oracles. Every entry is something a filesystem path
// or an app name can legally contain.
var vectors = []struct {
	name string
	in   string
}{
	{"empty", ""},
	{"plain", "/tmp/datamitsu/bin"},
	{"space", "/tmp/my project/bin"},
	{"newline", "a\nb"},
	{"carriage_return", "a\rb"},
	{"tab", "a\tb"},
	{"command_substitution", "$(id)"},
	{"backtick", "`id`"},
	{"single_quote", "it's"},
	{"double_quote", `say "hi"`},
	{"backslash", `a\b`},
	{"bang", "a!b"},
	{"leading_dash", "-rf"},
	{"glob", "a[b]c*?"},
	{"dollar_brace", "${HOME}"},
	{"semicolon", "a;rm -rf /"},
	{"tilde", "~/bin"},
	{"non_utf8", "a\xff\xfeb"},
	{"utf8", "café/日本語"},
	{"del", "a\x7fb"},
	{"hex_after_escape", "\x01ab"},
	{"all_control_bytes", "\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f"},
}

// TestBashExactLiterals and TestFishExactLiterals pin the emitted bytes rather
// than the literal's shape.
//
// The round-trip tests below are the real oracle, but they skip when the shell
// is absent — and fish is absent on most machines and in CI, so without exact
// expectations a regression in Fish (an \x escape where \X is required, a
// dropped closing quote) ships with the suite green. These cases hold the two
// renderers to a fixed encoding with no shell installed.
func TestBashExactLiterals(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", "''"},
		{"plain", "/tmp/bin", `$'/tmp/bin'`},
		{"space", "/tmp/my project", `$'/tmp/my project'`},
		{"single_quote", "it's", `$'it\'s'`},
		{"backslash", `a\b`, `$'a\\b'`},
		{"newline", "a\nb", `$'a\nb'`},
		{"carriage_return", "a\rb", `$'a\rb'`},
		{"tab", "a\tb", `$'a\tb'`},
		{"command_substitution", "$(id)", `$'$(id)'`},
		{"glob", "a[b]c*?", `$'a[b]c*?'`},
		{"non_utf8", "a\xffb", `$'a\xffb'`},
		{"hex_after_escape", "\x01ab", `$'\x01ab'`},
		{"del", "a\x7fb", `$'a\x7fb'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Bash(tc.in); got != tc.want {
				t.Fatalf("Bash(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFishExactLiterals(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", "''"},
		{"plain", "/tmp/bin", `'/tmp/bin'`},
		{"space", "/tmp/my project", `'/tmp/my project'`},
		{"single_quote", "it's", `'it\'s'`},
		{"backslash", `a\b`, `'a\\b'`},
		// A control byte closes the quoted run, is written as a raw-byte \X
		// escape, and the next printable byte opens a new run; fish joins
		// adjacent tokens into one word.
		{"newline", "a\nb", `'a'\X0a'b'`},
		{"carriage_return", "a\rb", `'a'\X0d'b'`},
		{"tab", "a\tb", `'a'\X09'b'`},
		{"command_substitution", "$(id)", `'$(id)'`},
		{"glob", "a[b]c*?", `'a[b]c*?'`},
		{"non_utf8", "a\xffb", `'a'\Xff'b'`},
		{"leading_control", "\x01ab", `\X01'ab'`},
		{"trailing_control", "ab\x01", `'ab'\X01`},
		{"del", "a\x7fb", `'a'\X7f'b'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Fish(tc.in); got != tc.want {
				t.Fatalf("Fish(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestLiteralShape holds the structural invariants over the whole hostile
// corpus, which the exact-literal cases above only sample.
func TestLiteralShape(t *testing.T) {
	for _, tc := range vectors {
		t.Run(tc.name, func(t *testing.T) {
			bash := Bash(tc.in)
			if tc.in == "" {
				if bash != "''" {
					t.Fatalf("Bash(%q) = %q, want ''", tc.in, bash)
				}
			} else if !strings.HasPrefix(bash, "$'") {
				t.Fatalf("Bash(%q) = %q, want an ANSI-C literal", tc.in, bash)
			}
			if !strings.HasSuffix(bash, "'") {
				t.Fatalf("Bash(%q) = %q, want a trailing quote", tc.in, bash)
			}
			if fish := Fish(tc.in); fish == "" {
				t.Fatalf("Fish(%q) returned an empty literal", tc.in)
			}
		})
	}
}

// TestOutputIsSingleLineASCII pins the two invariants the rest of source mode
// relies on: emitted shell code stays line-oriented, and it survives any
// locale.
func TestOutputIsSingleLineASCII(t *testing.T) {
	for _, tc := range vectors {
		t.Run(tc.name, func(t *testing.T) {
			for fn, got := range map[string]string{"Bash": Bash(tc.in), "Fish": Fish(tc.in)} {
				if strings.ContainsAny(got, "\n\r") {
					t.Errorf("%s(%q) = %q contains a raw newline", fn, tc.in, got)
				}
				for i := range len(got) {
					if c := got[i]; c < 0x20 || c >= 0x7f {
						t.Errorf("%s(%q) = %q contains non-printable-ASCII byte %#x at %d", fn, tc.in, got, c, i)
						break
					}
				}
			}
		})
	}
}

func TestNULPanics(t *testing.T) {
	for name, fn := range map[string]func(string) string{"Bash": Bash, "Fish": Fish} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("%s did not panic on a NUL byte", name)
				}
			}()
			_ = fn("a\x00b")
		})
	}
}

func TestFishPathList(t *testing.T) {
	tests := []struct {
		name string
		dirs []string
		want string
	}{
		{"empty", nil, ""},
		{"single", []string{"/a/b"}, "'/a/b'"},
		{"multiple", []string{"/a", "/b"}, "'/a' '/b'"},
		{"space_and_quote", []string{"/a b", "/it's"}, `'/a b' '/it\'s'`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FishPathList(tc.dirs); got != tc.want {
				t.Fatalf("FishPathList(%q) = %q, want %q", tc.dirs, got, tc.want)
			}
		})
	}
}

// roundTrip asks the shell itself whether the literal parses back to the input
// byte-exactly. This is the only check that matters; everything else is a
// proxy for it.
func roundTrip(t *testing.T, shellPath string, literal, want string) {
	t.Helper()
	out, err := exec.Command(shellPath, "-c", "printf %s "+literal).Output()
	if err != nil {
		t.Fatalf("%s -c %q: %v", shellPath, "printf %s "+literal, err)
	}
	if string(out) != want {
		t.Fatalf("%s round-trip of %q via %s = %q, want %q", shellPath, want, literal, string(out), want)
	}
}

// lookShell finds a shell binary or skips the test, stating that the property
// is unverified on this machine.
func lookShell(t *testing.T, names ...string) string {
	t.Helper()
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	t.Skipf("none of %v is installed: shell round-trip property is unverified on this machine", names)
	return ""
}

func TestBashRoundTrip(t *testing.T) {
	// bash first: /bin/sh is dash on many Linux distributions and older dash
	// has no ANSI-C quoting, which would fail the oracle for the wrong reason.
	sh := lookShell(t, "bash", "zsh", "/bin/sh")
	for _, tc := range vectors {
		t.Run(tc.name, func(t *testing.T) {
			roundTrip(t, sh, Bash(tc.in), tc.in)
		})
	}
}

func TestFishRoundTrip(t *testing.T) {
	sh := lookShell(t, "fish")
	for _, tc := range vectors {
		t.Run(tc.name, func(t *testing.T) {
			roundTrip(t, sh, Fish(tc.in), tc.in)
		})
	}
}

func FuzzBash(f *testing.F) {
	for _, tc := range vectors {
		f.Add(tc.in)
	}
	sh, err := exec.LookPath("bash")
	if err != nil {
		f.Skip("bash is not installed: the fuzz round-trip property is unverified on this machine")
	}
	f.Fuzz(func(t *testing.T, in string) {
		if strings.IndexByte(in, 0) >= 0 {
			t.Skip("NUL cannot survive an argument vector")
		}
		got := Bash(in)
		if strings.ContainsAny(got, "\n\r") {
			t.Fatalf("Bash(%q) = %q contains a raw newline", in, got)
		}
		out, err := exec.Command(sh, "-c", "printf %s "+got).Output()
		if err != nil {
			t.Fatalf("bash -c %q: %v", "printf %s "+got, err)
		}
		if string(out) != in {
			t.Fatalf("round-trip of %q via %q = %q", in, got, string(out))
		}
	})
}
