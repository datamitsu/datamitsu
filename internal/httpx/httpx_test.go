package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewHardenedClientTimeout(t *testing.T) {
	const want = 90 * time.Second
	c := NewHardenedClient(want)
	if c.Timeout != want {
		t.Fatalf("Timeout = %v, want %v", c.Timeout, want)
	}
}

func TestNewHardenedClientTransport(t *testing.T) {
	c := NewHardenedClient(time.Minute)
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", c.Transport)
	}
	if tr.Proxy == nil {
		t.Error("Proxy is nil, want http.ProxyFromEnvironment")
	}
	if tr.DialContext == nil {
		t.Error("DialContext is nil, want a dialer with a timeout")
	}
	if tr.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 10s", tr.TLSHandshakeTimeout)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 30s", tr.ResponseHeaderTimeout)
	}
	if c.CheckRedirect == nil {
		t.Error("CheckRedirect is nil, want the hardened redirect policy")
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestHardenedRedirectPolicy(t *testing.T) {
	httpsReq := &http.Request{URL: mustParse(t, "https://example.com/a")}
	httpReq := &http.Request{URL: mustParse(t, "http://example.com/a")}

	t.Run("allows https to https", func(t *testing.T) {
		via := []*http.Request{{URL: mustParse(t, "https://example.com/start")}}
		if err := hardenedRedirect(httpsReq, via); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects https to http downgrade", func(t *testing.T) {
		via := []*http.Request{{URL: mustParse(t, "https://example.com/start")}}
		err := hardenedRedirect(httpReq, via)
		if err == nil {
			t.Fatal("expected error for HTTPS to HTTP downgrade, got nil")
		}
		if !strings.Contains(err.Error(), "HTTPS to HTTP redirect rejected") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects after 10 redirects", func(t *testing.T) {
		via := make([]*http.Request, 10)
		for i := range via {
			via[i] = &http.Request{URL: mustParse(t, "https://example.com/hop")}
		}
		err := hardenedRedirect(httpsReq, via)
		if err == nil {
			t.Fatal("expected error after 10 redirects, got nil")
		}
		if !strings.Contains(err.Error(), "stopped after 10 redirects") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// TestHardenedClientRejectsDowngradeEndToEnd exercises the redirect policy
// through a real client + server so the wired-up CheckRedirect is covered, not
// just the bare function.
func TestHardenedClientRejectsDowngradeEndToEnd(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer httpServer.Close()

	httpsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, httpServer.URL, http.StatusFound)
	}))
	defer httpsServer.Close()

	// Trust the test TLS cert but keep the hardened redirect policy.
	client := NewHardenedClient(30 * time.Second)
	client.Transport = httpsServer.Client().Transport

	resp, err := client.Get(httpsServer.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected error for HTTPS to HTTP redirect, got nil")
	}
	if !strings.Contains(err.Error(), "HTTPS to HTTP redirect rejected") {
		t.Errorf("unexpected error: %v", err)
	}
}
