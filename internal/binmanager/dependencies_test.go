package binmanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestAppDependencyClosure(t *testing.T) {
	apps := MapOfApps{
		"a": {DependsOn: []string{"c", "b"}},
		"b": {DependsOn: []string{"d"}},
		"c": {DependsOn: []string{"d"}},
		"d": {},
		"e": {},
	}
	for _, tt := range []struct {
		name        string
		roots, want []string
		wantErr     string
	}{
		{"empty", nil, nil, ""},
		{"transitive", []string{"b"}, []string{"d", "b"}, ""},
		{"diamond", []string{"a"}, []string{"d", "b", "c", "a"}, ""},
		{"order and dedup", []string{"e", "a", "d", "a"}, []string{"d", "b", "c", "a", "e"}, ""},
		{"unknown", []string{"missing"}, nil, "app 'missing' not found in registry"},
		{"unknown with valid root", []string{"missing", "b"}, []string{"d", "b"}, "app 'missing' not found in registry"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AppDependencyClosure(apps, tt.roots)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("closure = %v, want %v", got, tt.want)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
	if !slices.Equal(apps["a"].DependsOn, []string{"c", "b"}) {
		t.Fatal("closure mutated dependency list")
	}
}

func TestDependencyProvisioning(t *testing.T) {
	for _, boundary := range []string{"ensure", "command", "exec", "binary", "install", "concurrent install", "shell command", "shell exec"} {
		t.Run(boundary, func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			t.Setenv("DATAMITSU_OFFLINE", "1")
			apps := binaryAppFixture(t, "root")
			root := apps["root"]
			root.Required = true
			root.DependsOn = []string{"left", "right"}
			apps["root"] = root
			apps["left"] = App{Uv: &AppConfigUV{}, DependsOn: []string{"leaf"}, Lazy: true}
			apps["right"] = App{Node: &AppConfigNode{}, DependsOn: []string{"leaf"}}
			apps["leaf"] = App{Bun: &AppConfigBun{}, Lazy: true}
			apps["shell"] = App{Shell: &AppConfigShell{Name: "echo"}, DependsOn: []string{"root"}}
			dir := t.TempDir()
			var mu sync.Mutex
			calls := map[string]int{}
			mock := &mockRuntimeAppManager{getCommandInfoFunc: func(_ context.Context, name string, _ App) (*CommandInfo, error) {
				mu.Lock()
				defer mu.Unlock()
				calls[name]++
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte("installed"), 0o600); err != nil {
					return nil, err
				}
				return &CommandInfo{Command: path}, nil
			}}
			bm := New(apps, nil, mock)
			path, err := bm.getBinaryPath("root")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("cached root"), 0o755); err != nil {
				t.Fatal(err)
			}
			switch boundary {
			case "ensure":
				err = bm.EnsureTools(t.Context(), []string{"root", "root"})
			case "command":
				_, err = bm.GetCommandInfo(t.Context(), "root")
			case "exec":
				_, err = bm.GetExecCmd(t.Context(), "root", nil)
			case "binary":
				_, err = bm.GetBinaryPath(t.Context(), "root")
			case "install":
				err = bm.Install(t.Context())
			case "concurrent install":
				_, err = bm.InstallWithConcurrency(t.Context(), false, 3, true)
			case "shell command":
				_, err = bm.GetCommandInfo(t.Context(), "shell")
			case "shell exec":
				_, err = bm.GetExecCmd(t.Context(), "shell", nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"left", "right", "leaf"} {
				if calls[name] != 1 {
					t.Errorf("%s resolved %d times, want 1", name, calls[name])
				}
				if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
					t.Errorf("dependency %s not installed: %v", name, err)
				}
			}
		})
	}
}

func TestDependencyFailurePreventsCommandResolution(t *testing.T) {
	for _, tt := range []struct {
		name    string
		apps    MapOfApps
		wantErr string
	}{
		{"missing", MapOfApps{"root": {Shell: &AppConfigShell{Name: "echo"}, DependsOn: []string{"missing"}}}, "missing"},
		{"cycle", MapOfApps{"root": {DependsOn: []string{"dep"}}, "dep": {DependsOn: []string{"root"}}}, "root -> dep -> root"},
		{"install error", MapOfApps{"root": {Shell: &AppConfigShell{Name: "echo"}, DependsOn: []string{"dep"}}, "dep": {Uv: &AppConfigUV{}}}, "install failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bm := New(tt.apps, nil, &mockRuntimeAppManager{getCommandInfoFunc: func(context.Context, string, App) (*CommandInfo, error) { return nil, errors.New("install failed") }})
			if _, err := bm.GetCommandInfo(t.Context(), "root"); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestConcurrentAppDependencyInstalls(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "")
	payload := []byte("#!/bin/sh\necho fixture\n")
	previousClient := httpClient
	httpClient = &http.Client{Transport: dependencyTransport{payload: payload}}
	t.Cleanup(func() { httpClient = previousClient })
	sum := sha256.Sum256(payload)
	apps := MapOfApps{}
	for _, name := range []string{"left", "right", "leaf"} {
		app := binaryAppFixture(t, name)[name]
		for _, arches := range app.Binary.Binaries {
			for _, variants := range arches {
				for libc, info := range variants {
					info.URL = "https://fixture.invalid/tool"
					info.Hash = hex.EncodeToString(sum[:])
					variants[libc] = info
				}
			}
		}
		if name != "leaf" {
			app.DependsOn = []string{"leaf"}
		}
		apps[name] = app
	}
	bm := New(apps, nil, nil)
	before := NetworkDownloads()
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			name := []string{"left", "right"}[i%2]
			if _, err := bm.GetCommandInfo(t.Context(), name); err != nil {
				t.Errorf("GetCommandInfo(%s): %v", name, err)
			}
		})
	}
	wg.Wait()
	if got := NetworkDownloads() - before; got != 3 {
		t.Fatalf("downloads = %d, want 3 distinct apps", got)
	}
	for name := range apps {
		path, err := bm.getBinaryPath(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(payload) {
			t.Fatalf("%s has corrupt installed content", name)
		}
	}
	root := apps["left"]
	beforePath, err := bm.getBinaryPath("left")
	if err != nil {
		t.Fatal(err)
	}
	root.DependsOn = nil
	apps["left"] = root
	afterPath, err := bm.getBinaryPath("left")
	if err != nil {
		t.Fatal(err)
	}
	if beforePath != afterPath {
		t.Fatal("dependsOn changed binary install identity")
	}
}

type dependencyTransport struct{ payload []byte }

func (d dependencyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(d.payload)), ContentLength: int64(len(d.payload)), Header: make(http.Header), Request: req}, nil
}
