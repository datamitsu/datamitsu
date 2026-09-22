package binmanager

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/syslist"
)

func binaryApp(urls ...string) App {
	binaries := MapOfBinaries{}
	for index, url := range urls {
		osType := syslist.OsType("linux")
		if index%2 == 1 {
			osType = syslist.OsType("darwin")
		}
		if binaries[osType] == nil {
			binaries[osType] = map[syslist.ArchType]map[string]BinaryOsArchInfo{}
		}
		arch := syslist.ArchType("amd64")
		if index > 1 {
			arch = syslist.ArchType("arm64")
		}
		binaries[osType][arch] = map[string]BinaryOsArchInfo{"unknown": {URL: url}}
	}
	return App{Binary: &AppConfigBinary{Binaries: binaries}}
}

func TestDeriveOfficialURLPerRule(t *testing.T) {
	for _, tc := range []struct {
		app  App
		name string
		want string
	}{
		{
			app:  binaryApp("https://github.com/koalaman/shellcheck/releases/download/v0.10.0/shellcheck-v0.10.0.linux.x86_64.tar.xz"),
			name: "github release",
			want: "https://github.com/koalaman/shellcheck",
		},
		{
			app:  binaryApp("https://codeberg.org/dnkl/foot/releases/download/1.19.0/foot-1.19.0.tar.gz"),
			name: "codeberg release",
			want: "https://codeberg.org/dnkl/foot",
		},
		{
			app:  binaryApp("https://gitlab.com/gitlab-org/cli/-/releases/v1.44.0/downloads/glab_1.44.0_linux_amd64.tar.gz"),
			name: "gitlab release",
			want: "https://gitlab.com/gitlab-org/cli",
		},
		{
			app:  binaryApp("https://gitlab.com/group/subgroup/tool/-/releases/v1/downloads/tool.tar.gz"),
			name: "gitlab nested namespace",
			want: "https://gitlab.com/group/subgroup/tool",
		},
		{
			// Two platforms, one repository: the app has one home page.
			app: binaryApp(
				"https://github.com/mvdan/sh/releases/download/v3.8.0/shfmt_v3.8.0_linux_amd64",
				"https://github.com/mvdan/sh/releases/download/v3.8.0/shfmt_v3.8.0_darwin_amd64",
			),
			name: "every platform from the same repository",
			want: "https://github.com/mvdan/sh",
		},
		{
			// Two platforms, two repositories: no single answer, so none.
			app: binaryApp(
				"https://github.com/mvdan/sh/releases/download/v3.8.0/shfmt_linux",
				"https://github.com/someone/fork/releases/download/v3.8.0/shfmt_darwin",
			),
			name: "platforms disagree",
		},
		{
			app:  binaryApp("https://downloads.example.com/tool/1.0/tool-linux.tar.gz"),
			name: "unknown host",
		},
		{
			app:  binaryApp("https://github.com/owner/repo/archive/refs/tags/v1.tar.gz"),
			name: "github non-release path",
		},
		{
			app:  App{Node: &AppConfigNode{PackageName: "eslint", Version: "9.0.0"}},
			name: "node package",
			want: "https://www.npmjs.com/package/eslint",
		},
		{
			app:  App{Bun: &AppConfigBun{PackageName: "@biomejs/biome", Version: "1.0.0"}},
			name: "bun package",
			want: "https://www.npmjs.com/package/@biomejs/biome",
		},
		{
			app:  App{Uv: &AppConfigUV{PackageName: "ruff", Version: "0.6.0"}},
			name: "uv package",
			want: "https://pypi.org/project/ruff/",
		},
		{
			app:  App{Go: &AppConfigGo{PackageName: "golang.org/x/vuln/cmd/govulncheck", Version: "v1.1.3"}},
			name: "go module path",
			want: "https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck",
		},
		{
			app:  App{Go: &AppConfigGo{PackageName: "github.com/mgechev/revive@v1.3.7", Version: "v1.3.7"}},
			name: "go module path with a version",
			want: "https://pkg.go.dev/github.com/mgechev/revive",
		},
		{
			app:  App{Go: &AppConfigGo{PackageName: "tool", Version: "v1"}},
			name: "go path that is not a module path",
		},
		{
			app:  App{Jvm: &AppConfigJVM{JarURL: "https://repo1.maven.org/maven2/com/pinterest/ktlint/ktlint-cli/1.3.1/ktlint-cli-1.3.1-all.jar"}},
			name: "maven central jar",
			want: "https://central.sonatype.com/artifact/com.pinterest.ktlint/ktlint-cli",
		},
		{
			app:  App{Jvm: &AppConfigJVM{JarURL: "https://github.com/facebook/ktfmt/releases/download/v0.51/ktfmt-0.51-jar-with-dependencies.jar"}},
			name: "forge-hosted jar",
			want: "https://github.com/facebook/ktfmt",
		},
		{
			app:  App{Jvm: &AppConfigJVM{JarURL: "https://example.com/artifacts/tool.jar"}},
			name: "jar from an unknown host",
		},
		{
			app:  App{Shell: &AppConfigShell{Name: "git"}},
			name: "shell app",
		},
		{
			app:  App{},
			name: "app with no kind",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveOfficialURL(tc.app); got != tc.want {
				t.Fatalf("DeriveOfficialURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
