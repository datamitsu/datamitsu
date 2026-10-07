package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/httpretry"
	"github.com/datamitsu/datamitsu/internal/registry"
)

// retryNotifier prints every repeated request, so a run that waits on a flaky
// network or a rate limit says so instead of pausing in silence.
func retryNotifier(a httpretry.Attempt) {
	fmt.Fprintf(os.Stderr, "  retry %d/%d for %s in %s: %v\n",
		a.Attempt+1, a.Max, a.What, a.Delay.Round(time.Millisecond), a.Err)
}

// newGitHubClient is the GitHub client every devtools command uses: its retries
// are shown, and a test can point it at its own server through
// githubBaseURL.
func newGitHubClient() *github.Client {
	client := github.NewClient()
	client.RetryNotifier = retryNotifier
	if githubBaseURL != "" {
		client.BaseURL = githubBaseURL
	}
	return client
}

// githubBaseURL overrides the GitHub API root; tests set it to an httptest server.
var githubBaseURL string

// enableRetryNotices routes registry and verification-download retries to
// stderr for the rest of the process.
func enableRetryNotices() {
	registry.RetryNotifier = retryNotifier
	binmanager.VerifyRetryNotifier = retryNotifier
}

// isRateLimited reports whether err came from a GitHub rate limit, the one
// failure whose fix is a setting rather than a retry.
func isRateLimited(err error) bool {
	var limit *github.RateLimitError
	return errors.As(err, &limit)
}

// httpGetLimited GETs url under the retry policy and returns up to maxSize
// bytes of the body.
func httpGetLimited(ctx context.Context, client *http.Client, url string, maxSize int64) ([]byte, error) {
	var data []byte
	err := httpretry.Retry(ctx, "GET "+url, retryNotifier, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return httpretry.Permanent(fmt.Errorf("build request %s: %w", url, err))
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("GET %s: %w", url, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
			if httpretry.RetryableStatus(resp.StatusCode) {
				return httpretry.WithRetryAfter(err, resp)
			}
			return httpretry.Permanent(err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
		if err != nil {
			return fmt.Errorf("reading %s: %w", url, err)
		}
		if int64(len(body)) > maxSize {
			return httpretry.Permanent(fmt.Errorf("%s exceeds maximum size of %d bytes", url, maxSize))
		}
		data = body
		return nil
	})
	return data, err
}
