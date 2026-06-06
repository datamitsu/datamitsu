package binmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadFile(t *testing.T) {
	testContent := "test file content"

	t.Run("successful download", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(testContent))
		}))
		defer server.Close()

		tmpDir := t.TempDir()

		filePath, err := downloadFile(context.Background(), server.URL, tmpDir)
		if err != nil {
			t.Fatalf("downloadFile() error = %v", err)
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("downloaded file does not exist: %s", filePath)
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read downloaded file: %v", err)
		}
		if string(content) != testContent {
			t.Errorf("content mismatch: got %q, want %q", string(content), testContent)
		}

		if filepath.Dir(filePath) != tmpDir {
			t.Errorf("file not in temp dir: got %s, want %s", filepath.Dir(filePath), tmpDir)
		}
	})

	t.Run("HTTP 404 error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		tmpDir := t.TempDir()

		_, err := downloadFile(context.Background(), server.URL, tmpDir)
		if err == nil {
			t.Error("expected error for 404 status, got nil")
		}
	})

	t.Run("HTTP 500 error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		tmpDir := t.TempDir()

		_, err := downloadFile(context.Background(), server.URL, tmpDir)
		if err == nil {
			t.Error("expected error for 500 status, got nil")
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		server.Close()

		tmpDir := t.TempDir()
		_, err := downloadFile(context.Background(), server.URL, tmpDir)
		if err == nil {
			t.Error("expected error for refused connection, got nil")
		}
	})

	t.Run("creates destination directory", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(testContent))
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		nestedDir := filepath.Join(tmpDir, "nested", "dir")

		filePath, err := downloadFile(context.Background(), server.URL, nestedDir)
		if err != nil {
			t.Fatalf("downloadFile() error = %v", err)
		}

		if _, err := os.Stat(nestedDir); os.IsNotExist(err) {
			t.Error("destination directory was not created")
		}

		if filepath.Dir(filePath) != nestedDir {
			t.Errorf("file not in nested dir: got %s, want %s", filepath.Dir(filePath), nestedDir)
		}
	})
}

func TestDownloadFileSizeLimit(t *testing.T) {
	t.Run("rejects oversized Content-Length before download", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", MaxBinarySize+1))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("small body"))
		}))
		defer server.Close()

		tmpDir := t.TempDir()

		_, err := downloadFile(context.Background(), server.URL, tmpDir)
		if err == nil {
			t.Fatal("expected error for oversized Content-Length, got nil")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Errorf("unexpected error message: %v", err)
		}

		files, _ := os.ReadDir(tmpDir)
		if len(files) > 0 {
			t.Error("temporary file was not cleaned up after size rejection")
		}
	})

	t.Run("accepts file with Content-Length below MaxBinarySize", func(t *testing.T) {
		body := []byte("small file content")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		}))
		defer server.Close()

		tmpDir := t.TempDir()

		_, err := downloadFile(context.Background(), server.URL, tmpDir)
		if err != nil {
			t.Fatalf("expected no error for Content-Length below MaxBinarySize, got: %v", err)
		}
	})

	t.Run("rejects body that exceeds MaxBinarySize without Content-Length", func(t *testing.T) {
		oversized := make([]byte, MaxBinarySize+1024)
		for i := range oversized {
			oversized[i] = 'x'
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(oversized)
		}))
		defer server.Close()

		tmpDir := t.TempDir()

		_, err := downloadFile(context.Background(), server.URL, tmpDir)
		if err == nil {
			t.Fatal("expected error for oversized download without Content-Length, got nil")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Errorf("unexpected error message: %v", err)
		}

		files, _ := os.ReadDir(tmpDir)
		if len(files) > 0 {
			t.Error("temporary file was not cleaned up after size rejection")
		}
	})
}

func TestDownloadAndVerify(t *testing.T) {
	testContent := "test file content for hash verification"
	hash := sha256.Sum256([]byte(testContent))
	expectedHash := hex.EncodeToString(hash[:])

	t.Run("successful download and verification", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(testContent)); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		}))
		defer server.Close()

		tmpDir := t.TempDir()

		filePath, err := downloadAndVerify(context.Background(), server.URL, expectedHash, BinHashTypeSHA256, tmpDir)
		if err != nil {
			t.Fatalf("downloadAndVerify() error = %v", err)
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("downloaded file does not exist: %s", filePath)
		}
	})

	t.Run("hash verification fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("different content")); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		}))
		defer server.Close()

		tmpDir := t.TempDir()

		_, err := downloadAndVerify(context.Background(), server.URL, expectedHash, BinHashTypeSHA256, tmpDir)
		if err == nil {
			t.Error("expected hash verification error, got nil")
		}

		files, _ := os.ReadDir(tmpDir)
		if len(files) > 0 {
			t.Error("temporary file was not cleaned up after failed verification")
		}
	})

	t.Run("download fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		server.Close()

		tmpDir := t.TempDir()
		_, err := downloadAndVerify(context.Background(), server.URL, expectedHash, BinHashTypeSHA256, tmpDir)
		if err == nil {
			t.Error("expected download error, got nil")
		}
	})
}

