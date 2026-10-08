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
}

// New constructs a validated instance client without making a network request.
func New(source Source) (*Client, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	c := &Client{source: source, http: httpx.NewHardenedClient(apiTimeout), packageFiles: make(map[string]map[string]string), projectIDs: make(map[string]int64)}
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
		if link.DirectURL != "" {
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
		c.decorate(repository, &r)
		return &r, nil
	}
	var r releaseasset.Release
	if err := c.getJSON(ctx, path+url.PathEscape(tag), &r); err != nil {
		return nil, err
	}
	if r.TagName != tag {
		return nil, fmt.Errorf("release response does not match requested tag %q", tag)
	}
	c.decorate(repository, &r)
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
			c.decorate(repository, r)
			return r, nil
		}
	}
	return nil, errors.New("release listing did not finish within 10 pages; pin a tag explicitly")
}

// GetRepository fetches description and, for GitLab, the project identity used for digest lookup.
func (c *Client) GetRepository(ctx context.Context, repository string) (*releaseasset.Repository, error) {
	if err := ValidateRepository(c.source.Type, repository); err != nil {
		return nil, err
	}

	var raw struct {
		Description string `json:"description"`
		ID          int64  `json:"id"`
	}
	if err := c.getJSON(ctx, repoRoute(c.source.Type, repository), &raw); err != nil {
		return nil, err
	}
	c.projectIDs[repository] = raw.ID
	return &releaseasset.Repository{Description: raw.Description}, nil
}

func (c *Client) decorate(repository string, r *releaseasset.Release) {
	if r == nil || c.source.TokenEnv == "" {
		return
	}
	u, _ := url.Parse(c.source.APIBase())
	origin := u.Scheme + "://" + u.Host
	for i := range r.Assets {
		asset := &r.Assets[i]
		if c.source.Type == "gitlab" && sameOrigin(asset.BrowserDownloadURL, c.source.APIBase()) {
			raw, err := url.Parse(asset.BrowserDownloadURL)
			if err == nil {
				if index := strings.Index(raw.EscapedPath(), "/downloads/"); index >= 0 {
					asset.BrowserDownloadURL = c.source.APIBase() + repoRoute("gitlab", repository) + "/releases/" + url.PathEscape(r.TagName) + raw.EscapedPath()[index:]
				}
			}
		}
		if c.source.Type == "github" && asset.APIURL != "" && sameOrigin(asset.APIURL, c.source.APIBase()) {
			asset.BrowserDownloadURL = asset.APIURL
		}
		if !sameOrigin(asset.BrowserDownloadURL, c.source.APIBase()) {
			continue
		}
		ref := &httpx.RequestAuth{Origin: origin, TokenEnv: c.source.TokenEnv, Header: "Authorization", Scheme: "token"}
		if c.source.Type == "gitlab" {
			ref.Header = "PRIVATE-TOKEN"
			ref.Scheme = ""
		}
		if c.source.Type == "github" {
			ref.Scheme = "Bearer"
			ref.Accept = "application/octet-stream"
		}
		asset.Auth = ref
	}
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
	var data []byte
	err = httpretry.Retry(ctx, "GET "+raw, c.RetryNotifier, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return httpretry.Permanent(err)
		}
		req.Header.Set("Accept", accept)
		c.authenticate(req)
		resp, err := c.http.Do(req)
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
