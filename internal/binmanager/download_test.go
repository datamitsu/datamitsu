package binmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

		filePath, err := downloadFile(server.URL, tmpDir)
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

		_, err := downloadFile(server.URL, tmpDir)
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

		_, err := downloadFile(server.URL, tmpDir)
		if err == nil {
			t.Error("expected error for 500 status, got nil")
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		server.Close()

		tmpDir := t.TempDir()
		_, err := downloadFile(server.URL, tmpDir)
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

		filePath, err := downloadFile(server.URL, nestedDir)
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

		_, err := downloadFile(server.URL, tmpDir)
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

		_, err := downloadFile(server.URL, tmpDir)
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

		_, err := downloadFile(server.URL, tmpDir)
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

		filePath, err := downloadAndVerify(server.URL, expectedHash, BinHashTypeSHA256, tmpDir)
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

		_, err := downloadAndVerify(server.URL, expectedHash, BinHashTypeSHA256, tmpDir)
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
		_, err := downloadAndVerify(server.URL, expectedHash, BinHashTypeSHA256, tmpDir)
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
		_, err := downloadFile(server.URL, tmpDir)
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
		_, err := downloadFile(redirectServer.URL, tmpDir)
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
		if err := os.WriteFile(srcPath, testContent, 0644); err != nil {
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
		if info.Mode().Perm() != 0755 {
			t.Errorf("incorrect permissions: got %o, want %o", info.Mode().Perm(), 0755)
		}
	})

	t.Run("creates destination directory", func(t *testing.T) {
		tmpDir := t.TempDir()

		srcPath := filepath.Join(tmpDir, "source.txt")
		if err := os.WriteFile(srcPath, []byte("test"), 0644); err != nil {
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

	t.Run("overwrites existing file", func(t *testing.T) {
		tmpDir := t.TempDir()

		srcPath := filepath.Join(tmpDir, "source.txt")
		newContent := []byte("new content")
		if err := os.WriteFile(srcPath, newContent, 0644); err != nil {
			t.Fatalf("failed to create source file: %v", err)
		}

		dstPath := filepath.Join(tmpDir, "dest.txt")
		oldContent := []byte("old content")
		if err := os.WriteFile(dstPath, oldContent, 0644); err != nil {
			t.Fatalf("failed to create destination file: %v", err)
		}

		if err := moveFile(srcPath, dstPath); err != nil {
			t.Fatalf("moveFile() error = %v", err)
		}

		content, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("failed to read destination file: %v", err)
		}
		if string(content) != string(newContent) {
			t.Errorf("content not overwritten: got %q, want %q", string(content), string(newContent))
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
