package registry

import "net/http"

// SetPyPIBaseURLForTesting overrides the PyPI registry base URL and returns the
// previous value so callers can restore it. It exists so tests in other
// packages (e.g. cmd) can point PyPI fetches at an httptest server.
func SetPyPIBaseURLForTesting(url string) string {
	prev := pypiBaseURL
	pypiBaseURL = url
	return prev
}

// SetPyPIClientForTesting overrides the PyPI HTTP client and returns the
// previous client so callers can restore it.
func SetPyPIClientForTesting(client *http.Client) *http.Client {
	prev := pypiHTTPClient
	pypiHTTPClient = client
	return prev
}