func TestRedirectPolicy(t *testing.T) {
	t.Run("rejects HTTPS to HTTP downgrade", func(t *testing.T) {
		httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("content"))
		}))
		defer httpServer.Close()

		httpsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, httpServer.URL, http.StatusFound)
		}))
		defer httpsServer.Close()

		// Use a client with the TLS test certificate but with our redirect policy.
		// httpClient uses its own Transport, so we create a test-specific client
		// that shares our CheckRedirect policy but trusts the test TLS cert.
		tlsClient := &http.Client{
			Transport:     httpsServer.Client().Transport,
			CheckRedirect: httpClient.CheckRedirect,
		}

		resp, err := tlsClient.Get(httpsServer.URL)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatal("expected error for HTTPS to HTTP redirect, got nil")
		}
		if !strings.Contains(err.Error(), "HTTPS to HTTP redirect rejected") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects after 10 redirects", func(t *testing.T) {
		redirectCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			redirectCount++
			http.Redirect(w, r, "/", http.StatusFound)
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		_, err := downloadFile(context.Background(), server.URL, tmpDir)
		if err == nil {
			t.Fatal("expected error after too many redirects, got nil")
		}
		if !strings.Contains(err.Error(), "redirect") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("allows HTTP to HTTP redirect", func(t *testing.T) {
		finalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("final content"))
		}))
		defer finalServer.Close()

		redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, finalServer.URL, http.StatusFound)
		}))
		defer redirectServer.Close()

		tmpDir := t.TempDir()
		_, err := downloadFile(context.Background(), redirectServer.URL, tmpDir)
		if err != nil {
			t.Fatalf("expected success for HTTP to HTTP redirect, got: %v", err)
		}
	})
}

func TestMoveFile(t *testing.T) {
	t.Run("successful move", func(t *testing.T) {
		tmpDir := t.TempDir()

		srcPath := filepath.Join(tmpDir, "source.txt")
		testContent := []byte("test content")
		if err := os.WriteFile(srcPath, testContent, 0o644); err != nil {
			t.Fatalf("failed to create source file: %v", err)
		}

		dstPath := filepath.Join(tmpDir, "dest.txt")
		if err := moveFile(srcPath, dstPath); err != nil {
			t.Fatalf("moveFile() error = %v", err)
		}

		if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
			t.Error("source file still exists after move")
		}

		content, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("failed to read destination file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(testContent))
		}

		info, err := os.Stat(dstPath)
		if err != nil {
			t.Fatalf("failed to stat destination file: %v", err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("incorrect permissions: got %o, want %o", info.Mode().Perm(), 0o755)
		}
	})

	t.Run("creates destination directory", func(t *testing.T) {
		tmpDir := t.TempDir()

		srcPath := filepath.Join(tmpDir, "source.txt")
		if err := os.WriteFile(srcPath, []byte("test"), 0o644); err != nil {
			t.Fatalf("failed to create source file: %v", err)
		}

		dstPath := filepath.Join(tmpDir, "nested", "dir", "dest.txt")
		if err := moveFile(srcPath, dstPath); err != nil {
			t.Fatalf("moveFile() error = %v", err)
		}

		if _, err := os.Stat(filepath.Dir(dstPath)); os.IsNotExist(err) {
			t.Error("destination directory was not created")
		}

		if _, err := os.Stat(dstPath); os.IsNotExist(err) {
			t.Error("destination file does not exist")
		}
	})

	t.Run("skips when destination exists (content-addressed no-op)", func(t *testing.T) {
		tmpDir := t.TempDir()

		srcPath := filepath.Join(tmpDir, "source.txt")
		if err := os.WriteFile(srcPath, []byte("new content"), 0o644); err != nil {
			t.Fatalf("failed to create source file: %v", err)
		}

		dstPath := filepath.Join(tmpDir, "dest.txt")
		existing := []byte("existing content")
		if err := os.WriteFile(dstPath, existing, 0o644); err != nil {
			t.Fatalf("failed to create destination file: %v", err)
		}

		if err := moveFile(srcPath, dstPath); err != nil {
			t.Fatalf("moveFile() error = %v", err)
		}

		// dst must be left intact (never removed); src cleaned up.
		content, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("failed to read destination file: %v", err)
		}
		if string(content) != string(existing) {
			t.Errorf("existing dst was modified: got %q, want %q", string(content), string(existing))
		}
		if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
			t.Error("source file still exists after skip")
		}
	})

	t.Run("concurrent moves to same dst never expose missing dst", func(t *testing.T) {
		tmpDir := t.TempDir()
		dstPath := filepath.Join(tmpDir, "dest.bin")

		const movers = 8
		var wg sync.WaitGroup

		// Reader loops, asserting dst is never observed missing once it first appears.
		stop := make(chan struct{})
		var appeared atomic.Bool
		readErr := make(chan error, 1)
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := os.Stat(dstPath); err == nil {
					appeared.Store(true)
				} else if appeared.Load() && os.IsNotExist(err) {
					select {
					case readErr <- fmt.Errorf("dst disappeared after appearing"):
					default:
					}
					return
				}
			}
		}()

		wg.Add(movers)
		for i := 0; i < movers; i++ {
			go func(i int) {
				defer wg.Done()
				srcPath := filepath.Join(tmpDir, fmt.Sprintf("src-%d.bin", i))
				if err := os.WriteFile(srcPath, []byte("payload"), 0o644); err != nil {
					return
				}
				_ = moveFile(srcPath, dstPath)
			}(i)
		}

		wg.Wait()
		close(stop)

		select {
		case err := <-readErr:
			t.Fatal(err)
		default:
		}

		if _, err := os.Stat(dstPath); err != nil {
			t.Fatalf("dst missing after concurrent moves: %v", err)
		}
	})

	t.Run("source file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()

		srcPath := filepath.Join(tmpDir, "nonexistent.txt")
		dstPath := filepath.Join(tmpDir, "dest.txt")

		err := moveFile(srcPath, dstPath)
		if err == nil {
			t.Error("expected error for nonexistent source file, got nil")
		}
	})
}

