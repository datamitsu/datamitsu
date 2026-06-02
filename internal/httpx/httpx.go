// Package httpx provides the single hardened *http.Client constructor shared by
// every datamitsu code path that downloads artifacts or fetches metadata over
// the network (binary downloads, runtime/pnpm/JAR installs, the pull-runtimes
// devtools, and remote-config fetches).
//
// Centralizing the proxy, dialer, TLS-handshake, and response-header timeouts
// plus the redirect guard keeps all of those paths on one audited security
// posture: a redirect chain is capped, and an HTTPS→HTTP downgrade is refused so
// a redirect can never silently strip TLS. The downloaded bytes are still
// verified against a pinned hash by the caller (see CLAUDE.md) — this client
// hardens only the transport.
package httpx

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// NewHardenedClient returns an *http.Client with datamitsu's standard hardened
// transport and redirect policy. timeout sets the overall per-request deadline;
// the dialer, TLS-handshake, and response-header sub-timeouts are fixed shared
// defaults so every call site downloads with the same posture and only the
// overall budget varies.
func NewHardenedClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		CheckRedirect: hardenedRedirect,
	}
}

// hardenedRedirect caps redirect chains at 10 hops and refuses any HTTPS→HTTP
// downgrade, so a redirect cannot silently drop the connection to plaintext.
func hardenedRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
		return fmt.Errorf("HTTPS to HTTP redirect rejected: %s", req.URL)
	}
	return nil
}
