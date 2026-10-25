package cli_test

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/clitest"
)

// prepareConfigJS exercises the prepare surface end to end at load time:
// dependsOn edges in both forms, a prepare step, ${APP_PREPARED} in runtimeEnv
// and ${APP_BIN:<alias>} resolution through an alias.
func prepareConfigJS() string {
	return `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  version: "1.0.0",
  apps: {
    schepherd: {
      binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/schepherd", hash: "sha256:` + strings.Repeat("0", 64) + `", contentType: "application/octet-stream" } } } }, version: "0.1.0" },
      dependsOn: [{ app: "jsonschema", as: "validator" }],
      prepare: {
        args: ["--config", "", "--cache-dir", "${APP_PREPARED}", "materialize"],
        network: true,
      },
      runtimeEnv: {
        JSONSCHEMA_BIN: "${APP_BIN:validator}",
        SCHEPHERD_CACHE_DIR: "${APP_PREPARED}",
      },
    },
    jsonschema: {
      binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/jsonschema", hash: "sha256:` + strings.Repeat("1", 64) + `", contentType: "application/octet-stream" } } } }, version: "16.12.0" },
    },
  },
});
globalThis.getMinVersion = () => "0.0.0";
`
}

func TestConfigShowPrepare(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("prepare.config.js", prepareConfigJS())
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("`config show` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	clitest.AssertGolden(t, "config_show_prepare", norm.Apply(res.Stdout))

	if !strings.Contains(res.Stdout, `"prepare"`) {
		t.Errorf("config show does not carry the prepare step:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, `"as": "validator"`) {
		t.Errorf("config show does not carry the dependsOn alias:\n%s", res.Stdout)
	}
}

func TestConfigShowPrepareValidation(t *testing.T) {
	cases := []struct {
		name    string
		appJS   string
		wantErr string
	}{
		{
			name:    "shell app",
			appJS:   `shellprep: { shell: { name: "echo" }, prepare: { args: [], network: false } },`,
			wantErr: "prepare is not supported on shell apps",
		},
		{
			name:    "network missing",
			appJS:   `nopin: { binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/x", hash: "sha256:` + strings.Repeat("2", 64) + `", contentType: "application/octet-stream" } } } } }, prepare: { args: [] } },`,
			wantErr: "network: required",
		},
		{
			name:    "args missing",
			appJS:   `noargs: { binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/x", hash: "sha256:` + strings.Repeat("3", 64) + `", contentType: "application/octet-stream" } } } } }, prepare: { network: false } },`,
			wantErr: "args: required",
		},
		{
			name:    "store placeholder",
			appJS:   `storeph: { binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/x", hash: "sha256:` + strings.Repeat("4", 64) + `", contentType: "application/octet-stream" } } } } }, prepare: { args: ["${STORE}/x"], network: false } },`,
			wantErr: "unknown placeholder ${STORE}",
		},
		{
			name:    "prepared without prepare in runtimeEnv",
			appJS:   `orphan: { binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/x", hash: "sha256:` + strings.Repeat("5", 64) + `", contentType: "application/octet-stream" } } } } }, runtimeEnv: { CACHE: "${APP_PREPARED}" } },`,
			wantErr: "${APP_PREPARED} requires apps.orphan.prepare",
		},
		{
			name:    "prepared in env",
			appJS:   `badenv: { binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/x", hash: "sha256:` + strings.Repeat("6", 64) + `", contentType: "application/octet-stream" } } } } }, env: { CACHE: "${APP_PREPARED}" } },`,
			wantErr: "${APP_PREPARED} is not allowed in env",
		},
		{
			name: "alias collision",
			appJS: `collide: { binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/x", hash: "sha256:` + strings.Repeat("7", 64) + `", contentType: "application/octet-stream" } } } } }, dependsOn: [{ app: "a", as: "dup" }, { app: "b", as: "dup" }] },
        a: { binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/a", hash: "sha256:` + strings.Repeat("8", 64) + `", contentType: "application/octet-stream" } } } } } },
        b: { binary: { binaries: { linux: { amd64: { glibc: { url: "https://example.invalid/b", hash: "sha256:` + strings.Repeat("9", 64) + `", contentType: "application/octet-stream" } } } } } },`,
			wantErr: "already used",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := p.WriteFile("bad.config.js", "globalThis.getBeforeConfigs = () => [];\nglobalThis.getConfig = () => ({ version: \"1.0.0\", apps: { "+tt.appJS+" } });\nglobalThis.getMinVersion = () => \"0.0.0\";\n")
			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
				"--no-auto-config", "--config", cfg, "config", "show")
			if res.ExitCode == 0 {
				t.Fatalf("`config show` exit = 0, want failure\nstdout:\n%s", res.Stdout)
			}
			if !strings.Contains(res.Stderr, tt.wantErr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantErr, res.Stderr)
			}
		})
	}
}

