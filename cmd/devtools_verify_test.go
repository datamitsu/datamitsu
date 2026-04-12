package cmd

import (
	"bytes"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/verifycache"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2.7.2", "2.7.2"},
		{"v2.7.2", "2.7.2"},
		{"v1.0.0-rc1", "1.0.0"},
		{"v1.0.0-alpha", "1.0.0"},
		{"v1.0.0-alpha2", "1.0.0"},
		{"v1.0.0-beta", "1.0.0"},
		{"v1.0.0-beta3", "1.0.0"},
		{"1.2.3-rc", "1.2.3"},
		{"", ""},
		{"v", ""},
		{"1.0", "1.0"},
		{"1.0.0.1", "1.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeVersion(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeVersion(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple version",
			input:    "golangci-lint has version 2.7.2",
			expected: "2.7.2",
		},
		{
			name:     "version with v prefix in output",
			input:    "hadolint v2.12.0",
			expected: "2.12.0",
		},
		{
			name:     "version at start",
			input:    "1.38.0 installed from source",
			expected: "1.38.0",
		},
		{
			name:     "multiline output",
			input:    "cspell\nVersion: 9.7.0\nSome other info",
			expected: "9.7.0",
		},
		{
			name:     "no version found",
			input:    "no version info here",
			expected: "",
		},
		{
			name:     "version with four parts",
			input:    "tool version 1.2.3.4",
			expected: "1.2.3.4",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "version only two parts",
			input:    "version 1.0",
			expected: "1.0",
		},
		{
			name:     "version with trailing dot",
			input:    "version 1.2.3.",
			expected: "1.2.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVersion(tt.input)
			if got != tt.expected {
				t.Errorf("extractVersion(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetAppVersion(t *testing.T) {
	tests := []struct {
		name     string
		app      binmanager.App
		expected string
	}{
		{
			name:     "binary app",
			app:      binmanager.App{Binary: &binmanager.AppConfigBinary{Version: "1.2.3"}},
			expected: "1.2.3",
		},
		{
			name:     "uv app",
			app:      binmanager.App{Uv: &binmanager.AppConfigUV{Version: "2.0.0"}},
			expected: "2.0.0",
		},
		{
			name:     "fnm app",
			app:      binmanager.App{Fnm: &binmanager.AppConfigFNM{Version: "3.1.0"}},
			expected: "3.1.0",
		},
		{
			name:     "jvm app",
			app:      binmanager.App{Jvm: &binmanager.AppConfigJVM{Version: "7.20.0"}},
			expected: "7.20.0",
		},
		{
			name:     "shell app",
			app:      binmanager.App{Shell: &binmanager.AppConfigShell{Name: "echo"}},
			expected: "",
		},
		{
			name:     "empty app",
			app:      binmanager.App{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getAppVersion(tt.app)
			if got != tt.expected {
				t.Errorf("getAppVersion() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestComputeSummary(t *testing.T) {
	tests := []struct {
		name              string
		binaryResults     []binaryVerifyResult
		runtimeResults    []runtimeVerifyResult
		runtimeAppResults []runtimeAppResult
		versionResults    []versionCheckResult
		expected          jsonSummary
	}{
		{
			name:     "empty inputs",
			expected: jsonSummary{},
		},
		{
			name: "all ok",
			binaryResults: []binaryVerifyResult{
				{AppName: "a", Status: "ok"},
				{AppName: "b", Status: "ok"},
			},
			runtimeResults: []runtimeVerifyResult{
				{RuntimeName: "uv", Status: "ok"},
			},
			runtimeAppResults: []runtimeAppResult{
				{AppName: "c", Status: "ok"},
			},
			versionResults: []versionCheckResult{
				{AppName: "a", Status: "ok"},
				{AppName: "b", Status: "ok"},
			},
			expected: jsonSummary{
				BinaryDownloads:  jsonCountOkFailed{Ok: 2},
				RuntimeDownloads: jsonCountOkFailed{Ok: 1},
				RuntimeInstalls:  jsonCountOkFailed{Ok: 1},
				VersionChecks:    jsonCountVersionChecks{Ok: 2},
			},
		},
		{
			name: "mixed statuses",
			binaryResults: []binaryVerifyResult{
				{AppName: "a", Status: "ok"},
				{AppName: "b", Status: "failed"},
			},
			runtimeResults: []runtimeVerifyResult{
				{RuntimeName: "uv", Status: "failed"},
			},
			runtimeAppResults: []runtimeAppResult{
				{AppName: "c", Status: "ok"},
				{AppName: "d", Status: "failed"},
			},
			versionResults: []versionCheckResult{
				{AppName: "a", Status: "ok"},
				{AppName: "b", Status: "mismatch"},
				{AppName: "c", Status: "skipped"},
				{AppName: "d", Status: "exec_failed"},
				{AppName: "e", Status: "parse_failed"},
			},
			expected: jsonSummary{
				BinaryDownloads:  jsonCountOkFailed{Ok: 1, Failed: 1},
				RuntimeDownloads: jsonCountOkFailed{Failed: 1},
				RuntimeInstalls:  jsonCountOkFailed{Ok: 1, Failed: 1},
				VersionChecks:    jsonCountVersionChecks{Ok: 1, Mismatch: 1, Skipped: 1, ExecFailed: 1, ParseFailed: 1},
			},
		},
		{
			name: "nil version results",
			binaryResults: []binaryVerifyResult{
				{AppName: "a", Status: "ok"},
			},
			versionResults: nil,
			expected: jsonSummary{
				BinaryDownloads: jsonCountOkFailed{Ok: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeSummary(tt.binaryResults, tt.runtimeResults, tt.runtimeAppResults, nil, tt.versionResults)
			if got != tt.expected {
				t.Errorf("computeSummary() =\n  %+v\nwant\n  %+v", got, tt.expected)
			}
		})
	}
}

func TestBuildJSONOutput(t *testing.T) {
	binaryResults := []binaryVerifyResult{
		{AppName: "tool-b", Version: "1.0", Os: syslist.OsTypeLinux, Arch: syslist.ArchTypeAmd64, Status: "ok"},
		{AppName: "tool-a", Version: "2.0", Os: syslist.OsTypeLinux, Arch: syslist.ArchTypeAmd64, Status: "ok"},
		{AppName: "tool-a", Version: "2.0", Os: syslist.OsTypeDarwin, Arch: syslist.ArchTypeArm64, Status: "failed", ErrorMsg: "hash mismatch"},
	}
	runtimeResults := []runtimeVerifyResult{
		{RuntimeName: "uv", Os: syslist.OsTypeLinux, Arch: syslist.ArchTypeAmd64, Status: "ok"},
	}
	runtimeAppResults := []runtimeAppResult{
		{AppName: "yamllint", Kind: "uv", Version: "1.0", Status: "ok"},
	}
	versionResults := []versionCheckResult{
		{AppName: "tool-a", Args: []string{"--version"}, Expected: "2.0", Actual: "2.0", Status: "ok"},
	}
	summary := computeSummary(binaryResults, runtimeResults, runtimeAppResults, nil, versionResults)

	output := buildJSONOutput("linux", "amd64", binaryResults, runtimeResults, runtimeAppResults, nil, versionResults, summary,"failed")

	if output.CurrentPlatform.Os != "linux" || output.CurrentPlatform.Arch != "amd64" {
		t.Errorf("unexpected platform: %+v", output.CurrentPlatform)
	}
	if output.OverallStatus != "failed" {
		t.Errorf("expected overallStatus 'failed', got %q", output.OverallStatus)
	}
	if len(output.BinaryApps) != 2 {
		t.Fatalf("expected 2 binary apps, got %d", len(output.BinaryApps))
	}
	if output.BinaryApps[0].Name != "tool-a" {
		t.Errorf("expected first binary app 'tool-a', got %q", output.BinaryApps[0].Name)
	}
	if len(output.BinaryApps[0].Platforms) != 2 {
		t.Errorf("expected 2 platforms for tool-a, got %d", len(output.BinaryApps[0].Platforms))
	}
	if output.BinaryApps[1].Name != "tool-b" {
		t.Errorf("expected second binary app 'tool-b', got %q", output.BinaryApps[1].Name)
	}
	if len(output.ManagedRuntimes) != 1 {
		t.Errorf("expected 1 managed runtime, got %d", len(output.ManagedRuntimes))
	}
	if len(output.RuntimeApps) != 1 {
		t.Errorf("expected 1 runtime app, got %d", len(output.RuntimeApps))
	}
	if len(output.VersionChecks) != 1 {
		t.Errorf("expected 1 version check, got %d", len(output.VersionChecks))
	}
}

func TestFilterSkippedBinaryJobs(t *testing.T) {
	binaryPath := "bin/tool"
	jobs := []binaryVerifyJob{
		{
			appName: "tool-a",
			version: "1.0",
			os:      syslist.OsTypeLinux,
			arch:    syslist.ArchTypeAmd64,
			info:    binmanager.BinaryOsArchInfo{URL: "https://example.com/a", Hash: "abc", ContentType: "tar.gz", BinaryPath: &binaryPath},
		},
		{
			appName: "tool-b",
			version: "2.0",
			os:      syslist.OsTypeLinux,
			arch:    syslist.ArchTypeAmd64,
			info:    binmanager.BinaryOsArchInfo{URL: "https://example.com/b", Hash: "def", ContentType: "tar.gz", BinaryPath: &binaryPath},
		},
		{
			appName: "tool-c",
			version: "3.0",
			os:      syslist.OsTypeDarwin,
			arch:    syslist.ArchTypeArm64,
			info:    binmanager.BinaryOsArchInfo{URL: "https://example.com/c", Hash: "ghi", ContentType: "tar.gz"},
		},
	}

	aKey, aFP := binaryJobKeyAndFP(jobs[0])
	cKey, cFP := binaryJobKeyAndFP(jobs[2])

	sm := verifycache.NewStateManager(&verifycache.VerifyState{
		Version: 1,
		Entries: map[string]verifycache.VerifyEntry{
			aKey: {Fingerprint: aFP, Status: "ok"},
			cKey: {Fingerprint: cFP, Status: "ok"},
		},
	}, "")

	t.Run("partitions correctly", func(t *testing.T) {
		toRun, skipped := filterSkippedBinaryJobs(jobs, sm)
		if len(toRun) != 1 {
			t.Fatalf("expected 1 toRun, got %d", len(toRun))
		}
		if len(skipped) != 2 {
			t.Fatalf("expected 2 skipped, got %d", len(skipped))
		}
		if toRun[0].appName != "tool-b" {
			t.Errorf("expected toRun[0] to be tool-b, got %s", toRun[0].appName)
		}
	})

	t.Run("changed URL not skipped", func(t *testing.T) {
		modifiedJobs := []binaryVerifyJob{{
			appName: "tool-a",
			version: "1.0",
			os:      syslist.OsTypeLinux,
			arch:    syslist.ArchTypeAmd64,
			info:    binmanager.BinaryOsArchInfo{URL: "https://example.com/a-v2", Hash: "abc", ContentType: "tar.gz", BinaryPath: &binaryPath},
		}}
		toRun, skipped := filterSkippedBinaryJobs(modifiedJobs, sm)
		if len(toRun) != 1 {
			t.Errorf("expected 1 toRun, got %d", len(toRun))
		}
		if len(skipped) != 0 {
			t.Errorf("expected 0 skipped, got %d", len(skipped))
		}
	})

	t.Run("failed status not skipped", func(t *testing.T) {
		bKey, bFP := binaryJobKeyAndFP(jobs[1])
		smFailed := verifycache.NewStateManager(&verifycache.VerifyState{
			Version: 1,
			Entries: map[string]verifycache.VerifyEntry{
				bKey: {Fingerprint: bFP, Status: "failed"},
			},
		}, "")
		toRun, skipped := filterSkippedBinaryJobs(jobs[1:2], smFailed)
		if len(toRun) != 1 {
			t.Errorf("expected 1 toRun for failed entry, got %d", len(toRun))
		}
		if len(skipped) != 0 {
			t.Errorf("expected 0 skipped for failed entry, got %d", len(skipped))
		}
	})

	t.Run("empty jobs", func(t *testing.T) {
		toRun, skipped := filterSkippedBinaryJobs(nil, sm)
		if len(toRun) != 0 || len(skipped) != 0 {
			t.Errorf("expected empty results for nil jobs")
		}
	})
}

func TestFilterSkippedRuntimeJobs(t *testing.T) {
	binaryPath := "bin/rt"
	jobs := []runtimeVerifyJob{
		{
			runtimeName: "uv",
			os:          syslist.OsTypeLinux,
			arch:        syslist.ArchTypeAmd64,
			info:        binmanager.BinaryOsArchInfo{URL: "https://example.com/uv", Hash: "uvhash", ContentType: "tar.gz", BinaryPath: &binaryPath},
		},
		{
			runtimeName: "fnm",
			os:          syslist.OsTypeLinux,
			arch:        syslist.ArchTypeAmd64,
			info:        binmanager.BinaryOsArchInfo{URL: "https://example.com/fnm", Hash: "fnmhash", ContentType: "tar.gz", BinaryPath: &binaryPath},
		},
	}

	uvKey, uvFP := runtimeJobKeyAndFP(jobs[0])

	sm := verifycache.NewStateManager(&verifycache.VerifyState{
		Version: 1,
		Entries: map[string]verifycache.VerifyEntry{
			uvKey: {Fingerprint: uvFP, Status: "ok"},
		},
	}, "")

	t.Run("partitions correctly", func(t *testing.T) {
		toRun, skipped := filterSkippedRuntimeJobs(jobs, sm)
		if len(toRun) != 1 {
			t.Fatalf("expected 1 toRun, got %d", len(toRun))
		}
		if len(skipped) != 1 {
			t.Fatalf("expected 1 skipped, got %d", len(skipped))
		}
		if toRun[0].runtimeName != "fnm" {
			t.Errorf("expected toRun[0] to be fnm, got %s", toRun[0].runtimeName)
		}
		if skipped[0].runtimeName != "uv" {
			t.Errorf("expected skipped[0] to be uv, got %s", skipped[0].runtimeName)
		}
	})

	t.Run("empty jobs", func(t *testing.T) {
		toRun, skipped := filterSkippedRuntimeJobs(nil, sm)
		if len(toRun) != 0 || len(skipped) != 0 {
			t.Errorf("expected empty results for nil jobs")
		}
	})
}

func TestFilterSkippedRuntimeAppEntries(t *testing.T) {
	runtimes := config.MapOfRuntimes{
		"uv-rt": {Kind: "uv"},
	}

	entries := []runtimeAppEntry{
		{
			name: "yamllint",
			app:  binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1.35.0", Runtime: "uv-rt"}},
			kind: "uv",
		},
		{
			name: "ruff",
			app:  binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "ruff", Version: "0.5.0", Runtime: "uv-rt"}},
			kind: "uv",
		},
	}

	yKey, yFP := runtimeAppKeyAndFP(entries[0], runtimes, "linux", "amd64")

	sm := verifycache.NewStateManager(&verifycache.VerifyState{
		Version: 1,
		Entries: map[string]verifycache.VerifyEntry{
			yKey: {Fingerprint: yFP, Status: "ok"},
		},
	}, "")

	t.Run("partitions correctly", func(t *testing.T) {
		toRun, skipped := filterSkippedRuntimeAppEntries(entries, sm, runtimes, "linux", "amd64")
		if len(toRun) != 1 {
			t.Fatalf("expected 1 toRun, got %d", len(toRun))
		}
		if len(skipped) != 1 {
			t.Fatalf("expected 1 skipped, got %d", len(skipped))
		}
		if toRun[0].name != "ruff" {
			t.Errorf("expected toRun[0] to be ruff, got %s", toRun[0].name)
		}
		if skipped[0].name != "yamllint" {
			t.Errorf("expected skipped[0] to be yamllint, got %s", skipped[0].name)
		}
	})

	t.Run("changed version not skipped", func(t *testing.T) {
		modified := []runtimeAppEntry{{
			name: "yamllint",
			app:  binmanager.App{Uv: &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1.36.0", Runtime: "uv-rt"}},
			kind: "uv",
		}}
		toRun, skipped := filterSkippedRuntimeAppEntries(modified, sm, runtimes, "linux", "amd64")
		if len(toRun) != 1 {
			t.Errorf("expected 1 toRun after version change, got %d", len(toRun))
		}
		if len(skipped) != 0 {
			t.Errorf("expected 0 skipped after version change, got %d", len(skipped))
		}
	})
}

func TestFilterSkippedVersionCheckEntries(t *testing.T) {
	entries := []versionCheckEntry{
		{
			name: "tool-a",
			app:  binmanager.App{Binary: &binmanager.AppConfigBinary{Version: "1.0.0"}},
		},
		{
			name: "tool-b",
			app:  binmanager.App{Binary: &binmanager.AppConfigBinary{Version: "2.0.0"}},
		},
		{
			name: "tool-disabled",
			app: binmanager.App{
				Binary:       &binmanager.AppConfigBinary{Version: "3.0.0"},
				VersionCheck: &binmanager.AppVersionCheck{Disabled: true},
			},
		},
	}

	aKey, aFP := versionCheckKeyAndFP(entries[0], "linux", "amd64", "glibc")

	sm := verifycache.NewStateManager(&verifycache.VerifyState{
		Version: 1,
		Entries: map[string]verifycache.VerifyEntry{
			aKey: {Fingerprint: aFP, Status: "ok"},
		},
	}, "")

	t.Run("partitions correctly", func(t *testing.T) {
		toRun, skipped := filterSkippedVersionCheckEntries(entries, sm, "linux", "amd64", "glibc")
		if len(toRun) != 2 {
			t.Fatalf("expected 2 toRun (tool-b + disabled), got %d", len(toRun))
		}
		if len(skipped) != 1 {
			t.Fatalf("expected 1 skipped, got %d", len(skipped))
		}
		if skipped[0].name != "tool-a" {
			t.Errorf("expected skipped[0] to be tool-a, got %s", skipped[0].name)
		}
	})

	t.Run("disabled entries always run", func(t *testing.T) {
		disabledKey, disabledFP := versionCheckKeyAndFP(entries[2], "linux", "amd64", "glibc")
		smWithDisabled := verifycache.NewStateManager(&verifycache.VerifyState{
			Version: 1,
			Entries: map[string]verifycache.VerifyEntry{
				disabledKey: {Fingerprint: disabledFP, Status: "ok"},
			},
		}, "")
		toRun, _ := filterSkippedVersionCheckEntries(entries[2:3], smWithDisabled, "linux", "amd64", "glibc")
		if len(toRun) != 1 {
			t.Fatalf("expected 1 toRun for disabled entry, got %d", len(toRun))
		}
		if toRun[0].name != "tool-disabled" {
			t.Errorf("expected disabled entry in toRun, got %s", toRun[0].name)
		}
	})
}

func TestComputeSummaryWithCachedEntries(t *testing.T) {
	tests := []struct {
		name              string
		binaryResults     []binaryVerifyResult
		runtimeResults    []runtimeVerifyResult
		runtimeAppResults []runtimeAppResult
		versionResults    []versionCheckResult
		expected          jsonSummary
	}{
		{
			name: "all cached",
			binaryResults: []binaryVerifyResult{
				{AppName: "a", Status: "cached"},
				{AppName: "b", Status: "cached"},
			},
			runtimeResults: []runtimeVerifyResult{
				{RuntimeName: "uv", Status: "cached"},
			},
			runtimeAppResults: []runtimeAppResult{
				{AppName: "c", Status: "cached"},
			},
			versionResults: []versionCheckResult{
				{AppName: "a", Status: "cached"},
			},
			expected: jsonSummary{
				BinaryDownloads:  jsonCountOkFailed{Cached: 2},
				RuntimeDownloads: jsonCountOkFailed{Cached: 1},
				RuntimeInstalls:  jsonCountOkFailed{Cached: 1},
				VersionChecks:    jsonCountVersionChecks{Cached: 1},
			},
		},
		{
			name: "mixed ok, failed, and cached",
			binaryResults: []binaryVerifyResult{
				{AppName: "a", Status: "ok"},
				{AppName: "b", Status: "cached"},
				{AppName: "c", Status: "failed"},
			},
			runtimeResults: []runtimeVerifyResult{
				{RuntimeName: "uv", Status: "ok"},
				{RuntimeName: "fnm", Status: "cached"},
			},
			runtimeAppResults: []runtimeAppResult{
				{AppName: "d", Status: "cached"},
				{AppName: "e", Status: "ok"},
			},
			versionResults: []versionCheckResult{
				{AppName: "a", Status: "ok"},
				{AppName: "b", Status: "cached"},
				{AppName: "c", Status: "mismatch"},
			},
			expected: jsonSummary{
				BinaryDownloads:  jsonCountOkFailed{Ok: 1, Failed: 1, Cached: 1},
				RuntimeDownloads: jsonCountOkFailed{Ok: 1, Cached: 1},
				RuntimeInstalls:  jsonCountOkFailed{Ok: 1, Cached: 1},
				VersionChecks:    jsonCountVersionChecks{Ok: 1, Mismatch: 1, Cached: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeSummary(tt.binaryResults, tt.runtimeResults, tt.runtimeAppResults, nil, tt.versionResults)
			if got != tt.expected {
				t.Errorf("computeSummary() =\n  %+v\nwant\n  %+v", got, tt.expected)
			}
		})
	}
}

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestPrintBinaryResultCached(t *testing.T) {
	r := binaryVerifyResult{
		AppName: "lefthook",
		Version: "1.11.0",
		Os:      syslist.OsTypeDarwin,
		Arch:    syslist.ArchTypeArm64,
		Status:  "cached",
	}

	output := captureStdout(func() {
		printBinaryResult(r)
	})

	if !strings.Contains(output, "lefthook") {
		t.Errorf("expected output to contain app name, got: %s", output)
	}
	if !strings.Contains(output, "CACHED") {
		t.Errorf("expected output to contain CACHED, got: %s", output)
	}
	if !strings.Contains(output, "≡") {
		t.Errorf("expected output to contain ≡ symbol, got: %s", output)
	}
}

func TestPrintRuntimeResultCached(t *testing.T) {
	r := runtimeVerifyResult{
		RuntimeName: "uv",
		Os:          syslist.OsTypeLinux,
		Arch:        syslist.ArchTypeAmd64,
		Status:      "cached",
	}

	output := captureStdout(func() {
		printRuntimeResult(r)
	})

	if !strings.Contains(output, "uv") {
		t.Errorf("expected output to contain runtime name, got: %s", output)
	}
	if !strings.Contains(output, "CACHED") {
		t.Errorf("expected output to contain CACHED, got: %s", output)
	}
}

func TestPrintRuntimeAppResultCached(t *testing.T) {
	r := runtimeAppResult{
		AppName: "yamllint",
		Kind:    "uv",
		Version: "1.35.0",
		Status:  "cached",
	}

	output := captureStdout(func() {
		printRuntimeAppResult(r)
	})

	if !strings.Contains(output, "yamllint") {
		t.Errorf("expected output to contain app name, got: %s", output)
	}
	if !strings.Contains(output, "CACHED") {
		t.Errorf("expected output to contain CACHED, got: %s", output)
	}
}

func TestPrintVersionCheckResultCached(t *testing.T) {
	r := versionCheckResult{
		AppName: "tool-a",
		Args:    []string{"--version"},
		Status:  "cached",
	}

	output := captureStdout(func() {
		printVersionCheckResult(r)
	})

	if !strings.Contains(output, "tool-a") {
		t.Errorf("expected output to contain app name, got: %s", output)
	}
	if !strings.Contains(output, "CACHED") {
		t.Errorf("expected output to contain CACHED, got: %s", output)
	}
}

func TestPrintSummaryWithCached(t *testing.T) {
	s := jsonSummary{
		BinaryDownloads:  jsonCountOkFailed{Ok: 10, Failed: 1, Cached: 5},
		RuntimeDownloads: jsonCountOkFailed{Ok: 3, Cached: 2},
		RuntimeInstalls:  jsonCountOkFailed{Ok: 4, Failed: 1, Cached: 3},
		VersionChecks:    jsonCountVersionChecks{Ok: 8, Mismatch: 1, Cached: 4},
	}

	output := captureStdout(func() {
		printSummary(s)
	})

	if !strings.Contains(output, "(5 cached)") {
		t.Errorf("expected binary line to contain '(5 cached)', got: %s", output)
	}
	if !strings.Contains(output, "(2 cached)") {
		t.Errorf("expected runtime line to contain '(2 cached)', got: %s", output)
	}
	if !strings.Contains(output, "(3 cached)") {
		t.Errorf("expected install line to contain '(3 cached)', got: %s", output)
	}
	if !strings.Contains(output, "(4 cached)") {
		t.Errorf("expected version line to contain '(4 cached)', got: %s", output)
	}
}

func TestPrintSummaryWithoutCached(t *testing.T) {
	s := jsonSummary{
		BinaryDownloads:  jsonCountOkFailed{Ok: 10, Failed: 1},
		RuntimeDownloads: jsonCountOkFailed{Ok: 3},
		RuntimeInstalls:  jsonCountOkFailed{Ok: 4},
		VersionChecks:    jsonCountVersionChecks{Ok: 8},
	}

	output := captureStdout(func() {
		printSummary(s)
	})

	if strings.Contains(output, "cached") {
		t.Errorf("expected no 'cached' text when no cached entries, got: %s", output)
	}
}

func TestBuildJSONOutputWithCached(t *testing.T) {
	binaryResults := []binaryVerifyResult{
		{AppName: "tool-a", Version: "1.0", Os: syslist.OsTypeLinux, Arch: syslist.ArchTypeAmd64, Status: "ok"},
		{AppName: "tool-a", Version: "1.0", Os: syslist.OsTypeDarwin, Arch: syslist.ArchTypeArm64, Status: "cached"},
	}
	runtimeResults := []runtimeVerifyResult{
		{RuntimeName: "uv", Os: syslist.OsTypeLinux, Arch: syslist.ArchTypeAmd64, Status: "cached"},
	}
	runtimeAppResults := []runtimeAppResult{
		{AppName: "yamllint", Kind: "uv", Version: "1.0", Status: "cached"},
	}
	versionResults := []versionCheckResult{
		{AppName: "tool-a", Args: []string{"--version"}, Status: "cached"},
	}
	summary := computeSummary(binaryResults, runtimeResults, runtimeAppResults, nil, versionResults)

	output := buildJSONOutput("linux", "amd64", binaryResults, runtimeResults, runtimeAppResults, nil, versionResults, summary,"ok")

	if output.Summary.BinaryDownloads.Cached != 1 {
		t.Errorf("expected BinaryDownloads.Cached=1, got %d", output.Summary.BinaryDownloads.Cached)
	}
	if output.Summary.RuntimeDownloads.Cached != 1 {
		t.Errorf("expected RuntimeDownloads.Cached=1, got %d", output.Summary.RuntimeDownloads.Cached)
	}
	if output.Summary.RuntimeInstalls.Cached != 1 {
		t.Errorf("expected RuntimeInstalls.Cached=1, got %d", output.Summary.RuntimeInstalls.Cached)
	}
	if output.Summary.VersionChecks.Cached != 1 {
		t.Errorf("expected VersionChecks.Cached=1, got %d", output.Summary.VersionChecks.Cached)
	}

	// Verify platform result has cached status
	if len(output.BinaryApps) != 1 {
		t.Fatalf("expected 1 binary app, got %d", len(output.BinaryApps))
	}
	foundCached := false
	for _, p := range output.BinaryApps[0].Platforms {
		if p.Status == "cached" {
			foundCached = true
		}
	}
	if !foundCached {
		t.Error("expected a platform with status 'cached'")
	}

	// Verify runtime app has cached status
	if output.RuntimeApps[0].Status != "cached" {
		t.Errorf("expected runtime app status 'cached', got %q", output.RuntimeApps[0].Status)
	}

	// Verify version check has cached status
	if output.VersionChecks[0].Status != "cached" {
		t.Errorf("expected version check status 'cached', got %q", output.VersionChecks[0].Status)
	}
}

func TestRecordAfterBinaryResult(t *testing.T) {
	binaryPath := "bin/tool"

	t.Run("records ok result and is skippable", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")
		sm := verifycache.NewStateManager(&verifycache.VerifyState{
			Version: 1,
			CWD:     "/test",
			Entries: map[string]verifycache.VerifyEntry{},
		}, path)

		j := binaryVerifyJob{
			appName: "tool-a",
			version: "1.0",
			os:      syslist.OsTypeLinux,
			arch:    syslist.ArchTypeAmd64,
			info:    binmanager.BinaryOsArchInfo{URL: "https://example.com/a", Hash: "abc", ContentType: "tar.gz", BinaryPath: &binaryPath},
		}
		key, fp := binaryJobKeyAndFP(j)

		err := sm.Record(key, fp, "ok", "")
		if err != nil {
			t.Fatalf("Record() error = %v", err)
		}

		loaded, err := verifycache.LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}

		entry, ok := loaded.Entries[key]
		if !ok {
			t.Fatalf("entry %q not found in state", key)
		}
		if entry.Status != "ok" {
			t.Errorf("Status = %q, want %q", entry.Status, "ok")
		}
		if entry.Fingerprint != fp {
			t.Errorf("Fingerprint = %q, want %q", entry.Fingerprint, fp)
		}
		if !sm.ShouldSkip(key, fp) {
			t.Error("ShouldSkip should return true after recording ok result")
		}
	})

	t.Run("records failed result", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")
		sm := verifycache.NewStateManager(&verifycache.VerifyState{
			Version: 1,
			CWD:     "/test",
			Entries: map[string]verifycache.VerifyEntry{},
		}, path)

		j := binaryVerifyJob{
			appName: "tool-b",
			version: "2.0",
			os:      syslist.OsTypeLinux,
			arch:    syslist.ArchTypeAmd64,
			info:    binmanager.BinaryOsArchInfo{URL: "https://example.com/b", Hash: "def", ContentType: "tar.gz"},
		}
		key, fp := binaryJobKeyAndFP(j)

		err := sm.Record(key, fp, "failed", "hash mismatch")
		if err != nil {
			t.Fatalf("Record() error = %v", err)
		}

		if sm.ShouldSkip(key, fp) {
			t.Error("ShouldSkip should return false for failed result")
		}

		loaded, err := verifycache.LoadState(path)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}
		entry := loaded.Entries[key]
		if entry.Error != "hash mismatch" {
			t.Errorf("Error = %q, want %q", entry.Error, "hash mismatch")
		}
	})
}

func TestBundleKeyAndFP(t *testing.T) {
	entry := bundleVerifyEntry{
		name: "agent-skills",
		bundle: &binmanager.Bundle{
			Version: "1.0",
			Files:   map[string]string{"agents.md": "content"},
		},
	}

	key, fp := bundleKeyAndFP(entry)

	if key != "bundle:agent-skills" {
		t.Errorf("expected key 'bundle:agent-skills', got %q", key)
	}
	if fp == "" {
		t.Error("expected non-empty fingerprint")
	}

	// Same entry produces same fingerprint
	_, fp2 := bundleKeyAndFP(entry)
	if fp != fp2 {
		t.Errorf("expected deterministic fingerprint, got %q and %q", fp, fp2)
	}

	// Different version produces different fingerprint
	entry2 := bundleVerifyEntry{
		name: "agent-skills",
		bundle: &binmanager.Bundle{
			Version: "2.0",
			Files:   map[string]string{"agents.md": "content"},
		},
	}
	_, fp3 := bundleKeyAndFP(entry2)
	if fp == fp3 {
		t.Error("expected different fingerprint for different version")
	}
}

func TestFilterSkippedBundleEntries(t *testing.T) {
	entries := []bundleVerifyEntry{
		{
			name:   "bundle-a",
			bundle: &binmanager.Bundle{Version: "1.0", Files: map[string]string{"a.txt": "a"}},
		},
		{
			name:   "bundle-b",
			bundle: &binmanager.Bundle{Version: "2.0", Files: map[string]string{"b.txt": "b"}},
		},
	}

	aKey, aFP := bundleKeyAndFP(entries[0])

	sm := verifycache.NewStateManager(&verifycache.VerifyState{
		Version: 1,
		Entries: map[string]verifycache.VerifyEntry{
			aKey: {Fingerprint: aFP, Status: "ok"},
		},
	}, "")

	t.Run("partitions correctly", func(t *testing.T) {
		toRun, skipped := filterSkippedBundleEntries(entries, sm)
		if len(toRun) != 1 {
			t.Fatalf("expected 1 toRun, got %d", len(toRun))
		}
		if len(skipped) != 1 {
			t.Fatalf("expected 1 skipped, got %d", len(skipped))
		}
		if toRun[0].name != "bundle-b" {
			t.Errorf("expected toRun[0] to be bundle-b, got %s", toRun[0].name)
		}
		if skipped[0].name != "bundle-a" {
			t.Errorf("expected skipped[0] to be bundle-a, got %s", skipped[0].name)
		}
	})

	t.Run("empty entries", func(t *testing.T) {
		toRun, skipped := filterSkippedBundleEntries(nil, sm)
		if len(toRun) != 0 || len(skipped) != 0 {
			t.Errorf("expected empty results for nil entries")
		}
	})
}

func TestComputeSummaryWithBundles(t *testing.T) {
	bundleResults := []bundleVerifyResult{
		{BundleName: "a", Status: "ok"},
		{BundleName: "b", Status: "failed"},
		{BundleName: "c", Status: "cached"},
	}

	got := computeSummary(nil, nil, nil, bundleResults, nil)
	if got.BundleInstalls.Ok != 1 {
		t.Errorf("expected BundleInstalls.Ok=1, got %d", got.BundleInstalls.Ok)
	}
	if got.BundleInstalls.Failed != 1 {
		t.Errorf("expected BundleInstalls.Failed=1, got %d", got.BundleInstalls.Failed)
	}
	if got.BundleInstalls.Cached != 1 {
		t.Errorf("expected BundleInstalls.Cached=1, got %d", got.BundleInstalls.Cached)
	}
}

func TestBuildJSONOutputWithBundles(t *testing.T) {
	bundleResults := []bundleVerifyResult{
		{BundleName: "agent-skills", Version: "1.0", Status: "ok"},
		{BundleName: "docs", Version: "2.0", Status: "failed", ErrorMsg: "install error"},
	}

	summary := computeSummary(nil, nil, nil, bundleResults, nil)
	output := buildJSONOutput("linux", "amd64", nil, nil, nil, bundleResults, nil, summary, "failed")

	if len(output.Bundles) != 2 {
		t.Fatalf("expected 2 bundles, got %d", len(output.Bundles))
	}
	if output.Bundles[0].Name != "agent-skills" {
		t.Errorf("expected first bundle 'agent-skills', got %q", output.Bundles[0].Name)
	}
	if output.Bundles[0].Status != "ok" {
		t.Errorf("expected first bundle status 'ok', got %q", output.Bundles[0].Status)
	}
	if output.Bundles[1].Error != "install error" {
		t.Errorf("expected second bundle error 'install error', got %q", output.Bundles[1].Error)
	}
	if output.Summary.BundleInstalls.Ok != 1 {
		t.Errorf("expected BundleInstalls.Ok=1, got %d", output.Summary.BundleInstalls.Ok)
	}
	if output.Summary.BundleInstalls.Failed != 1 {
		t.Errorf("expected BundleInstalls.Failed=1, got %d", output.Summary.BundleInstalls.Failed)
	}
}

func TestPrintBundleResult(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		r := bundleVerifyResult{BundleName: "agent-skills", Version: "1.0", Status: "ok"}
		output := captureStdout(func() { printBundleResult(r) })
		if !strings.Contains(output, "agent-skills") {
			t.Errorf("expected output to contain bundle name, got: %s", output)
		}
		if !strings.Contains(output, "installed") {
			t.Errorf("expected output to contain 'installed', got: %s", output)
		}
	})

	t.Run("cached", func(t *testing.T) {
		r := bundleVerifyResult{BundleName: "docs", Version: "2.0", Status: "cached"}
		output := captureStdout(func() { printBundleResult(r) })
		if !strings.Contains(output, "CACHED") {
			t.Errorf("expected output to contain CACHED, got: %s", output)
		}
	})

	t.Run("failed", func(t *testing.T) {
		r := bundleVerifyResult{BundleName: "broken", Version: "1.0", Status: "failed", ErrorMsg: "install error"}
		output := captureStdout(func() { printBundleResult(r) })
		if !strings.Contains(output, "FAILED") {
			t.Errorf("expected output to contain FAILED, got: %s", output)
		}
	})
}

func TestPrintSummaryWithBundles(t *testing.T) {
	s := jsonSummary{
		BundleInstalls: jsonCountOkFailed{Ok: 3, Failed: 1, Cached: 2},
	}

	output := captureStdout(func() { printSummary(s) })

	if !strings.Contains(output, "Bundle installs") {
		t.Errorf("expected summary to contain 'Bundle installs', got: %s", output)
	}
	if !strings.Contains(output, "(2 cached)") {
		t.Errorf("expected summary to contain '(2 cached)', got: %s", output)
	}
}
