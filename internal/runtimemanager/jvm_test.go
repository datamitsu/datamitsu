package runtimemanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestDownloadAndVerifyJAR(t *testing.T) {
	body := []byte("jar-payload")
	pin := digest.SHA256Of(body).String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	t.Run("valid verified download replaces destination", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "tool.jar")
		if err := downloadAndVerifyJAR(context.Background(), "tool", server.URL, pin, dest); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dest)
		if err != nil || string(got) != string(body) {
			t.Fatalf("downloaded content=%q err=%v", got, err)
		}
	})

	for _, tc := range []struct {
		name, url, hash, want string
	}{
		{"mismatched pin", server.URL, "sha256:" + strings.Repeat("0", 64), "hash mismatch"},
		{"invalid pin", server.URL, "not-a-digest", "invalid JAR hash pin"},
		{"http failure", server.URL + "/missing", pin, "status 404"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "tool.jar")
			if err := os.WriteFile(dest, []byte("previous"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := downloadAndVerifyJAR(context.Background(), "tool", tc.url, tc.hash, dest); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want substring %q", err, tc.want)
			}
			got, err := os.ReadFile(dest)
			if err != nil || string(got) != "previous" {
				t.Fatalf("failed download changed destination: content=%q err=%v", got, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "tool.jar" {
				t.Fatalf("temporary download leaked: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestInstallJVMAppReadyStateAndFailedDownloadCleanup(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	payload := []byte("verified-jar")
	pin := digest.SHA256Of(payload).String()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	javaDir := t.TempDir()
	java := filepath.Join(javaDir, "java")
	if err := os.WriteFile(java, []byte("java"), 0o755); err != nil {
		t.Fatal(err)
	}
	rm := New(config.MapOfRuntimes{"jdk": {Kind: config.RuntimeKindJVM, Mode: config.RuntimeModeSystem, System: &config.RuntimeConfigSystem{Command: java}}})
	app := &binmanager.AppConfigJVM{Runtime: "jdk", Version: "1", JarURL: server.URL, JarHash: pin}
	if err := rm.InstallJVMApp(context.Background(), "tool", app, nil, nil); err != nil {
		t.Fatal(err)
	}
	path, err := rm.GetJVMAppPath("tool", app, nil, nil, "jdk")
	if err != nil {
		t.Fatal(err)
	}
	if !appReady(path) {
		t.Fatal("successful JAR install was not marked ready")
	}
	if err := rm.InstallJVMApp(context.Background(), "tool", app, nil, nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("ready app caused %d downloads, want one", requests.Load())
	}
	if err := os.Remove(filepath.Join(path, appReadyMarker)); err != nil {
		t.Fatal(err)
	}
	if err := rm.InstallJVMApp(context.Background(), "tool", app, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !appReady(path) || requests.Load() != 2 {
		t.Fatalf("markerless app repair: ready=%v requests=%d", appReady(path), requests.Load())
	}

	bad := *app
	bad.JarHash = "sha256:" + strings.Repeat("0", 64)
	if err := rm.InstallJVMApp(context.Background(), "broken", &bad, nil, nil); err == nil {
		t.Fatal("install with incorrect JAR pin succeeded")
	}
	brokenPath, err := rm.GetJVMAppPath("broken", &bad, nil, nil, "jdk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(brokenPath); !os.IsNotExist(err) {
		t.Fatalf("failed install left app workspace: stat err=%v", err)
	}
}
