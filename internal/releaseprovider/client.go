package releaseprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/httpretry"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/releaseasset"
)

// Provider is the release discovery contract shared by all forge adapters.
type Provider interface {
	GetRelease(ctx context.Context, repository, tag string) (*releaseasset.Release, error)
	GetLatestReleaseWithMinAge(ctx context.Context, repository string, minAge int) (*releaseasset.Release, error)
	GetRepository(ctx context.Context, repository string) (*releaseasset.Repository, error)
	ResolveDigest(ctx context.Context, repository string, assets []releaseasset.Asset, asset *releaseasset.Asset, hashes, checksums map[string]string) error
}

// Client handles forge-specific routes and host-scoped metadata authentication.
type Client struct {
	source        Source
	RetryNotifier httpretry.Notifier
	http          *http.Client
	token         string
	packageFiles  map[string]map[string]string
	projectIDs    map[string]int64
	repositories  map[string]releaseasset.Repository
}

// New constructs a validated instance client without making a network request.
func New(source Source) (*Client, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	c := &Client{source: source, http: httpx.NewHardenedClient(apiTimeout), packageFiles: make(map[string]map[string]string), projectIDs: make(map[string]int64), repositories: make(map[string]releaseasset.Repository)}
	if source.TokenEnv != "" {
		var err error
		c.token, err = env.Credential(source.TokenEnv)
		if err != nil {
			return nil, err
		}
	}
	guard := c.http.CheckRedirect
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := guard(req, via); err != nil {
			return err
		}
		req.Header.Del("Authorization")
		req.Header.Del("Private-Token")
		c.authenticate(req)
		return nil
	}
	return c, nil
}

func sameOrigin(a, b string) bool { return httpx.SameOrigin(a, b) }

func repoRoute(kind, repository string) string {
	if kind == "gitlab" {
		return "/projects/" + url.PathEscape(repository)
	}
	parts := strings.Split(repository, "/")
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}

type gitlabRelease struct {
	TagName    string    `json:"tag_name"`
	ReleasedAt time.Time `json:"released_at"`
	Upcoming   bool      `json:"upcoming_release"`
	Assets     struct {
		Links []struct {
			Name      string `json:"name"`
			URL       string `json:"url"`
			DirectURL string `json:"direct_asset_url"`
		} `json:"links"`
	} `json:"assets"`
}

func (r gitlabRelease) release() releaseasset.Release {
	out := releaseasset.Release{TagName: r.TagName, PublishedAt: r.ReleasedAt, Draft: r.Upcoming}
	for _, link := range r.Assets.Links {
		raw := link.URL
		if raw == "" {
			raw = link.DirectURL
		}
		out.Assets = append(out.Assets, releaseasset.Asset{Name: link.Name, BrowserDownloadURL: raw, MetadataURL: link.URL})
	}
	return out
}