func TestCopyFileAtomic(t *testing.T) {
	t.Run("copies content and marks executable", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcPath := filepath.Join(tmpDir, "source.bin")
		payload := []byte("binary payload")
		if err := os.WriteFile(srcPath, payload, 0o644); err != nil {
			t.Fatalf("failed to create source file: %v", err)
		}

		dstPath := filepath.Join(tmpDir, "dest.bin")
		if err := copyFileAtomic(srcPath, dstPath); err != nil {
			t.Fatalf("copyFileAtomic() error = %v", err)
		}

		content, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("failed to read destination file: %v", err)
		}
		if string(content) != string(payload) {
			t.Errorf("content mismatch: got %q, want %q", string(content), string(payload))
		}

		info, err := os.Stat(dstPath)
		if err != nil {
			t.Fatalf("failed to stat destination file: %v", err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("incorrect permissions: got %o, want %o", info.Mode().Perm(), 0o755)
		}
	})

	t.Run("leaves no temp file behind on source error", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcPath := filepath.Join(tmpDir, "nonexistent.bin")
		dstPath := filepath.Join(tmpDir, "dest.bin")

		if err := copyFileAtomic(srcPath, dstPath); err == nil {
			t.Fatal("expected error for nonexistent source, got nil")
		}

		entries, err := os.ReadDir(tmpDir)
		if err != nil {
			t.Fatalf("failed to read temp dir: %v", err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "move-") {
				t.Errorf("leftover temp file: %s", e.Name())
			}
		}
	})
}

func TestMoveDir(t *testing.T) {
	writeDir := func(t *testing.T, dir, marker string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(marker), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	t.Run("successful move", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcDir := filepath.Join(tmpDir, "src")
		writeDir(t, srcDir, "hello")

		dstDir := filepath.Join(tmpDir, "out", "dst")
		if err := moveDir(srcDir, dstDir); err != nil {
			t.Fatalf("moveDir() error = %v", err)
		}
		content, err := os.ReadFile(filepath.Join(dstDir, "f.txt"))
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		if string(content) != "hello" {
			t.Errorf("content = %q, want hello", content)
		}
		if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
			t.Error("src still exists after move")
		}
	})

	t.Run("skips when destination exists (content-addressed no-op)", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcDir := filepath.Join(tmpDir, "src")
		writeDir(t, srcDir, "new")
		dstDir := filepath.Join(tmpDir, "dst")
		writeDir(t, dstDir, "existing")

		if err := moveDir(srcDir, dstDir); err != nil {
			t.Fatalf("moveDir() error = %v", err)
		}
		content, err := os.ReadFile(filepath.Join(dstDir, "f.txt"))
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		if string(content) != "existing" {
			t.Errorf("existing dst modified: got %q, want existing", content)
		}
		if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
			t.Error("src still exists after skip")
		}
	})

	t.Run("concurrent moves to same dst never expose missing dst", func(t *testing.T) {
		tmpDir := t.TempDir()
		dstDir := filepath.Join(tmpDir, "dst")

		const movers = 8
		var wg sync.WaitGroup

		stop := make(chan struct{})
		var appeared atomic.Bool
		readErr := make(chan error, 1)
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := os.Stat(dstDir); err == nil {
					appeared.Store(true)
				} else if appeared.Load() && os.IsNotExist(err) {
					select {
					case readErr <- fmt.Errorf("dst disappeared after appearing"):
					default:
					}
					return
				}
			}
		}()

		wg.Add(movers)
		for i := 0; i < movers; i++ {
			go func(i int) {
				defer wg.Done()
				srcDir := filepath.Join(tmpDir, fmt.Sprintf("src-%d", i))
				if err := os.MkdirAll(srcDir, 0o755); err != nil {
					return
				}
				if err := os.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("payload"), 0o644); err != nil {
					return
				}
				_ = moveDir(srcDir, dstDir)
			}(i)
		}

		wg.Wait()
		close(stop)

		select {
		case err := <-readErr:
			t.Fatal(err)
		default:
		}
		if _, err := os.Stat(dstDir); err != nil {
			t.Fatalf("dst missing after concurrent moves: %v", err)
		}
	})
}

