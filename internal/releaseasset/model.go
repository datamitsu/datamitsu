// Package releaseasset holds provider-independent release metadata.
package releaseasset

import (
	"time"

	"github.com/datamitsu/datamitsu/internal/httpx"
)

// Asset carries discovery metadata; a non-empty SHA-256 digest is required before downloading.
type Asset struct {
	APIURL             string             `json:"url,omitempty"`
	MetadataURL        string             `json:"-"`
	Auth               *httpx.RequestAuth `json:"-"`
	Name               string             `json:"name"`
	BrowserDownloadURL string             `json:"browser_download_url"`
	Size               int64              `json:"size"`
	ContentType        string             `json:"content_type"`
	Digest             string             `json:"digest,omitempty"`
}

// Release is a published version and its discovered assets.
type Release struct {
	TagName     string    `json:"tag_name"`
	Assets      []Asset   `json:"assets"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

// Repository is the metadata carried into a generated app description.
type Repository struct {
	Description string `json:"description"`
}
