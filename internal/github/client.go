// Package github provides a minimal GitHub API client for fetching release
// metadata and assets used by datamitsu's binary distribution.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/datamitsu/datamitsu/internal/httpretry"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/releaseasset"
)

// DefaultBaseURL is the GitHub REST API root.
const DefaultBaseURL = "https://api.github.com"

// Asset is the provider-neutral asset consumed by runtime and release tooling.
type Asset = releaseasset.Asset

// Release is the shared release metadata used for version selection.
type Release = releaseasset.Release

// Client is a GitHub API client. Every request is retried under the shared
// httpretry policy: network failures, 5xx and rate limits that name a short
// wait are repeated, a 404 or a rate limit with no wait in sight is not.
type Client struct {
	httpClient *http.Client
	token      string

	// BaseURL is the API root, DefaultBaseURL unless a test points it elsewhere.
	BaseURL string
	// RetryNotifier is told about every repeated request; nil retries silently.
	RetryNotifier httpretry.Notifier
}

// NewClient creates a new GitHub API client
func NewClient() *Client {
	return NewClientForSource(DefaultBaseURL, os.Getenv("GITHUB_TOKEN")) //nolint:forbidigo // third-party token, not a datamitsu env var
}

// NewClientForSource binds an explicit credential to a configured GitHub API origin.
func NewClientForSource(baseURL, token string) *Client {
	c := &Client{httpClient: httpx.NewHardenedClient(30 * time.Second), token: token, BaseURL: baseURL}
	guard := c.httpClient.CheckRedirect
	c.httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := guard(req, via); err != nil {
			return err
		}
		origin, err := url.Parse(c.BaseURL)
		if err != nil {
			return fmt.Errorf("parse GitHub API origin: %w", err)
		}
		if !httpx.SameOrigin(req.URL.String(), origin.String()) {
			req.Header.Del("Authorization")
		}
		return nil
	}
	return c
}

// GetRelease fetches a specific release by tag
func (c *Client) GetRelease(ctx context.Context, owner, repo, tag string) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", c.BaseURL, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(tag))
	return c.fetchRelease(ctx, url)
}

// GetLatestRelease fetches the latest release
func (c *Client) GetLatestRelease(ctx context.Context, owner, repo string) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.BaseURL, owner, repo)
	return c.fetchRelease(ctx, url)
}

// releasePageSize is the page size of a release listing. maxReleasePages is
// how many pages GetLatestReleaseWithMinAge reads before it gives up: a
// repository that publishes nightly or early-access builds as prereleases can
// push its last stable release off the first page.
const (
	releasePageSize = 30
	maxReleasePages = 10
)

// ListReleases fetches up to perPage releases for a repo, with retry logic.
func (c *Client) ListReleases(ctx context.Context, owner, repo string, perPage int) ([]Release, error) {
	return c.listReleasesPage(ctx, owner, repo, perPage, 1)
}

// GetLatestReleaseWithMinAge returns the highest-semver stable release that is
// at least minAgeMinutes old. It lists recent releases and selects by semantic
// version (never by publish date) via selectLatestStableRelease, so a patch
// backported to an older branch cannot mask a newer minor/major. Prereleases,
// drafts, and releases with a zero PublishedAt are skipped. When a page holds
// no qualifying release it reads the next, up to maxReleasePages.
//
// When minAgeMinutes <= 0 the age cutoff is disabled but selection stays
// semver-based; if the list yields no stable release at all (degenerate repo)
// it falls back to GitHub's own "latest" designation. Under an active cutoff
// that excludes every release it returns (nil, nil) — a documented, non-error
// outcome callers branch on.
func (c *Client) GetLatestReleaseWithMinAge(ctx context.Context, owner, repo string, minAgeMinutes int) (*Release, error) {
	now := time.Now()
	for page := 1; page <= maxReleasePages; page++ {
		releases, err := c.listReleasesPage(ctx, owner, repo, releasePageSize, page)
		if err != nil {
			return nil, err
		}
		// Nothing on the earlier pages qualified, so selecting within this
		// page is the same as selecting over every release read so far.
		if sel := selectLatestStableRelease(releases, minAgeMinutes, now); sel != nil {
			return sel, nil
		}
		if len(releases) < releasePageSize {
			break
		}
	}

	if minAgeMinutes <= 0 {
		return c.GetLatestRelease(ctx, owner, repo)
	}
	return nil, nil //nolint:nilnil
}

// NotFoundError is returned when a release is not found
type NotFoundError struct {
	URL string
}

func (e *NotFoundError) Error() string {
	return "release not found: " + e.URL
}

// RateLimitError is returned when GitHub answers 403 or 429. Wait is what the
// response asked for — Retry-After, or the time to the primary limit's reset —
// and zero when it named none, in which case the request is not retried.
type RateLimitError struct {
	Wait  time.Duration
	Reset time.Time
}

