package github

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	t.Run("creates client without token", func(t *testing.T) {
		_ = os.Unsetenv("GITHUB_TOKEN")

		client := NewClient()
		if client == nil {
			t.Fatal("expected non-nil client")
		}

		if client.httpClient == nil {
			t.Error("expected non-nil http client")
		}

		if client.token != "" {
			t.Errorf("expected empty token, got '%s'", client.token)
		}
	})

	t.Run("creates client with token from env", func(t *testing.T) {
		expectedToken := "test-token-123"
		_ = os.Setenv("GITHUB_TOKEN", expectedToken)
		defer func() { _ = os.Unsetenv("GITHUB_TOKEN") }()

		client := NewClient()
		if client.token != expectedToken {
			t.Errorf("expected token '%s', got '%s'", expectedToken, client.token)
		}
	})

	t.Run("sets timeout on http client", func(t *testing.T) {
		client := NewClient()
		if client.httpClient.Timeout != 30*time.Second {
			t.Errorf("expected timeout 30s, got %v", client.httpClient.Timeout)
		}
	})
}

func TestGetRelease(t *testing.T) {
	t.Run("successful fetch via fetchRelease", func(t *testing.T) {
		release := &Release{
			TagName: "v1.0.0",
			Assets: []Asset{
				{
					Name:               "binary-linux-amd64",
					BrowserDownloadURL: "https://example.com/binary",
					Size:               1024,
					ContentType:        "application/octet-stream",
				},
			},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") != "application/vnd.github.v3+json" {
				t.Errorf("unexpected Accept header: %s", r.Header.Get("Accept"))
			}

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(release)
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		result, err := client.fetchRelease(server.URL)
		if err != nil {
			t.Fatalf("fetchRelease() error = %v", err)
		}

		if result.TagName != "v1.0.0" {
			t.Errorf("expected tag 'v1.0.0', got '%s'", result.TagName)
		}

		if len(result.Assets) != 1 {
			t.Errorf("expected 1 asset, got %d", len(result.Assets))
		}
	})

	t.Run("404 not found via fetchRelease", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		_, err := client.fetchRelease(server.URL)
		if err == nil {
			t.Error("expected error for 404, got nil")
		}

		notFoundError := &NotFoundError{}
		if !errors.As(err, &notFoundError) {
			t.Errorf("expected NotFoundError, got %T", err)
		}
	})

	t.Run("includes auth token when set via doRequest", func(t *testing.T) {
		expectedToken := "test-token"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "Bearer "+expectedToken {
				t.Errorf("expected auth header 'Bearer %s', got '%s'", expectedToken, authHeader)
			}

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(&Release{TagName: "v1.0.0"})
		}))
		defer server.Close()

		client := NewClient()
		client.token = expectedToken
		client.httpClient = server.Client()

		_, err := client.doRequest(server.URL)
		if err != nil {
			t.Fatalf("doRequest() error = %v", err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestGetLatestRelease(t *testing.T) {
	t.Run("constructs correct URL", func(t *testing.T) {
		var capturedURL string
		client := NewClient()
		client.httpClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				capturedURL = req.URL.String()
				body := `{"tag_name":"v1.0.0","assets":[]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		}

		release, err := client.GetLatestRelease("myowner", "myrepo")
		if err != nil {
			t.Fatalf("GetLatestRelease() error = %v", err)
		}

		expected := "https://api.github.com/repos/myowner/myrepo/releases/latest"
		if capturedURL != expected {
			t.Errorf("expected URL %q, got %q", expected, capturedURL)
		}

		if release.TagName != "v1.0.0" {
			t.Errorf("expected tag 'v1.0.0', got '%s'", release.TagName)
		}
	})
}

func TestGetReleaseByTag(t *testing.T) {
	t.Run("constructs correct URL with tag", func(t *testing.T) {
		var capturedURL string
		client := NewClient()
		client.httpClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				capturedURL = req.URL.String()
				body := `{"tag_name":"v2.0.0","assets":[]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		}

		release, err := client.GetRelease("myowner", "myrepo", "v2.0.0")
		if err != nil {
			t.Fatalf("GetRelease() error = %v", err)
		}

		expected := "https://api.github.com/repos/myowner/myrepo/releases/tags/v2.0.0"
		if capturedURL != expected {
			t.Errorf("expected URL %q, got %q", expected, capturedURL)
		}

		if release.TagName != "v2.0.0" {
			t.Errorf("expected tag 'v2.0.0', got '%s'", release.TagName)
		}
	})
}

func TestFetchReleaseRetry(t *testing.T) {
	t.Run("retries on server error", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(&Release{TagName: "v1.0.0"})
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		release, err := client.fetchRelease(server.URL)
		if err != nil {
			t.Fatalf("fetchRelease() error = %v", err)
		}

		if release.TagName != "v1.0.0" {
			t.Errorf("expected tag 'v1.0.0', got '%s'", release.TagName)
		}

		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}
	})

	t.Run("does not retry on 404", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		_, err := client.fetchRelease(server.URL)
		if err == nil {
			t.Error("expected error for 404")
		}

		if attempts != 1 {
			t.Errorf("expected 1 attempt for 404, got %d", attempts)
		}
	})

	t.Run("does not retry on 403 rate limit", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		_, err := client.fetchRelease(server.URL)
		if err == nil {
			t.Error("expected error for 403")
		}

		rateLimitError := &RateLimitError{}
		if !errors.As(err, &rateLimitError) {
			t.Errorf("expected RateLimitError, got %T", err)
		}

		if attempts != 1 {
			t.Errorf("expected 1 attempt for 403, got %d", attempts)
		}
	})

	t.Run("max retries exceeded", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		_, err := client.fetchRelease(server.URL)
		if err == nil {
			t.Error("expected error after max retries")
		}

		if attempts != 3 {
			t.Errorf("expected 3 attempts (max retries), got %d", attempts)
		}
	})
}

