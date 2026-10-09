package releaseprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/releaseasset"
)

func TestDownloadPolicy(t *testing.T) {
	for _, kind := range []string{"github", "gitlab", "gitea", "forgejo"} {
		for _, tc := range []struct {
			name, mode, visibility string
			wantAuth, wantError    bool
		}{
			{name: "public", visibility: "public"},
			{name: "restricted", visibility: "private", wantAuth: true},
			{name: "none-public", mode: "none", visibility: "public"},
			{name: "none-private", mode: "none", visibility: "private", wantError: true},
			{name: "required-public", mode: "required", visibility: "public", wantAuth: true},
			{name: "unknown", wantError: true},
			{name: "unknown-required", mode: "required", wantAuth: true},
			{name: "unknown-none", mode: "none"},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				t.Setenv("TEST_DOWNLOAD_TOKEN", "fixture")
				var base string
				var downloads int
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					isBinary := strings.HasSuffix(req.URL.Path, "/tool-linux-amd64") || strings.HasSuffix(req.URL.Path, "/releases/assets/1")
					if isBinary {
						downloads++
						got := req.Header.Get("Authorization") != "" || req.Header.Get("Private-Token") != ""
						if got != tc.wantAuth {
							t.Errorf("download auth=%v want %v", got, tc.wantAuth)
						}
						_, _ = w.Write([]byte("binary"))
						return
					}
					if req.Header.Get("Authorization") == "" && req.Header.Get("Private-Token") == "" {
						t.Error("metadata lacks token")
					}
					if strings.HasSuffix(req.URL.Path, "/releases/v1") && kind == "gitlab" {
						_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1", "assets": map[string]any{"links": []map[string]string{{"name": "tool-linux-amd64", "url": base + "/group/tool/-/releases/v1/downloads/tool-linux-amd64"}}}})
						return
					}
					if strings.Contains(req.URL.Path, "/releases/") {
						_ = json.NewEncoder(w).Encode(releaseasset.Release{TagName: "v1", Assets: []releaseasset.Asset{{Name: "tool-linux-amd64", BrowserDownloadURL: base + "/group/tool/releases/download/v1/tool-linux-amd64", APIURL: base + "/repos/group/tool/releases/assets/1"}}})
						return
					}
					metadata := map[string]any{"id": 42}
					if tc.visibility != "" {
						if kind == "gitlab" {
							metadata["visibility"] = tc.visibility
						} else {
							metadata["private"] = tc.visibility != "public"
						}
					}
					_ = json.NewEncoder(w).Encode(metadata)
				}))
				defer srv.Close()
				base = srv.URL
				c, err := New(Source{Type: kind, URL: base, APIURL: base, TokenEnv: "TEST_DOWNLOAD_TOKEN", DownloadAuth: tc.mode})
				if err != nil {
					t.Fatal(err)
				}
				r, err := c.GetRelease(context.Background(), "group/tool", "v1")
				if tc.wantError {
					if err == nil {
						t.Fatal("accepted invalid download policy")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				asset := r.Assets[0]
				if (asset.Auth != nil) != tc.wantAuth {
					t.Fatalf("auth=%+v", asset.Auth)
				}
				if !tc.wantAuth {
					t.Setenv("TEST_DOWNLOAD_TOKEN", "")
				}
				if _, err = c.getAsset(context.Background(), &asset); err != nil {
					t.Fatal(err)
				}
				if downloads != 1 {
					t.Fatalf("downloads=%d", downloads)
				}
			})
		}
	}
}

func TestGitLabAssetSpecificAccess(t *testing.T) {
	for _, tc := range []struct {
		name, visibility, access, path, mode string
		wantAuth, wantError                  bool
	}{
		{"private-public-package", "private", "public", "/api/v4/projects/42/packages/generic/tool/v1/tool", "", false, false},
		{"public-private-package", "public", "private", "/api/v4/projects/42/packages/generic/tool/v1/tool", "", true, false},
		{"internal-package", "internal", "enabled", "/api/v4/projects/42/packages/generic/tool/v1/tool", "", true, false},
		{"unknown-package-policy", "public", "", "/api/v4/projects/42/packages/generic/tool/v1/tool", "", false, true},
		{"disabled", "public", "disabled", "/api/v4/projects/42/packages/generic/tool/v1/tool", "", false, true},
		{"other-project", "public", "public", "/api/v4/projects/43/packages/generic/tool/v1/tool", "", false, true},
		{"opaque-same-origin", "public", "public", "/some/download", "", false, true},
		{"opaque-none", "private", "", "/some/download", "none", false, false},
		{"opaque-required", "public", "", "/some/download", "required", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEST_DOWNLOAD_TOKEN", "fixture")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "visibility": tc.visibility, "package_registry_access_level": tc.access})
			}))
			defer srv.Close()
			c, _ := New(Source{Type: "gitlab", URL: srv.URL, TokenEnv: "TEST_DOWNLOAD_TOKEN", DownloadAuth: tc.mode})
			r := &releaseasset.Release{TagName: "v1", Assets: []releaseasset.Asset{{Name: "tool", BrowserDownloadURL: srv.URL + tc.path}}}
			err := c.decorate(context.Background(), "group/tool", r)
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v", err)
			}
			if !tc.wantError && (r.Assets[0].Auth != nil) != tc.wantAuth {
				t.Fatalf("auth=%+v", r.Assets[0].Auth)
			}
		})
	}
}