// GetRelease returns an explicitly pinned tag from the selected instance.
func (c *Client) GetRelease(ctx context.Context, repository, tag string) (*releaseasset.Release, error) {
	if err := ValidateRepository(c.source.Type, repository); err != nil {
		return nil, err
	}
	if err := ValidateTag(tag); err != nil {
		return nil, err
	}

	path := repoRoute(c.source.Type, repository) + "/releases/"
	if c.source.Type != "gitlab" {
		path += "tags/"
	}
	if c.source.Type == "gitlab" {
		var raw gitlabRelease
		if err := c.getJSON(ctx, path+url.PathEscape(tag), &raw); err != nil {
			return nil, err
		}
		r := raw.release()
		if r.TagName != tag {
			return nil, fmt.Errorf("release response does not match requested tag %q", tag)
		}
		if err := c.decorate(ctx, repository, &r); err != nil {
			return nil, err
		}
		return &r, nil
	}
	var r releaseasset.Release
	if err := c.getJSON(ctx, path+url.PathEscape(tag), &r); err != nil {
		return nil, err
	}
	if r.TagName != tag {
		return nil, fmt.Errorf("release response does not match requested tag %q", tag)
	}
	if err := c.decorate(ctx, repository, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// GetLatestReleaseWithMinAge compares stable versions across all bounded listing pages.
func (c *Client) GetLatestReleaseWithMinAge(ctx context.Context, repository string, minAge int) (*releaseasset.Release, error) {
	if err := ValidateRepository(c.source.Type, repository); err != nil {
		return nil, err
	}

	var releases []releaseasset.Release
	for page := 1; page <= apiMaxPages; page++ {
		var rows []releaseasset.Release
		path := fmt.Sprintf("%s/releases?page=%d&limit=%d", repoRoute(c.source.Type, repository), page, apiPageSize)
		if c.source.Type == "github" {
			path = fmt.Sprintf("%s/releases?page=%d&per_page=%d", repoRoute(c.source.Type, repository), page, apiPageSize)
		}
		if c.source.Type == "gitlab" {
			path = fmt.Sprintf("%s/releases?page=%d&per_page=%d", repoRoute(c.source.Type, repository), page, apiPageSize)
			var raw []gitlabRelease
			if err := c.getJSON(ctx, path, &raw); err != nil {
				return nil, err
			}
			for _, r := range raw {
				rows = append(rows, r.release())
			}
		} else if err := c.getJSON(ctx, path, &rows); err != nil {
			return nil, err
		}
		for i := range rows {
			if releaseasset.IsPrereleaseTag(rows[i].TagName) {
				rows[i].Prerelease = true
			}
		}
		releases = append(releases, rows...)
		if len(rows) == 0 {
			r := releaseasset.SelectLatestStableRelease(releases, minAge, time.Now())
			if r != nil {
				if err := ValidateTag(r.TagName); err != nil {
					return nil, err
				}
			}
			if err := c.decorate(ctx, repository, r); err != nil {
				return nil, err
			}
			return r, nil
		}
	}
	return nil, errors.New("release listing did not finish within 10 pages; pin a tag explicitly")
}

// GetRepository fetches visibility and description, plus GitLab package access and project identity.
func (c *Client) GetRepository(ctx context.Context, repository string) (*releaseasset.Repository, error) {
	if err := ValidateRepository(c.source.Type, repository); err != nil {
		return nil, err
	}

	if cached, ok := c.repositories[repository]; ok {
		return &cached, nil
	}

	var raw struct {
		Private       *bool  `json:"private"`
		Visibility    string `json:"visibility"`
		PackageAccess string `json:"package_registry_access_level"`
		Description   string `json:"description"`
		ID            int64  `json:"id"`
	}
	if err := c.getJSON(ctx, repoRoute(c.source.Type, repository), &raw); err != nil {
		return nil, err
	}
	repo := releaseasset.Repository{Description: raw.Description, PackageAccess: raw.PackageAccess}
	if c.source.Type == "gitlab" {
		switch raw.Visibility {
		case "public", "internal", "private":
			repo.Visibility = raw.Visibility
			repo.Private = raw.Visibility != "public"
		}
	} else if raw.Private != nil {
		repo.Private = *raw.Private
		repo.Visibility = "public"
		if *raw.Private {
			repo.Visibility = "private"
		}
		if raw.Visibility != "" && raw.Visibility != repo.Visibility {
			return nil, errors.New("repository visibility conflicts with private flag")
		}
	}
	c.projectIDs[repository] = raw.ID
	if repo.Visibility != "" {
		c.repositories[repository] = repo
	}
	return &repo, nil
}

func (c *Client) authenticate(req *http.Request) {
	if c.token == "" || !sameOrigin(req.URL.String(), c.source.APIBase()) {
		return
	}
	switch c.source.Type {
	case "gitlab":
		req.Header.Set("Private-Token", c.token)
	case "github":
		req.Header.Set("Authorization", "Bearer "+c.token)
	default:
		req.Header.Set("Authorization", "token "+c.token)
	}
}

func (c *Client) get(ctx context.Context, raw string, accept string) ([]byte, error) {
	return c.request(ctx, raw, accept, nil, false)
}

// getAsset keeps discovery authentication out of artifact/checksum requests.
func (c *Client) getAsset(ctx context.Context, asset *releaseasset.Asset) ([]byte, error) {
	return c.request(ctx, asset.BrowserDownloadURL, "application/octet-stream", asset.Auth, true)
}

func (c *Client) request(ctx context.Context, raw string, accept string, auth *httpx.RequestAuth, download bool) ([]byte, error) {
	if err := httpx.GuardOffline("release provider request"); err != nil {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("malformed release request URL")
	}
	checked := *u
	checked.RawQuery = ""
	if err := ValidateURL(checked.String()); err != nil {
		return nil, err
	}
	client := c.http
	if download {
		client, err = httpx.WithAuth(httpx.NewHardenedClient(apiTimeout), auth)
		if err != nil {
			return nil, err
		}
	}
	var data []byte
	err = httpretry.Retry(ctx, "GET "+raw, c.RetryNotifier, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return httpretry.Permanent(err)
		}
		req.Header.Set("Accept", accept)
		if download {
			if err := auth.Apply(req); err != nil {
				return httpretry.Permanent(err)
			}
		} else {
			c.authenticate(req)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("release request failed: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if c.source.Type == "github" && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) {
			limit := github.RateLimitFromResponse(resp, time.Now())
			if limit.Wait == 0 {
				return httpretry.Permanent(limit)
			}
			return limit
		}
		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("release source %s returned HTTP %d", c.source.Type, resp.StatusCode)
			if httpretry.RetryableStatus(resp.StatusCode) {
				return httpretry.WithRetryAfter(err, resp)
			}
			return httpretry.Permanent(err)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, metadataMaxBytes+1))
		if err != nil {
			return fmt.Errorf("read release metadata: %w", err)
		}
		if len(data) > metadataMaxBytes {
			return httpretry.Permanent(errors.New("release metadata exceeds 16 MiB"))
		}
		if accept == "application/json" {
			var decoded any
			if err := json.NewDecoder(bytes.NewReader(data)).Decode(&decoded); err != nil {
				return httpretry.ClassifyDecode(err)
			}
		}
		return nil
	})
	return data, err
}

func (c *Client) getJSON(ctx context.Context, path string, value any) error {
	data, err := c.get(ctx, c.source.APIBase()+path, "application/json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return errors.New("release metadata does not match the provider API schema")
	}
	return nil
}

var _ Provider = (*Client)(nil)
