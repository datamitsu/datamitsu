package ocidigest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestArtifactPull_ManifestThenBlob drives a real Resolver through the exact
// sequence a parser-module pull performs: fetch the artifact manifest by
// digest, read its single layer descriptor, then fetch that layer as a blob.
//
// It lives here rather than in internal/ociartifact because that package can
// only reach a Resolver through an interface — NewResolverForHost hardcodes the
// https scheme on an unexported field, so out-of-package code cannot point one
// at an httptest server. Without this test the ociartifact↔ocidigest seam (URL
// shape, Accept header, the bearer handshake, and the descriptor-size handoff
// into PullBlob) would be exercised nowhere in the default build. An in-package
// test also cannot import ociartifact — that package imports this one — so the
// manifest is decoded here with the same two fields the consumer reads.
func TestArtifactPull_ManifestThenBlob(t *testing.T) {
	const repo = "datamitsu/datamitsu-parsers"

	module := []byte("\x00asm\x01\x00\x00\x00parser module bytes")
	layerDigest := sha256Digest(module)
	manifest := fmt.Appendf(nil, `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"artifactType": "application/vnd.datamitsu.parsers.v1+wasm",
		"config": {"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},
		"layers": [{"mediaType":"application/wasm","digest":%q,"size":%d}]
	}`, layerDigest, len(module))
	manifestDigest := sha256Digest(manifest)

	var (
		manifestAccept string
		manifestPath   string
		blobPath       string
		tokenHits      int
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		tokenHits++
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "artifact-token"})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		// Every registry endpoint challenges once, so the pull exercises the
		// full 401 → token → retry handshake rather than an anonymous shortcut.
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+baseURL(r)+`/token",service="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			manifestPath = r.URL.Path
			manifestAccept = r.Header.Get("Accept")
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = w.Write(manifest)
		case strings.Contains(r.URL.Path, "/blobs/"):
			blobPath = r.URL.Path
			_, _ = w.Write(module)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())

	raw, err := r.PullManifest(context.Background(), repo, manifestDigest)
	if err != nil {
		t.Fatalf("PullManifest: %v", err)
	}

	var m struct {
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode artifact manifest: %v", err)
	}
	if len(m.Layers) != 1 {
		t.Fatalf("artifact manifest has %d layers, want 1", len(m.Layers))
	}
	layer := m.Layers[0]

	path, err := r.PullBlob(context.Background(), repo, layer.Digest, layer.Size, 64<<20, "core", t.TempDir())
	if err != nil {
		t.Fatalf("PullBlob: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pulled module: %v", err)
	}
	if string(got) != string(module) {
		t.Errorf("pulled module = %q, want %q", got, module)
	}

	// The request shapes are part of the contract with any registry, not just
	// this fake: a digest-pinned manifest GET under /v2/<repo>/manifests/, and
	// a blob GET under /v2/<repo>/blobs/.
	if want := "/v2/" + repo + "/manifests/" + manifestDigest; manifestPath != want {
		t.Errorf("manifest path = %q, want %q", manifestPath, want)
	}
	if want := "/v2/" + repo + "/blobs/" + layer.Digest; blobPath != want {
		t.Errorf("blob path = %q, want %q", blobPath, want)
	}
	if !strings.Contains(manifestAccept, "application/vnd.oci.image.manifest.v1+json") {
		t.Errorf("manifest Accept = %q, want it to include the OCI image manifest type", manifestAccept)
	}
	if tokenHits == 0 {
		t.Error("the bearer handshake never ran; the pull took an anonymous shortcut")
	}
}

// baseURL reconstructs the test server's origin from a request, so the bearer
// challenge can point back at the same server.
func baseURL(r *http.Request) string {
	return "http://" + r.Host
}

// TestArtifactPull_ManifestSizeCapRejectsHugeBody pins that the artifact path
// inherits the manifest size cap: a registry that answers a manifest request
// with a huge body cannot make the client buffer it.
func TestArtifactPull_ManifestSizeCapRejectsHugeBody(t *testing.T) {
	huge := make([]byte, manifestMaxBytes+1)
	for i := range huge {
		huge[i] = 'x'
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(huge)
	}))
	defer srv.Close()

	r := newTestResolver(srv.URL, t.TempDir())
	_, err := r.PullManifest(context.Background(), "datamitsu/datamitsu-parsers", sha256Digest(huge))
	if err == nil {
		t.Fatal("PullManifest accepted a manifest over the size limit")
	}
	if !strings.Contains(err.Error(), "byte limit") {
		t.Errorf("error %q does not mention the size limit", err)
	}
}
