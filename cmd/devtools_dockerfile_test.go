package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeResolver struct {
	digest string
	err    error
	calls  int
}

func (f *fakeResolver) ResolveCached(_ context.Context, _, _ string) (string, error) {
	f.calls++
	return f.digest, f.err
}

func TestResolveBaseDigest(t *testing.T) {
	ctx := context.Background()

	t.Run("offline skips resolution", func(t *testing.T) {
		r := &fakeResolver{digest: "sha256:x"}
		digest, reason := resolveBaseDigest(ctx, r, true, "0.0.19", "datamitsu/datamitsu", "0.0.19")
		if digest != "" {
			t.Errorf("digest = %q, want empty when offline", digest)
		}
		if r.calls != 0 {
			t.Errorf("resolver called %d times in offline mode, want 0", r.calls)
		}
		if !strings.Contains(reason, "offline") {
			t.Errorf("reason = %q, want mention of offline", reason)
		}
	})

	t.Run("non-release version is not pinned", func(t *testing.T) {
		r := &fakeResolver{digest: "sha256:x"}
		digest, reason := resolveBaseDigest(ctx, r, false, "dev", "datamitsu/datamitsu", "dev")
		if digest != "" || r.calls != 0 {
			t.Errorf("dev version should not resolve: digest=%q calls=%d", digest, r.calls)
		}
		if !strings.Contains(reason, "not a release tag") {
			t.Errorf("reason = %q", reason)
		}
	})

	t.Run("resolver error degrades to unpinned", func(t *testing.T) {
		r := &fakeResolver{err: errors.New("network down")}
		digest, reason := resolveBaseDigest(ctx, r, false, "0.0.19", "datamitsu/datamitsu", "0.0.19")
		if digest != "" {
			t.Errorf("digest = %q, want empty on error", digest)
		}
		if !strings.Contains(reason, "network down") {
			t.Errorf("reason = %q, want underlying error", reason)
		}
	})

	t.Run("success returns digest", func(t *testing.T) {
		r := &fakeResolver{digest: "sha256:abc"}
		digest, reason := resolveBaseDigest(ctx, r, false, "0.0.19", "datamitsu/datamitsu", "0.0.19")
		if digest != "sha256:abc" {
			t.Errorf("digest = %q, want sha256:abc", digest)
		}
		if reason != "" {
			t.Errorf("reason = %q, want empty on success", reason)
		}
	})
}

func TestIsPinnableVersion(t *testing.T) {
	cases := map[string]bool{
		"0.0.19":             true,
		"v1.2.3":             true,
		"1.2.3":              true,
		"dev":                false,
		"":                   false,
		"0.0.0-unstable.abc": false,
		"v2.0.0-rc.1":        false,
	}
	for version, want := range cases {
		if got := isPinnableVersion(version); got != want {
			t.Errorf("isPinnableVersion(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestResolveImageRepo(t *testing.T) {
	if got := resolveImageRepo("me/fork", "datamitsu/datamitsu-unstable"); got != "me/fork" {
		t.Errorf("--repo override = %q, want me/fork", got)
	}
	if got := resolveImageRepo("", "datamitsu/datamitsu-unstable"); got != "datamitsu/datamitsu-unstable" {
		t.Errorf("ldflags repo = %q, want datamitsu/datamitsu-unstable", got)
	}
	if got := resolveImageRepo("", ""); got != defaultBaseRepo {
		t.Errorf("local-build default = %q, want %q", got, defaultBaseRepo)
	}
}

func TestResolveImageTag(t *testing.T) {
	// Unstable: the baked tag_name differs from the version string.
	if got := resolveImageTag("unstable-20260607-abc", "0.0.0-unstable.20260607.abc", false); got != "unstable-20260607-abc" {
		t.Errorf("baked tag = %q, want unstable-20260607-abc", got)
	}
	// Stable + alpine.
	if got := resolveImageTag("0.0.19", "0.0.19", true); got != "0.0.19-alpine" {
		t.Errorf("alpine tag = %q, want 0.0.19-alpine", got)
	}
	// Local build: empty ImageTag falls back to the version.
	if got := resolveImageTag("", "dev", false); got != "dev" {
		t.Errorf("local fallback = %q, want dev", got)
	}
	if got := resolveImageTag("", "dev", true); got != "dev-alpine" {
		t.Errorf("local alpine fallback = %q, want dev-alpine", got)
	}
}

func TestParseLabels(t *testing.T) {
	got, err := parseLabels([]string{"a=1", "b=two=2"})
	if err != nil {
		t.Fatalf("parseLabels: %v", err)
	}
	if got["a"] != "1" || got["b"] != "two=2" {
		t.Errorf("parseLabels = %v", got)
	}

	if _, err := parseLabels([]string{"noequals"}); err == nil {
		t.Error("expected error for label without '='")
	}
	if _, err := parseLabels([]string{"=v"}); err == nil {
		t.Error("expected error for empty key")
	}
}

func TestParseEnv(t *testing.T) {
	got, err := parseEnv([]string{"TZ=UTC", "FOO=bar=baz"})
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if got["TZ"] != "UTC" || got["FOO"] != "bar=baz" {
		t.Errorf("parseEnv = %v", got)
	}

	if _, err := parseEnv([]string{"noequals"}); err == nil {
		t.Error("expected error for env without '='")
	}
	if _, err := parseEnv([]string{"=v"}); err == nil {
		t.Error("expected error for empty key")
	}
}

func TestParseArgs(t *testing.T) {
	got, err := parseArgs("arg", []string{"BUILD_ID", "TZ=UTC", "EXPR=a=b"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got["BUILD_ID"] != "" || got["TZ"] != "UTC" || got["EXPR"] != "a=b" {
		t.Errorf("parseArgs = %v", got)
	}

	_, err = parseArgs("build-arg", []string{"=v"})
	if err == nil {
		t.Fatal("expected error for empty arg name")
	}
	if !strings.Contains(err.Error(), "--build-arg") {
		t.Errorf("error should name the flag: %v", err)
	}
}

func TestWriteFileAtomic_OverwritesFully(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker", "Dockerfile")

	if err := writeFileAtomic(path, []byte("first content\n")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	if err := writeFileAtomic(path, []byte("second\n")); err != nil {
		t.Fatalf("writeFileAtomic overwrite: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "second\n" {
		t.Errorf("content = %q, want full overwrite to 'second\\n'", string(data))
	}

	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
