package releaseprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/releaseasset"
)

// SHA256 validates an external digest and returns its lowercase hexadecimal form.
func SHA256(value string) (string, error) {
	value = strings.TrimPrefix(value, "sha256:")
	data, err := hex.DecodeString(value)
	if err != nil || len(data) != sha256.Size {
		return "", errors.New("expected a SHA-256 digest (64 hexadecimal characters)")
	}
	return strings.ToLower(value), nil
}

// ResolveDigest resolves only the candidate currently being considered for a selected target.
// Checksum artifacts themselves must have an explicit SHA-256 pin before they are fetched.
func (c *Client) ResolveDigest(ctx context.Context, repository string, assets []releaseasset.Asset, asset *releaseasset.Asset, hashes, checksums map[string]string) error {
	if err := ValidateArtifactURL(asset.BrowserDownloadURL); err != nil {
		return err
	}
	if pinned, ok := hashes[asset.Name]; ok {
		expected, err := SHA256(pinned)
		if err != nil {
			return err
		}
		if asset.Digest != "" {
			actual, err := SHA256(asset.Digest)
			if err != nil {
				return err
			}
			if actual != expected {
				return errors.New("API digest conflicts with manifest hash")
			}
		}
		asset.Digest = "sha256:" + expected
		return nil
	}
	if asset.Digest != "" {
		_, err := SHA256(asset.Digest)
		return err
	}
	if c.source.Type == "gitlab" {
		metadataURL := asset.MetadataURL
		if metadataURL == "" {
			metadataURL = asset.BrowserDownloadURL
		}
		hash, err := c.gitlabDigest(ctx, repository, metadataURL)
		if err != nil {
			return err
		}
		if hash != "" {
			asset.Digest = "sha256:" + hash
			return nil
		}
	}
	names := make([]string, 0, len(checksums))
	for name := range checksums {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var checksum *releaseasset.Asset
		for i := range assets {
			if assets[i].Name == name {
				if checksum != nil {
					return fmt.Errorf("ambiguous checksum asset %q", name)
				}
				checksum = &assets[i]
			}
		}
		if checksum == nil {
			return fmt.Errorf("pinned checksum asset %q is absent", name)
		}
		expected, err := SHA256(checksums[name])
		if err != nil {
			return err
		}
		key := "checksum:" + checksum.BrowserDownloadURL + ":" + expected
		table, ok := c.packageFiles[key]
		if !ok {
			data, err := c.getAsset(ctx, checksum)
			if err != nil {
				return err
			}
			got := sha256.Sum256(data)
			if hex.EncodeToString(got[:]) != expected {
				return fmt.Errorf("checksum asset %q failed SHA-256 verification", name)
			}
			table, err = parseChecksums(data)
			if err != nil {
				return err
			}
			c.packageFiles[key] = table
		}
		if hash, ok := table[asset.Name]; ok {
			asset.Digest = "sha256:" + hash
			return nil
		}
	}
	return fmt.Errorf("asset %q has no published SHA-256; supply hashes or a checksums asset with its SHA-256 pin", asset.Name)
}

func parseChecksums(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var name, hash string
		if strings.HasPrefix(line, "SHA256 (") {
			split := strings.LastIndex(line, ") = ")
			if split < 8 {
				return nil, errors.New("malformed BSD SHA-256 record")
			}
			name = line[8:split]
			hash = line[split+4:]
		} else {
			if len(line) < 66 {
				return nil, errors.New("malformed SHA-256 checksum record")
			}
			hash = line[:64]
			name = strings.TrimSpace(line[64:])
			name = strings.TrimPrefix(name, "*")
		}
		valid, err := SHA256(hash)
		if err != nil {
			return nil, err
		}
		if name == "" || strings.ContainsAny(name, "\x00\r\n") {
			return nil, errors.New("invalid checksum filename")
		}
		if existing, ok := out[name]; ok && existing != valid {
			return nil, fmt.Errorf("conflicting checksum records for %q", name)
		}
		out[name] = valid
	}
	return out, nil
}