func TestExternalLinkDoesNotInheritPrivateRepositoryAuth(t *testing.T) {
	for _, kind := range []string{"gitlab", "gitea", "forgejo"} {
		for _, mode := range []string{"auto", "required", "none"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				t.Setenv("TEST_DOWNLOAD_TOKEN", "fixture")
				c, _ := New(Source{Type: kind, URL: "https://forge.example.test", TokenEnv: "TEST_DOWNLOAD_TOKEN", DownloadAuth: mode})
				r := &releaseasset.Release{TagName: "v1", Assets: []releaseasset.Asset{{Name: "tool", BrowserDownloadURL: "https://files.example.test/tool"}}}
				if err := c.decorate(context.Background(), "group/tool", r); err != nil {
					t.Fatal(err)
				}
				if r.Assets[0].Auth != nil {
					t.Fatal("external asset inherited repository credentials")
				}
			})
		}
	}
}

func TestChecksumDownloadUsesResolvedAuth(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		name := "public"
		if restricted {
			name = "private"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("TEST_DOWNLOAD_TOKEN", "fixture")
			payload := []byte(strings.Repeat("a", 64) + "  tool\n")
			var base string
			cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("checksum redirect leaked auth")
				}
				_, _ = w.Write(payload)
			}))
			defer cdn.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/releases/download/") {
					if (r.Header.Get("Authorization") != "") != restricted {
						t.Error("checksum auth differs from policy")
					}
					http.Redirect(w, r, cdn.URL, http.StatusFound)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"private": restricted})
			}))
			defer srv.Close()
			base = srv.URL
			c, _ := New(Source{Type: "gitea", URL: base, TokenEnv: "TEST_DOWNLOAD_TOKEN"})
			r := &releaseasset.Release{TagName: "v1", Assets: []releaseasset.Asset{{Name: "tool", BrowserDownloadURL: base + "/group/tool/releases/download/v1/tool"}, {Name: "checksums", BrowserDownloadURL: base + "/group/tool/releases/download/v1/checksums"}}}
			if err := c.decorate(context.Background(), "group/tool", r); err != nil {
				t.Fatal(err)
			}
			if !restricted {
				t.Setenv("TEST_DOWNLOAD_TOKEN", "")
			}
			if err := c.ResolveDigest(context.Background(), "group/tool", r.Assets, &r.Assets[0], nil, map[string]string{"checksums": digest(payload)}); err != nil {
				t.Fatal(err)
			}
			if r.Assets[0].Digest != "sha256:"+strings.Repeat("a", 64) {
				t.Fatal("checksum not verified")
			}
		})
	}
}

func TestDownloadPolicyValidation(t *testing.T) {
	for _, s := range []Source{{Type: "github", URL: "https://github.com", DownloadAuth: "yes"}, {Type: "gitlab", URL: "https://gitlab.com", DownloadAuth: "required"}} {
		if _, err := New(s); err == nil {
			t.Fatal("invalid downloadAuth accepted")
		}
	}
}

func TestNativeAttachmentDraftAndSubpath(t *testing.T) {
	for _, kind := range []string{"gitea", "forgejo"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("TEST_DOWNLOAD_TOKEN", "fixture")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/forge/api/v1/repos/group/tool" {
					t.Errorf("repository path=%s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"private":false}`))
			}))
			defer srv.Close()
			c, _ := New(Source{Type: kind, URL: srv.URL + "/forge", TokenEnv: "TEST_DOWNLOAD_TOKEN"})
			for _, draft := range []bool{false, true} {
				r := &releaseasset.Release{TagName: "v1", Draft: draft, Assets: []releaseasset.Asset{{UUID: "asset-id", Name: "tool", BrowserDownloadURL: srv.URL + "/forge/attachments/asset-id"}}}
				if err := c.decorate(context.Background(), "group/tool", r); err != nil {
					t.Fatal(err)
				}
				if (r.Assets[0].Auth != nil) != draft {
					t.Fatalf("draft=%v auth=%+v", draft, r.Assets[0].Auth)
				}
			}
			foreign := &releaseasset.Release{TagName: "v1", Assets: []releaseasset.Asset{{Name: "tool", BrowserDownloadURL: srv.URL + "/forge/other/project/releases/download/v1/tool"}}}
			if err := c.decorate(context.Background(), "group/tool", foreign); err == nil {
				t.Fatal("unrelated native route inherited visibility")
			}
		})
	}
}

func TestGitLabExternalTargetBypassesPrivatePermanentLink(t *testing.T) {
	raw := gitlabRelease{TagName: "v1"}
	// Discovery may supply a private project's permanent redirect for a public
	// external file. Keep the actual target so no project credential is needed.
	if err := json.Unmarshal([]byte(`{"tag_name":"v1","assets":{"links":[{"name":"tool","url":"https://files.example.test/tool","direct_asset_url":"https://gitlab.example.test/group/tool/-/releases/v1/downloads/tool"}]}}`), &raw); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_DOWNLOAD_TOKEN", "fixture")
	c, _ := New(Source{Type: "gitlab", URL: "https://gitlab.example.test", TokenEnv: "TEST_DOWNLOAD_TOKEN"})
	r := raw.release()
	if err := c.decorate(context.Background(), "group/tool", &r); err != nil {
		t.Fatal(err)
	}
	if r.Assets[0].BrowserDownloadURL != "https://files.example.test/tool" || r.Assets[0].Auth != nil {
		t.Fatalf("external target=%+v", r.Assets[0])
	}
}