func TestDoRequest(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		release := &Release{
			TagName: "v1.0.0",
			Assets:  []Asset{},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(release)
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		result, err := client.doRequest(server.URL)
		if err != nil {
			t.Fatalf("doRequest() error = %v", err)
		}

		if result.TagName != "v1.0.0" {
			t.Errorf("expected tag 'v1.0.0', got '%s'", result.TagName)
		}
	})

	t.Run("invalid JSON response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("invalid json"))
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		_, err := client.doRequest(server.URL)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("unexpected status code", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		_, err := client.doRequest(server.URL)
		if err == nil {
			t.Error("expected error for 400 status")
		}

		if !strings.Contains(err.Error(), "400") {
			t.Errorf("error should mention status code 400: %v", err)
		}
	})
}

func TestNotFoundError(t *testing.T) {
	t.Run("error message contains URL", func(t *testing.T) {
		url := "https://api.github.com/repos/owner/repo/releases/tags/v1.0.0"
		err := &NotFoundError{URL: url}

		if !strings.Contains(err.Error(), url) {
			t.Errorf("error message should contain URL: %s", err.Error())
		}
	})
}

func TestRateLimitError(t *testing.T) {
	t.Run("error message mentions GITHUB_TOKEN", func(t *testing.T) {
		err := &RateLimitError{}

		if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
			t.Errorf("error message should mention GITHUB_TOKEN: %s", err.Error())
		}
	})
}

func TestGetRepository(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		repo := &Repository{
			FullName:    "owner/repo",
			Description: "A test repository",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") != "application/vnd.github.v3+json" {
				t.Errorf("unexpected Accept header: %s", r.Header.Get("Accept"))
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(repo)
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		result, err := client.fetchRepository(server.URL)
		if err != nil {
			t.Fatalf("fetchRepository() error = %v", err)
		}

		if result.FullName != "owner/repo" {
			t.Errorf("expected full_name 'owner/repo', got '%s'", result.FullName)
		}
		if result.Description != "A test repository" {
			t.Errorf("expected description 'A test repository', got '%s'", result.Description)
		}
	})

	t.Run("404 not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		_, err := client.fetchRepository(server.URL)
		if err == nil {
			t.Error("expected error for 404, got nil")
		}
		notFoundError := &NotFoundError{}
		if !errors.As(err, &notFoundError) {
			t.Errorf("expected NotFoundError, got %T", err)
		}
	})

	t.Run("403 rate limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		_, err := client.fetchRepository(server.URL)
		if err == nil {
			t.Error("expected error for 403, got nil")
		}
		rateLimitError := &RateLimitError{}
		if !errors.As(err, &rateLimitError) {
			t.Errorf("expected RateLimitError, got %T", err)
		}
	})

	t.Run("includes auth token", func(t *testing.T) {
		expectedToken := "test-token"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "Bearer "+expectedToken {
				t.Errorf("expected auth header 'Bearer %s', got '%s'", expectedToken, authHeader)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(&Repository{FullName: "owner/repo"})
		}))
		defer server.Close()

		client := NewClient()
		client.token = expectedToken
		client.httpClient = server.Client()

		_, err := client.fetchRepository(server.URL)
		if err != nil {
			t.Fatalf("fetchRepository() error = %v", err)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		_, err := client.fetchRepository(server.URL)
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})
}

func TestIsNonRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "NotFoundError is non-retryable",
			err:      &NotFoundError{URL: "test"},
			expected: true,
		},
		{
			name:     "RateLimitError is non-retryable",
			err:      &RateLimitError{},
			expected: true,
		},
		{
			name:     "generic error is retryable",
			err:      &genericError{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNonRetryableError(tt.err)
			if result != tt.expected {
				t.Errorf("isNonRetryableError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestReleaseFieldParsing(t *testing.T) {
	t.Run("parses PublishedAt from ISO 8601", func(t *testing.T) {
		body := `{"tag_name":"v1.0.0","published_at":"2026-01-02T03:04:05Z","prerelease":true,"draft":false,"assets":[]}`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		result, err := client.doRequest(server.URL)
		if err != nil {
			t.Fatalf("doRequest() error = %v", err)
		}

		want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		if !result.PublishedAt.Equal(want) {
			t.Errorf("expected PublishedAt %v, got %v", want, result.PublishedAt)
		}
		if !result.Prerelease {
			t.Error("expected Prerelease=true")
		}
		if result.Draft {
			t.Error("expected Draft=false")
		}
	})

	t.Run("missing published_at yields zero time", func(t *testing.T) {
		body := `{"tag_name":"v1.0.0","assets":[]}`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		result, err := client.doRequest(server.URL)
		if err != nil {
			t.Fatalf("doRequest() error = %v", err)
		}

		if !result.PublishedAt.IsZero() {
			t.Errorf("expected zero PublishedAt, got %v", result.PublishedAt)
		}
	})

	t.Run("parses Draft field", func(t *testing.T) {
		body := `{"tag_name":"v1.0.0","draft":true,"assets":[]}`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()

		client := NewClient()
		client.httpClient = server.Client()

		result, err := client.doRequest(server.URL)
		if err != nil {
			t.Fatalf("doRequest() error = %v", err)
		}

		if !result.Draft {
			t.Error("expected Draft=true")
		}
	})
}

func TestListReleases(t *testing.T) {
	t.Run("constructs URL and decodes results", func(t *testing.T) {
		var capturedURL string
		client := NewClient()
		client.httpClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				capturedURL = req.URL.String()
				body := `[{"tag_name":"v2.0.0","assets":[]},{"tag_name":"v1.0.0","assets":[]}]`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		}

		releases, err := client.ListReleases("myowner", "myrepo", 30)
		if err != nil {
			t.Fatalf("ListReleases() error = %v", err)
		}

		expected := "https://api.github.com/repos/myowner/myrepo/releases?per_page=30"
		if capturedURL != expected {
			t.Errorf("expected URL %q, got %q", expected, capturedURL)
		}

		if len(releases) != 2 {
			t.Fatalf("expected 2 releases, got %d", len(releases))
		}
		if releases[0].TagName != "v2.0.0" {
			t.Errorf("expected first tag 'v2.0.0', got '%s'", releases[0].TagName)
		}
	})

	t.Run("defaults perPage when non-positive", func(t *testing.T) {
		var capturedURL string
		client := NewClient()
		client.httpClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				capturedURL = req.URL.String()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`[]`)),
				}, nil
			}),
		}

		_, err := client.ListReleases("o", "r", 0)
		if err != nil {
			t.Fatalf("ListReleases() error = %v", err)
		}
		if !strings.Contains(capturedURL, "per_page=30") {
			t.Errorf("expected default per_page=30, got %q", capturedURL)
		}
	})

	t.Run("does not retry on 404", func(t *testing.T) {
		attempts := 0
		client := NewClient()
		client.httpClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		_, err := client.ListReleases("o", "r", 30)
		if err == nil {
			t.Error("expected error for 404")
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt for 404, got %d", attempts)
		}
	})
}

