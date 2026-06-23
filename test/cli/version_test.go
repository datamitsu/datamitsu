package cli_test

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// TestVersionGolden freezes the `version` output contract:
// "<PackageName> version <VERSION>" on a single line, exit 0, nothing on
// stderr. The build version is masked by the normalizer so the golden is stable
// across local ("dev") and release builds.
func TestVersionGolden(t *testing.T) {
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{}, "version")
	if res.ExitCode != 0 {
		t.Fatalf("`version` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.Stderr != "" {
		t.Errorf("`version` wrote to stderr:\n%s", res.Stderr)
	}
	clitest.AssertGolden(t, "version", norm.Apply(res.Stdout))
}

// TestVersionIgnoresExtraArgs proves `version` accepts (and ignores) trailing
// positional args and the persistent --verbose flag without altering its
// output, so wrappers can pass extra tokens safely.
func TestVersionIgnoresExtraArgs(t *testing.T) {
	norm := clitest.NewNormalizer()

	base := clitest.Run(t, clitest.RunOptions{}, "version")
	if base.ExitCode != 0 {
		t.Fatalf("`version` exit = %d, want 0\nstderr:\n%s", base.ExitCode, base.Stderr)
	}
	want := norm.Apply(base.Stdout)

	cases := []struct {
		name string
		args []string
	}{
		{"extra-arg", []string{"version", "extra"}},
		{"extra-args", []string{"version", "foo", "bar"}},
		{"verbose-long", []string{"--verbose", "version"}},
		{"verbose-short", []string{"-v", "version"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clitest.Run(t, clitest.RunOptions{}, tc.args...)
			if res.ExitCode != 0 {
				t.Fatalf("`%s` exit = %d, want 0\nstderr:\n%s",
					strings.Join(tc.args, " "), res.ExitCode, res.Stderr)
			}
			if got := norm.Apply(res.Stdout); got != want {
				t.Errorf("`%s` output differs from bare `version`:\n got: %q\nwant: %q",
					strings.Join(tc.args, " "), got, want)
			}
		})
	}
}
