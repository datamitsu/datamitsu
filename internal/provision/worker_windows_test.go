//go:build windows

package provision

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--internal-provision-worker" {
		os.Exit(runWindowsWorkerFixture())
	}
	os.Exit(m.Run())
}

func runWindowsWorkerFixture() int {
	rpc, live, cancelPipe, handshake, err := OpenWorkerPipes(os.Args[2:])
	if err != nil {
		return 10
	}
	defer func() { _ = rpc.Close(); _ = live.Close(); _ = cancelPipe.Close() }()
	if err := ReadStartupHandshake(handshake); err != nil {
		return 11
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, live); cancel() }()
	go func() {
		var b [1]byte
		if _, err := cancelPipe.Read(b[:]); err == nil {
			cancel()
		}
	}()
	err = ServeContext(ctx, rpc, func(ctx context.Context, _ string, payload json.RawMessage) (any, error) {
		var request struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			return nil, err
		}
		if err := Progress(ctx, struct {
			Ready bool `json:"ready"`
		}{true}); err != nil {
			return nil, err
		}
		<-ctx.Done()
		if err := os.WriteFile(request.Path, []byte("canceled"), 0o600); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return 12
	}
	return 0
}

func TestWindowsWorkerLivenessWaitsForOperationCleanup(t *testing.T) {
	worker, err := StartWorker(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close() })
	ready := make(chan struct{}, 1)
	worker.Client.SetProgress(func(json.RawMessage) {
		select {
		case ready <- struct{}{}:
		default:
		}
	})
	marker := filepath.Join(t.TempDir(), "finished")
	callDone := make(chan error, 1)
	go func() {
		callDone <- worker.Client.Call(context.Background(), "wait", struct {
			Path string `json:"path"`
		}{marker}, nil)
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("worker operation never started")
	}
	if err := worker.liveness.Close(); err != nil {
		t.Fatal(err)
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "canceled" {
		t.Fatalf("worker exited before operation cleanup: data=%q error=%v", data, err)
	}
	select {
	case err := <-callDone:
		if err == nil {
			t.Fatal("disconnected call unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("disconnected call remained blocked")
	}
}