func TestCopyDirAtomic(t *testing.T) {
	t.Run("copies tree into fresh dst", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcDir := filepath.Join(tmpDir, "src")
		if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "sub", "f.txt"), []byte("payload"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		dstDir := filepath.Join(tmpDir, "dst")
		if err := copyDirAtomic(srcDir, dstDir); err != nil {
			t.Fatalf("copyDirAtomic() error = %v", err)
		}
		content, err := os.ReadFile(filepath.Join(dstDir, "sub", "f.txt"))
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		if string(content) != "payload" {
			t.Errorf("content = %q, want payload", content)
		}
	})

	t.Run("treats pre-existing dst as success", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcDir := filepath.Join(tmpDir, "src")
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("new"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		dstDir := filepath.Join(tmpDir, "dst")
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			t.Fatalf("mkdir dst: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, "f.txt"), []byte("existing"), 0o644); err != nil {
			t.Fatalf("write dst: %v", err)
		}

		if err := copyDirAtomic(srcDir, dstDir); err != nil {
			t.Fatalf("copyDirAtomic() error = %v", err)
		}
		content, err := os.ReadFile(filepath.Join(dstDir, "f.txt"))
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		if string(content) != "existing" {
			t.Errorf("existing dst modified: got %q, want existing", content)
		}
	})
}

func TestNewInstallContext(t *testing.T) {
	t.Run("timeout disabled means no deadline", func(t *testing.T) {
		t.Setenv("DATAMITSU_INSTALL_TIMEOUT", "0")

		ctx, cancel, sec := newInstallContext(context.Background())
		defer cancel()

		if _, ok := ctx.Deadline(); ok {
			t.Error("expected no deadline when install timeout is disabled (0)")
		}
		if sec != 0 {
			t.Errorf("timeoutSec = %d, want 0", sec)
		}
	})

	t.Run("positive timeout sets a deadline", func(t *testing.T) {
		t.Setenv("DATAMITSU_INSTALL_TIMEOUT", "120")

		ctx, cancel, sec := newInstallContext(context.Background())
		defer cancel()

		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected a deadline for a positive install timeout")
		}
		if sec != 120 {
			t.Errorf("timeoutSec = %d, want 120", sec)
		}
		if remaining := time.Until(dl); remaining <= 0 || remaining > 120*time.Second {
			t.Errorf("unexpected deadline remaining: %v", remaining)
		}
	})
}

func TestWrapInstallTimeout(t *testing.T) {
	if got := wrapInstallTimeout(nil, 600); got != nil {
		t.Errorf("nil error should pass through, got %v", got)
	}

	other := errors.New("boom")
	if got := wrapInstallTimeout(other, 600); got != other {
		t.Errorf("non-timeout error should pass through unchanged, got %v", got)
	}

	timeoutErr := fmt.Errorf("download stalled: %w", context.DeadlineExceeded)
	got := wrapInstallTimeout(timeoutErr, 5)
	if got == nil || !strings.Contains(got.Error(), "installation timed out after 5s") {
		t.Errorf("expected a timeout message, got %v", got)
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("wrapped timeout should still match DeadlineExceeded, got %v", got)
	}
}

func TestDownloadFileContextTimeout(t *testing.T) {
	// The handler sends headers plus a partial chunk, then blocks until the
	// client's context is canceled — forcing the body read to time out
	// mid-download so we can assert the partial file is cleaned up.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial-payload"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := downloadFile(ctx, server.URL, tmpDir)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}

	// The partial download must be removed — no leftover temp files leak.
	files, _ := os.ReadDir(tmpDir)
	if len(files) > 0 {
		t.Errorf("temp file not cleaned up after timeout: %v", files)
	}
}