func (c *Client) gitlabDigest(ctx context.Context, repository, raw string) (string, error) {
	if !sameOrigin(raw, c.source.APIBase()) {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse GitLab asset URL: %w", err)
	}
	base, _ := url.Parse(c.source.APIBase())
	prefix := strings.TrimRight(base.EscapedPath(), "/") + "/projects/"
	if !strings.HasPrefix(u.EscapedPath(), prefix) {
		return "", nil
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), prefix), "/")
	if len(parts) != 6 || parts[1] != "packages" || parts[2] != "generic" {
		return "", nil
	}
	decoded := make([]string, len(parts))
	for i, part := range parts {
		decoded[i], err = url.PathUnescape(part)
		if err != nil {
			return "", fmt.Errorf("decode GitLab package path: %w", err)
		}
	}
	if decoded[0] != repository {
		if c.projectIDs[repository] == 0 {
			if _, err := c.GetRepository(ctx, repository); err != nil {
				return "", err
			}
		}
		if decoded[0] != strconv.FormatInt(c.projectIDs[repository], 10) {
			return "", errors.New("release asset points to a different GitLab project")
		}
	}
	type packageRow struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	var packages []packageRow
	for page := 1; page <= apiMaxPages; page++ {
		path := repoRoute("gitlab", repository) + "/packages?package_type=generic&package_name=" + url.QueryEscape(decoded[3]) + "&package_version=" + url.QueryEscape(decoded[4]) + fmt.Sprintf("&page=%d&per_page=%d", page, apiPageSize)
		var rows []packageRow
		if err := c.getJSON(ctx, path, &rows); err != nil {
			return "", err
		}
		if len(rows) == 0 {
			break
		}
		packages = append(packages, rows...)
		if page == apiMaxPages {
			return "", errors.New("GitLab packages exceed pagination limit")
		}
	}
	var expected string
	for _, p := range packages {
		if p.Name != decoded[3] || p.Version != decoded[4] {
			continue
		}
		key := fmt.Sprintf("gitlab:%s:%d", repository, p.ID)
		table, ok := c.packageFiles[key]
		if !ok {
			table = make(map[string]string)
			for page := 1; page <= apiMaxPages; page++ {
				var files []struct {
					Name   string `json:"file_name"`
					SHA256 string `json:"file_sha256"`
				}
				route := fmt.Sprintf("%s/packages/%d/package_files?page=%d&per_page=%d", repoRoute("gitlab", repository), p.ID, page, apiPageSize)
				if err := c.getJSON(ctx, route, &files); err != nil {
					return "", err
				}
				if len(files) == 0 {
					break
				}
				for _, file := range files {
					if old, present := table[file.Name]; present && old != file.SHA256 {
						table[file.Name] = "ambiguous"
					} else {
						table[file.Name] = file.SHA256
					}
				}
				if page == apiMaxPages {
					return "", errors.New("GitLab package files exceed pagination limit")
				}
			}
			c.packageFiles[key] = table
		}
		rawHash := table[decoded[5]]
		if rawHash == "" {
			continue
		}
		hash, err := SHA256(rawHash)
		if err != nil {
			return "", fmt.Errorf("invalid or ambiguous GitLab digest for %q: %w", decoded[5], err)
		}
		if expected != "" && expected != hash {
			return "", errors.New("multiple GitLab packages disagree on the asset SHA-256")
		}
		expected = hash
	}
	return expected, nil
}

// ValidateHashPins prevents a hash-less checksum request before any app is processed.
func ValidateHashPins(hashes, checksums map[string]string) error {
	for _, pins := range []map[string]string{hashes, checksums} {
		for name, hash := range pins {
			if name == "" {
				return errors.New("hash pin requires an asset filename")
			}
			if _, err := SHA256(hash); err != nil {
				return fmt.Errorf("hash for %q: %w", name, err)
			}
		}
	}
	return nil
}