func TestGetLatestReleaseWithMinAge(t *testing.T) {
	// newClientReturning serves the given JSON array body for ListReleases.
	newClientReturning := func(body string) *Client {
		client := NewClient()
		client.httpClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		}
		return client
	}

	iso := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	now := time.Now()
	old := iso(now.Add(-48 * time.Hour))    // 2 days old
	fresh := iso(now.Add(-1 * time.Minute)) // very fresh

	t.Run("selects older release when latest is too fresh", func(t *testing.T) {
		body := `[` +
			`{"tag_name":"v2.0.0","published_at":"` + fresh + `","assets":[]},` +
			`{"tag_name":"v1.0.0","published_at":"` + old + `","assets":[]}]`
		client := newClientReturning(body)

		r, err := client.GetLatestReleaseWithMinAge("o", "r", 60)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if r == nil || r.TagName != "v1.0.0" {
			t.Fatalf("expected v1.0.0, got %v", r)
		}
	})

	t.Run("skips prerelease even if old enough", func(t *testing.T) {
		body := `[` +
			`{"tag_name":"v2.0.0","published_at":"` + old + `","prerelease":true,"assets":[]},` +
			`{"tag_name":"v1.0.0","published_at":"` + old + `","assets":[]}]`
		client := newClientReturning(body)

		r, err := client.GetLatestReleaseWithMinAge("o", "r", 60)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if r == nil || r.TagName != "v1.0.0" {
			t.Fatalf("expected v1.0.0, got %v", r)
		}
	})

	t.Run("skips draft releases", func(t *testing.T) {
		body := `[` +
			`{"tag_name":"v2.0.0","published_at":"` + old + `","draft":true,"assets":[]},` +
			`{"tag_name":"v1.0.0","published_at":"` + old + `","assets":[]}]`
		client := newClientReturning(body)

		r, err := client.GetLatestReleaseWithMinAge("o", "r", 60)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if r == nil || r.TagName != "v1.0.0" {
			t.Fatalf("expected v1.0.0, got %v", r)
		}
	})

	t.Run("skips releases with zero PublishedAt", func(t *testing.T) {
		body := `[` +
			`{"tag_name":"v2.0.0","assets":[]},` +
			`{"tag_name":"v1.0.0","published_at":"` + old + `","assets":[]}]`
		client := newClientReturning(body)

		r, err := client.GetLatestReleaseWithMinAge("o", "r", 60)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if r == nil || r.TagName != "v1.0.0" {
			t.Fatalf("expected v1.0.0, got %v", r)
		}
	})

	t.Run("minAgeMinutes=0 falls through to GetLatestRelease", func(t *testing.T) {
		var capturedURL string
		client := NewClient()
		client.httpClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				capturedURL = req.URL.String()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v9.9.9","assets":[]}`)),
				}, nil
			}),
		}

		r, err := client.GetLatestReleaseWithMinAge("o", "r", 0)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if r == nil || r.TagName != "v9.9.9" {
			t.Fatalf("expected v9.9.9, got %v", r)
		}
		if !strings.HasSuffix(capturedURL, "/releases/latest") {
			t.Errorf("expected latest endpoint, got %q", capturedURL)
		}
	})

	t.Run("returns nil,nil when no release is old enough", func(t *testing.T) {
		body := `[` +
			`{"tag_name":"v2.0.0","published_at":"` + fresh + `","assets":[]}]`
		client := newClientReturning(body)

		r, err := client.GetLatestReleaseWithMinAge("o", "r", 60)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if r != nil {
			t.Fatalf("expected nil release, got %v", r)
		}
	})
}

type genericError struct{}

func (e *genericError) Error() string {
	return "generic error"
}
