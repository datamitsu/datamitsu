package releaseprovider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/releaseasset"
)

func (c *Client) decorate(ctx context.Context, repository string, r *releaseasset.Release) error {
	if r == nil {
		return nil
	}
	// GitHub native assets all share the repository's access policy, including
	// empty releases; never infer public visibility from an absent private field.
	if c.source.Type == "github" && (c.source.DownloadAuth == "" || c.source.DownloadAuth == "auto") {
		repo, err := c.GetRepository(ctx, repository)
		if err != nil {
			return err
		}
		if repo.Visibility == "" {
			return errors.New("GitHub repository metadata is missing private visibility")
		}
	}
	for i := range r.Assets {
		if err := c.decorateAsset(ctx, repository, r, &r.Assets[i]); err != nil {
			return fmt.Errorf("asset %q: %w", r.Assets[i].Name, err)
		}
	}
	return nil
}

func (c *Client) decorateAsset(ctx context.Context, repository string, r *releaseasset.Release, asset *releaseasset.Asset) error {
	if err := ValidateArtifactURL(asset.BrowserDownloadURL); err != nil {
		return err
	}
	asset.Auth = nil
	mode := c.source.DownloadAuth
	if mode == "" {
		mode = "auto"
	}
	// External links never inherit a repository credential. Explicit required
	// applies only on the configured instance, not on an unrelated file server.
	if c.source.Type != "github" && !sameOrigin(asset.BrowserDownloadURL, c.source.URL) && !sameOrigin(asset.BrowserDownloadURL, c.source.APIBase()) {
		return nil
	}
	known, restricted, err := c.downloadAccess(ctx, repository, r, asset)
	if err != nil {
		return err
	}
	if mode == "none" {
		if known && restricted {
			return errors.New("downloadAuth none conflicts with a restricted download endpoint")
		}
		return nil
	}
	if mode == "auto" {
		if !known {
			return errors.New("download access is unknown; set source.downloadAuth to required or none")
		}
		if !restricted {
			return nil
		}
	}
	if c.source.TokenEnv == "" {
		return errors.New("authenticated downloads require source.tokenEnv")
	}
	if c.source.Type == "github" {
		if asset.APIURL == "" || !sameOrigin(asset.APIURL, c.source.APIBase()) {
			return errors.New("restricted GitHub asset has no origin-scoped API download URL")
		}
		asset.BrowserDownloadURL = asset.APIURL
	}
	u, _ := url.Parse(asset.BrowserDownloadURL)
	ref := &httpx.RequestAuth{Origin: u.Scheme + "://" + u.Host, TokenEnv: c.source.TokenEnv, Header: "Authorization", Scheme: "token"}
	switch c.source.Type {
	case "gitea", "forgejo":
		ref.Scheme = "Bearer"
	case "github":
		ref.Scheme = "Bearer"
		ref.Accept = "application/octet-stream"
	case "gitlab":
		ref.Header = "PRIVATE-TOKEN"
		ref.Scheme = ""
	}
	asset.Auth = ref
	return nil
}

// downloadAccess classifies only endpoints whose access contract the adapter
// knows. Same-origin links can point to unrelated projects and are not evidence.
func (c *Client) downloadAccess(ctx context.Context, repository string, r *releaseasset.Release, asset *releaseasset.Asset) (bool, bool, error) {
	u, _ := url.Parse(asset.BrowserDownloadURL)
	root, _ := url.Parse(c.source.URL)
	webPrefix := strings.TrimRight(root.EscapedPath(), "/") + "/" + repository + "/"
	path := u.EscapedPath()
	if c.source.Type == "gitea" || c.source.Type == "forgejo" {
		native := strings.HasPrefix(path, webPrefix+"releases/download/"+url.PathEscape(r.TagName)+"/") ||
			strings.HasPrefix(path, webPrefix+"releases/attachments/") ||
			strings.HasPrefix(path, webPrefix+"attachments/") ||
			(asset.UUID != "" && path == strings.TrimRight(root.EscapedPath(), "/")+"/attachments/"+url.PathEscape(asset.UUID))
		if !sameOrigin(asset.BrowserDownloadURL, c.source.URL) || !native {
			return false, false, nil
		}
	}
	repo, err := c.GetRepository(ctx, repository)
	if err != nil {
		return false, false, err
	}
	if c.source.Type == "gitlab" {
		api, _ := url.Parse(c.source.APIBase())
		prefix := strings.TrimRight(api.EscapedPath(), "/") + "/projects/"
		if sameOrigin(asset.BrowserDownloadURL, c.source.APIBase()) && strings.HasPrefix(path, prefix) {
			parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
			project, err := url.PathUnescape(parts[0])
			if err != nil {
				return false, false, fmt.Errorf("decode GitLab project path: %w", err)
			}
			if project != repository && project != strconv.FormatInt(c.projectIDs[repository], 10) {
				return false, false, nil
			}
			if len(parts) == 6 && parts[1] == "packages" && parts[2] == "generic" {
				switch repo.PackageAccess {
				case "public":
					return true, false, nil
				case "private":
					return true, true, nil
				case "disabled":
					return false, false, errors.New("GitLab package registry is disabled")
				case "enabled":
					return repo.Visibility != "", repo.Private, nil
				default:
					return false, false, nil
				}
			}
			if len(parts) >= 5 && parts[1] == "releases" && parts[3] == "downloads" {
				return repo.Visibility != "", repo.Private, nil
			}
		}
		prefix = webPrefix + "-/releases/" + url.PathEscape(r.TagName) + "/downloads/"
		if sameOrigin(asset.BrowserDownloadURL, c.source.URL) && strings.HasPrefix(path, prefix) {
			// Use the documented token-capable release API route for restricted
			// downloads. Preserve the original web URL for public projects.
			if repo.Visibility != "" && repo.Private && c.source.DownloadAuth != "none" {
				asset.BrowserDownloadURL = c.source.APIBase() + repoRoute("gitlab", repository) + "/releases/" + url.PathEscape(r.TagName) + "/downloads/" + strings.TrimPrefix(path, prefix)
			}
			return repo.Visibility != "", repo.Private, nil
		}
		return false, false, nil
	}
	return repo.Visibility != "" || r.Draft, repo.Private || r.Draft, nil
}
