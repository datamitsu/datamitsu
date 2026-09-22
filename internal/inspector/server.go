package inspector

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

const serverTimeout = 5 * time.Second

// Handler serves only the snapshot, never files from the working directory.
func Handler(html []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodGet {
			_, _ = w.Write(html)
		}
	})
}

// Serve shuts down when the CLI context is canceled, including while idle.
func Serve(ctx context.Context, listener net.Listener, html []byte) error {
	server := &http.Server{Handler: Handler(html), ReadHeaderTimeout: serverTimeout}
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), serverTimeout)
			defer cancel()
			if err := server.Shutdown(shutdown); err != nil {
				_ = server.Close()
			}
		case <-stopped:
		}
	}()
	err := server.Serve(listener)
	close(stopped)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("serve inspector: %w", err)
	}
	return nil
}
