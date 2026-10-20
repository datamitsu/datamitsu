// Package releaseprovider adapts release APIs into shared release metadata.
package releaseprovider

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"

	"github.com/datamitsu/datamitsu/internal/env"
)

// Source identifies an instance and an optional host-scoped API credential.
type Source struct {
	Type         string `json:"type"`
	URL          string `json:"url"`
	APIURL       string `json:"apiUrl,omitempty"`
	TokenEnv     string `json:"tokenEnv,omitempty"`
	DownloadAuth string `json:"downloadAuth,omitempty"`
}

// ValidateURL permits HTTPS and loopback-only HTTP for local instances and tests.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("source URL must have a host and no credentials, query or fragment")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("source URL requires HTTPS (HTTP is allowed only on loopback)")
}

// Validate rejects unknown providers and unsafe credential references.
func (s Source) Validate() error {
	switch s.Type {
	case "github", "gitlab", "gitea", "forgejo":
	default:
		return fmt.Errorf("unknown release source type %q", s.Type)
	}
	if err := ValidateURL(s.URL); err != nil {
		return err
	}
	if s.APIURL != "" {
		if err := ValidateURL(s.APIURL); err != nil {
			return err
		}
	}
	switch s.DownloadAuth {
	case "", "auto", "none":
	case "required":
		if s.TokenEnv == "" {
			return errors.New("source.downloadAuth required needs source.tokenEnv")
		}
	default:
		return fmt.Errorf("unknown source.downloadAuth %q (expected auto, required or none)", s.DownloadAuth)
	}
	if s.TokenEnv != "" {
		if _, err := env.Credential(s.TokenEnv); err != nil {
			return err
		}
	}
	return nil
}

// APIBase derives each forge's API prefix while preserving self-hosted subpaths.
func (s Source) APIBase() string {
	if s.APIURL != "" {
		return strings.TrimRight(s.APIURL, "/")
	}
	root := strings.TrimRight(s.URL, "/")
	if s.Type == "github" {
		u, _ := url.Parse(root)
		if strings.EqualFold(u.Hostname(), "github.com") && (u.Port() == "" || u.Port() == "443") && (u.Path == "" || u.Path == "/") {
			return "https://api.github.com"
		}
		return root + "/api/v3"
	}
	if s.Type == "gitlab" {
		return root + "/api/v4"
	}
	return root + "/api/v1"
}

// ValidateRepository protects endpoint construction and supports GitLab subgroups.
func ValidateRepository(kind, repository string) error {
	parts := strings.Split(repository, "/")
	if len(parts) < 2 || (kind != "gitlab" && len(parts) != 2) {
		return errors.New("repository must be namespace/project (GitLab permits nested namespaces)")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\?#%\x00\r\n\t ") {
			return fmt.Errorf("invalid repository component %q", part)
		}
	}
	return nil
}

// ValidateArtifactURL applies source URL safeguards while permitting download query parameters.
func ValidateArtifactURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid artifact URL: %w", err)
	}
	checked := *u
	checked.RawQuery = ""
	return ValidateURL(checked.String())
}

// ValidateTag rejects metadata that cannot be persisted as a usable pin.
func ValidateTag(tag string) error {
	if tag == "" || strings.IndexFunc(tag, unicode.IsControl) >= 0 {
		return errors.New("release tag must be non-empty and contain no control characters")
	}
	return nil
}
