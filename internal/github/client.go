package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
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
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
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
		token: os.Getenv("GITHUB_TOKEN"),
	}
}

// GetRelease fetches a specific release by tag
func (c *Client) GetRelease(owner, repo, tag string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	return c.fetchRelease(url)
}

// GetLatestRelease fetches the latest release
func (c *Client) GetLatestRelease(owner, repo string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	return c.fetchRelease(url)
}

// fetchRelease fetches a release from the given URL with retry logic
func (c *Client) fetchRelease(url string) (*Release, error) {
	var lastErr error
	maxRetries := 3
	backoff := time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff)
			backoff *= 2
		}

		release, err := c.doRequest(url)
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

// doRequest performs the actual HTTP request
func (c *Client) doRequest(url string) (*Release, error) {
	req, err := http.NewRequest("GET", url, nil)
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
			// Best effort - log but don't fail the request
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

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &release, nil
}

// NotFoundError is returned when a release is not found
type NotFoundError struct {
	URL string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("release not found: %s", e.URL)
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
func (c *Client) GetRepository(owner, repo string) (*Repository, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	return c.fetchRepository(url)
}

func (c *Client) fetchRepository(url string) (*Repository, error) {
	req, err := http.NewRequest("GET", url, nil)
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
	switch err.(type) {
	case *NotFoundError, *RateLimitError:
		return true
	default:
		return false
	}
}
