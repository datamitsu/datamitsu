package installstate

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
)

func TestWaitStatusReportsOnlyFreshTrustedCounters(t *testing.T) {
	for _, name := range []string{"bytes", "packages", "unknown total", "exceeded total", "negative count", "unknown unit", "stale", "future", "wrong schema", "missing owner", "invalid pid", "invalid phase", "malformed", "missing"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			dir := filepath.Join(env.GetCachePath(), "store-locks", "status")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			completed, total := int64(3), int64(10)
			record := status{Schema: schema, Owner: "owner", PID: os.Getpid(), Phase: "download", UpdatedAt: time.Now(), Completed: &completed, Total: &total, Unit: "bytes"}
			want := "Waiting for installation: probe (download 3/10 bytes)"
			switch name {
			case "packages":
				record.Phase, record.Unit = "install", "packages"
				want = "Waiting for installation: probe (install 3/10 packages)"
			case "unknown total":
				record.Total = nil
				want = "Waiting for installation: probe (download 3 bytes)"
			case "exceeded total":
				total = 2
				want = "Waiting for installation: probe (download 3 bytes)"
			case "negative count":
				completed = -1
				want = "Waiting for installation: probe (download)"
			case "unknown unit":
				record.Unit = "secrets"
				want = "Waiting for installation: probe (download)"
			case "stale":
				record.UpdatedAt = time.Now().Add(-time.Minute)
			case "future":
				record.UpdatedAt = time.Now().Add(time.Minute)
			case "wrong schema":
				record.Schema = "unknown"
			case "missing owner":
				record.Owner = ""
			case "invalid pid":
				record.PID = 0
			case "invalid phase":
				record.Phase = "finished"
			}
			switch name {
			case "stale", "future", "wrong schema", "missing owner", "invalid pid", "invalid phase", "malformed", "missing":
				want = "Waiting for installation: probe"
			}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if name == "malformed" {
				data = []byte("not-json")
			}
			if name != "missing" {
				if err := os.WriteFile(filepath.Join(dir, "probe.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close() }()
			previous := os.Stderr
			os.Stderr = writer
			defer func() { os.Stderr = previous; _ = writer.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() { _ = writer.Close() }()
				waitStatus(ctx, "probe.json", "probe")
			}()
			line, readErr := bufio.NewReader(reader).ReadString('\n')
			cancel()
			<-done
			if readErr != nil || line != want+"\n" {
				t.Fatalf("wait message = %q, error = %v; want %q", line, readErr, want)
			}
		})
	}
}

func TestStatusReleasePreservesReplacementOwner(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	if err := os.MkdirAll(env.GetCachePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	owner := &statusWriter{path: "probe.json", owner: "first", label: "probe", target: "apps/probe"}
	if !owner.write("install", nil, nil, "", true) {
		t.Fatal("could not publish owner status")
	}
	replacement := &statusWriter{path: "probe.json", owner: "second", label: "probe", target: "apps/probe"}
	if !replacement.write("verify", nil, nil, "", true) {
		t.Fatal("could not publish replacement status")
	}
	if err := owner.remove(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(env.GetCachePath(), "store-locks", "status", "probe.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got status
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Owner != "second" || got.Phase != "verify" {
		t.Fatalf("old release changed replacement status: %+v", got)
	}
}
