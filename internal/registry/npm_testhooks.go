package registry

import "net/http"

// SetNPMRegistryBaseURLForTesting overrides the npm registry base URL and
// returns the previous value so callers can restore it. It exists so tests in
// other packages (e.g. cmd) can point npm fetches at an httptest server.
func SetNPMRegistryBaseURLForTesting(url string) string {
	prev := npmRegistryBaseURL
	npmRegistryBaseURL = url
	return prev
}

// SetNPMClientForTesting overrides the npm HTTP client and returns the
// previous client so callers can restore it.
func SetNPMClientForTesting(client *http.Client) *http.Client {
	prev := npmHTTPClient
	npmHTTPClient = client
	return prev
}