func TestConfigShowDependsOnLegacyStrings(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("legacy.config.js", "globalThis.getBeforeConfigs = () => [];\nglobalThis.getConfig = () => ({ version: \"1.0.0\", apps: {\n"+
		"  proxy: { shell: { name: \"echo\" }, dependsOn: [\"upstream\"], runtimeEnv: { UP: \"${APP_BIN:upstream}\" } },\n"+
		"  upstream: { binary: { binaries: { linux: { amd64: { glibc: { url: \"https://example.invalid/u\", hash: \"sha256:"+strings.Repeat("a", 64)+"\", contentType: \"application/octet-stream\" } } } } } },\n"+
		"} });\nglobalThis.getMinVersion = () => \"0.0.0\";\n")

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("legacy string dependsOn rejected:\nstderr:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stdout, `"app": "upstream"`) {
		t.Errorf("legacy edge did not normalize to the object form:\n%s", res.Stdout)
	}
}

// goSourceConfigJS declares a Go app that ships its module source as an inline
// archive: the PR-4 form (previously rejected by validation). The inline
// payload is a real brotli tar (validation decodes it); the lockFile stays an
// opaque string here — config show never interprets it.
func goSourceConfigJS(t *testing.T) string {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	body := "package main\n\nfunc main() {}\n"
	if err := tw.WriteHeader(&tar.Header{Name: "main.go", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	inline, err := binmanager.CompressArchive(tarBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	return `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  version: "1.0.0",
  runtimes: {
    go: { kind: "go", mode: "system", system: { command: "/usr/local/go/bin/go", systemVersion: "1.22.0" } },
  },
  apps: {
    "my-gen": {
      go: {
        packageName: "example.com/mygen/cmd/mygen",
        version: "v0.1.0",
        lockFile: "br:` + strings.Repeat("2", 48) + `",
      },
      archives: { main: { inline: "` + inline + `" } },
    },
  },
});
globalThis.getMinVersion = () => "0.0.0";
`
}

// TestConfigShowGoSourceArchive freezes `config show` for a Go app carrying
// an inline source archive: the archives map survives load and validation
// accepts the app (the pre-PR-4 form failed with "files/links/archives are
// only supported on bun, uv, and node apps").
func TestConfigShowGoSourceArchive(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("gosource.config.js", goSourceConfigJS(t))
	norm := clitest.NewNormalizer()

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir},
		"--no-auto-config", "--config", cfg, "config", "show")
	if res.ExitCode != 0 {
		t.Fatalf("`config show` exit = %d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	clitest.AssertGolden(t, "config_show_go_source_archive", norm.Apply(res.Stdout))

	if !strings.Contains(res.Stdout, `"archives"`) {
		t.Errorf("config show does not carry the source archive:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, `"packageName": "example.com/mygen/cmd/mygen"`) {
		t.Errorf("config show does not carry the Go app:\n%s", res.Stdout)
	}
}
