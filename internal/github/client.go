// Package github provides a minimal GitHub API client for fetching release
// metadata and assets used by datamitsu's binary distribution.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpx"
)

// Asset represents a GitHub release asset
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
	Digest             string `json:"digest,omitempty"` // SHA256 digest in format "sha256:hash"
}

// Release represents a GitHub release
type Release struct {
	TagName     string    `json:"tag_name"`
	Assets      []Asset   `json:"assets"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

// Client is a GitHub API client
type Client struct {
	httpClient *http.Client
	token      string
}

// NewClient creates a new GitHub API client
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		token: os.Getenv("GITHUB_TOKEN"), //nolint:forbidigo // third-party token, not a datamitsu env var
	}
}

// GetRelease fetches a specific release by tag
func (c *Client) GetRelease(ctx context.Context, owner, repo, tag string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	return c.fetchRelease(ctx, url)
}

// GetLatestRelease fetches the latest release
func (c *Client) GetLatestRelease(ctx context.Context, owner, repo string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	return c.fetchRelease(ctx, url)
}

// ListReleases fetches up to perPage releases for a repo, with retry logic.
func (c *Client) ListReleases(ctx context.Context, owner, repo string, perPage int) ([]Release, error) {
	if perPage <= 0 {
		perPage = 30
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=%d", owner, repo, perPage)

	var lastErr error
	maxRetries := 3
	backoff := time.Second

	for attempt := range maxRetries {
		if attempt > 0 {
			time.Sleep(backoff)
			backoff *= 2
		}

		var releases []Release
		err := c.doJSONRequest(ctx, url, &releases)
		if err == nil {
			return releases, nil
		}

		lastErr = err
		if isNonRetryableError(err) {
			break
		}
	}

	return nil, lastErr
}

// GetLatestReleaseWithMinAge returns the most recent release at least minAgeMinutes old.
// It fetches up to 30 releases and skips prereleases, drafts, and releases with a
// zero PublishedAt. When minAgeMinutes <= 0 it falls through to GetLatestRelease.
// Returns (nil, nil) when no release qualifies.
func (c *Client) GetLatestReleaseWithMinAge(ctx context.Context, owner, repo string, minAgeMinutes int) (*Release, error) {
	if minAgeMinutes <= 0 {
		return c.GetLatestRelease(ctx, owner, repo)
	}

	releases, err := c.ListReleases(ctx, owner, repo, 30)
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().Add(-time.Duration(minAgeMinutes) * time.Minute)
	for i := range releases {
		r := releases[i]
		if r.Prerelease || r.Draft {
			continue
		}
		if r.PublishedAt.IsZero() {
			continue
		}
		if r.PublishedAt.After(cutoff) {
			continue
		}
		return &r, nil
	}

	// No release qualifies under the min-age cutoff; callers branch on a nil
	// release as a normal, non-error outcome (documented contract).
	return nil, nil //nolint:nilnil
}

// NotFoundError is returned when a release is not found
type NotFoundError struct {
	URL string
}

func (e *NotFoundError) Error() string {
	return "release not found: " + e.URL
}

// RateLimitError is returned when rate limit is exceeded
type RateLimitError struct{}

func (e *RateLimitError) Error() string {
	return "GitHub API rate limit exceeded. Set GITHUB_TOKEN environment variable for higher limits."
}

// Repository represents a GitHub repository
type Repository struct {
	FullName    string `json:"full_name"`
	Description string `json:"description"`
}

// GetRepository fetches repository metadata
func (c *Client) GetRepository(ctx context.Context, owner, repo string) (*Repository, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	return c.fetchRepository(ctx, url)
}

func (c *Client) fetchRepository(ctx context.Context, url string) (*Repository, error) {
	if err := httpx.GuardOffline("GitHub API request"); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{URL: url}
	}

	if resp.StatusCode == http.StatusForbidden {
		return nil, &RateLimitError{}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var repository Repository
	if err := json.NewDecoder(resp.Body).Decode(&repository); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &repository, nil
}

// isNonRetryableError checks if an error should not be retried
func isNonRetryableError(err error) bool {
	{
		var errCase0 *NotFoundError
		var errCase1 *RateLimitError
		switch {
		case errors.As(err, &errCase0), errors.As(err, &errCase1):
			return true
		default:
			return false
		}
	}
}

// fetchRelease fetches a release from the given URL with retry logic
func (c *Client) fetchRelease(ctx context.Context, url string) (*Release, error) {
	var lastErr error
	maxRetries := 3
	backoff := time.Second

	for attempt := range maxRetries {
		if attempt > 0 {
			time.Sleep(backoff)
			backoff *= 2
		}

		release, err := c.doRequest(ctx, url)
		if err == nil {
			return release, nil
		}

		lastErr = err

		// Don't retry on 404 or 403
		if isNonRetryableError(err) {
			break
		}
	}

	return nil, lastErr
}

// doRequest performs the actual HTTP request for a single release
func (c *Client) doRequest(ctx context.Context, url string) (*Release, error) {
	var release Release
	if err := c.doJSONRequest(ctx, url, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// doJSONRequest performs a GET request and decodes the JSON response into target.
func (c *Client) doJSONRequest(ctx context.Context, url string, target any) error {
	if err := httpx.GuardOffline("GitHub API request"); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Best effort - log but don't fail the request
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return &NotFoundError{URL: url}
	}

	if resp.StatusCode == http.StatusForbidden {
		return &RateLimitError{}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}