func (e *RateLimitError) Error() string {
	msg := "GitHub API rate limit exceeded"
	switch {
	case !e.Reset.IsZero():
		msg += fmt.Sprintf(" (resets at %s)", e.Reset.Local().Format(time.TimeOnly))
	case e.Wait > 0:
		msg += fmt.Sprintf(" (asked to retry after %s)", e.Wait.Round(time.Second))
	}
	return msg + ". Set GITHUB_TOKEN environment variable for higher limits."
}

// RetryAfter is the wait the response asked for; see httpretry.RetryAfterError.
func (e *RateLimitError) RetryAfter() time.Duration { return e.Wait }

// Repository represents a GitHub repository
type Repository struct {
	FullName    string `json:"full_name"`
	Description string `json:"description"`
}

// GetRepository fetches repository metadata
func (c *Client) GetRepository(ctx context.Context, owner, repo string) (*Repository, error) {
	url := fmt.Sprintf("%s/repos/%s/%s", c.BaseURL, owner, repo)
	return c.fetchRepository(ctx, url)
}

// listReleasesPage fetches one page of a repo's releases. The first page's URL
// carries no page parameter, the same request ListReleases has always made.
func (c *Client) listReleasesPage(ctx context.Context, owner, repo string, perPage, page int) ([]Release, error) {
	if perPage <= 0 {
		perPage = releasePageSize
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d", c.BaseURL, owner, repo, perPage)
	if page > 1 {
		url += "&page=" + strconv.Itoa(page)
	}

	var releases []Release
	if err := c.getJSON(ctx, url, &releases); err != nil {
		return nil, err
	}
	return releases, nil
}

func (c *Client) fetchRepository(ctx context.Context, url string) (*Repository, error) {
	var repository Repository
	if err := c.getJSON(ctx, url, &repository); err != nil {
		return nil, err
	}
	return &repository, nil
}

// fetchRelease fetches a release from the given URL with retry logic
func (c *Client) fetchRelease(ctx context.Context, url string) (*Release, error) {
	var release Release
	if err := c.getJSON(ctx, url, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// doRequest performs one attempt at fetching a release, without retries.
func (c *Client) doRequest(ctx context.Context, url string) (*Release, error) {
	var release Release
	if err := c.doJSONRequest(ctx, url, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// getJSON performs a GET under the retry policy and decodes the response into target.
func (c *Client) getJSON(ctx context.Context, url string, target any) error {
	if err := httpx.GuardOffline("GitHub API request"); err != nil {
		return err
	}
	return httpretry.Retry(ctx, "GET "+url, c.RetryNotifier, func() error {
		return c.doJSONRequest(ctx, url, target)
	})
}

// doJSONRequest performs one GET and decodes the JSON response into target. It
// classifies the outcome for httpretry: a 404 and a 4xx other than a rate limit
// are permanent, a rate limit carries the wait the response named.
func (c *Client) doJSONRequest(ctx context.Context, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return httpretry.Permanent(fmt.Errorf("failed to create request: %w", err))
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

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return httpretry.Permanent(&NotFoundError{URL: url})
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		limit := rateLimitFromResponse(resp, time.Now())
		if limit.Wait == 0 {
			return httpretry.Permanent(limit)
		}
		return limit
	case resp.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		err := fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
		if httpretry.RetryableStatus(resp.StatusCode) {
			return httpretry.WithRetryAfter(err, resp)
		}
		return httpretry.Permanent(err)
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return httpretry.ClassifyDecode(fmt.Errorf("failed to decode response: %w", err))
	}

	return nil
}

// rateLimitFromResponse reads the wait a 403 or 429 asks for: Retry-After in
// seconds (a secondary limit), or X-Ratelimit-Reset once X-Ratelimit-Remaining
// is 0 (the primary limit). A response with neither names no wait.
func rateLimitFromResponse(resp *http.Response, now time.Time) *RateLimitError {
	limit := &RateLimitError{}
	if wait := httpretry.RetryAfterHeader(resp.Header, now); wait > 0 {
		limit.Wait = wait
		return limit
	}
	if resp.Header.Get("X-Ratelimit-Remaining") != "0" {
		return limit
	}
	reset, err := strconv.ParseInt(resp.Header.Get("X-Ratelimit-Reset"), 10, 64)
	if err != nil {
		return limit
	}
	limit.Reset = time.Unix(reset, 0)
	// A reset already in the past still names the limit: retry at once.
	limit.Wait = max(limit.Reset.Sub(now), time.Second)
	return limit
}

// RateLimitFromResponse exposes GitHub's primary and secondary limit handling to release providers.
func RateLimitFromResponse(resp *http.Response, now time.Time) *RateLimitError {
	return rateLimitFromResponse(resp, now)
}
