package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	maps0 "maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

const nodeTestVersion = "26.2.0"

// buildNodeTestShasums assigns a distinct, valid SHA-256-shaped hash to every
// Node archive tuple for version v, split into the dist (nodejs.org) and musl
// (unofficial-builds) manifests the same way the real sources are.
func buildNodeTestShasums(v string) (dist, musl map[string]string) {
	dist = map[string]string{}
	musl = map[string]string{}
	const hexChars = "0123456789abcdef"
	for i, spec := range nodeArchiveSpecs(v) {
		h := strings.Repeat(string(hexChars[i]), 64)
		if spec.musl {
			musl[spec.filename] = h
		} else {
			dist[spec.filename] = h
		}
	}
	return dist, musl
}

func mergeShasums(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		maps0.Copy(out, m)
	}
	return out
}

func shasumsText(m map[string]string) string {
	var b strings.Builder
	for name, hash := range m {
		fmt.Fprintf(&b, "%s  %s\n", hash, name)
	}
	return b.String()
}

func newTestEntity(t *testing.T) *openpgp.Entity {
	t.Helper()
	e, err := openpgp.NewEntity("Test Node Signer", "", "node-test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	return e
}

func clearsignText(t *testing.T, signer *openpgp.Entity, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, signer.PrivateKey, nil)
	if err != nil {
		t.Fatalf("clearsign.Encode: %v", err)
	}
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.Bytes()
}

// nodeMockServers spins up two httptest servers mimicking nodejs.org (serving a
// clearsigned SHASUMS256.txt.asc) and unofficial-builds (serving a plain
// SHASUMS256.txt), and returns their base URLs.
func nodeMockServers(t *testing.T, distAsc []byte, muslTxt string) (distBase, muslBase string) {
	t.Helper()
	distSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/SHASUMS256.txt.asc") {
			_, _ = w.Write(distAsc)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(distSrv.Close)

	muslSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/SHASUMS256.txt") {
			_, _ = io.WriteString(w, muslTxt)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(muslSrv.Close)

	return distSrv.URL, muslSrv.URL
}

func TestNodeArchiveSpecs_FilenamesAndPaths(t *testing.T) {
	specs := nodeArchiveSpecs(nodeTestVersion)
	if len(specs) != 8 {
		t.Fatalf("expected 8 specs, got %d", len(specs))
	}

	type want struct {
		filename    string
		binaryPath  string
		contentType binmanager.BinContentType
		musl        bool
	}
	wants := map[string]want{
		"linux/amd64/glibc":     {"node-v26.2.0-linux-x64.tar.xz", "node-v26.2.0-linux-x64/bin/node", binmanager.BinContentTypeTarXz, false},
		"linux/amd64/musl":      {"node-v26.2.0-linux-x64-musl.tar.xz", "node-v26.2.0-linux-x64-musl/bin/node", binmanager.BinContentTypeTarXz, true},
		"linux/arm64/glibc":     {"node-v26.2.0-linux-arm64.tar.xz", "node-v26.2.0-linux-arm64/bin/node", binmanager.BinContentTypeTarXz, false},
		"linux/arm64/musl":      {"node-v26.2.0-linux-arm64-musl.tar.xz", "node-v26.2.0-linux-arm64-musl/bin/node", binmanager.BinContentTypeTarXz, true},
		"darwin/amd64/unknown":  {"node-v26.2.0-darwin-x64.tar.xz", "node-v26.2.0-darwin-x64/bin/node", binmanager.BinContentTypeTarXz, false},
		"darwin/arm64/unknown":  {"node-v26.2.0-darwin-arm64.tar.xz", "node-v26.2.0-darwin-arm64/bin/node", binmanager.BinContentTypeTarXz, false},
		"windows/amd64/unknown": {"node-v26.2.0-win-x64.zip", "node-v26.2.0-win-x64/node.exe", binmanager.BinContentTypeZip, false},
		"windows/arm64/unknown": {"node-v26.2.0-win-arm64.zip", "node-v26.2.0-win-arm64/node.exe", binmanager.BinContentTypeZip, false},
	}

	for _, spec := range specs {
		key := fmt.Sprintf("%s/%s/%s", spec.os, spec.arch, spec.libc)
		w, ok := wants[key]
		if !ok {
			t.Errorf("unexpected spec tuple: %s", key)
			continue
		}
		if spec.filename != w.filename {
			t.Errorf("%s: filename = %q, want %q", key, spec.filename, w.filename)
		}
		if got := nodeBinaryPath(spec); got != w.binaryPath {
			t.Errorf("%s: binaryPath = %q, want %q", key, got, w.binaryPath)
		}
		if spec.contentType != w.contentType {
			t.Errorf("%s: contentType = %q, want %q", key, spec.contentType, w.contentType)
		}
		if spec.musl != w.musl {
			t.Errorf("%s: musl = %v, want %v", key, spec.musl, w.musl)
		}
		delete(wants, key)
	}
	if len(wants) != 0 {
		t.Errorf("specs missing tuples: %v", wants)
	}
}

func TestNodeBinaryPath(t *testing.T) {
	xz := nodeArchiveSpec{filename: "node-v26.2.0-linux-x64.tar.xz", contentType: binmanager.BinContentTypeTarXz}
	if got := nodeBinaryPath(xz); got != "node-v26.2.0-linux-x64/bin/node" {
		t.Errorf("tar.xz binaryPath = %q", got)
	}
	zip := nodeArchiveSpec{filename: "node-v26.2.0-win-x64.zip", contentType: binmanager.BinContentTypeZip}
	if got := nodeBinaryPath(zip); got != "node-v26.2.0-win-x64/node.exe" {
		t.Errorf("zip binaryPath = %q", got)
	}
}

func TestParseSHASUMS(t *testing.T) {
	content := "" +
		"aaaa  node-v1.0.0-linux-x64.tar.xz\n" +
		"bbbb *node-v1.0.0-win-x64.zip\n" + // binary-mode "*" prefix
		"\n" + // blank line ignored
		"malformed-line-with-only-one-field\n" +
		"cccc  node-v1.0.0-darwin-arm64.tar.xz\n"
	m := parseSHASUMS(content)

	if m["node-v1.0.0-linux-x64.tar.xz"] != "aaaa" {
		t.Errorf("linux hash = %q", m["node-v1.0.0-linux-x64.tar.xz"])
	}
	if m["node-v1.0.0-win-x64.zip"] != "bbbb" {
		t.Errorf("win hash (with * prefix) = %q", m["node-v1.0.0-win-x64.zip"])
	}
	if m["node-v1.0.0-darwin-arm64.tar.xz"] != "cccc" {
		t.Errorf("darwin hash = %q", m["node-v1.0.0-darwin-arm64.tar.xz"])
	}
	if len(m) != 3 {
		t.Errorf("expected 3 parsed entries, got %d: %v", len(m), m)
	}
}

func TestBuildNodeBinaries_AllTuples(t *testing.T) {
	dist, musl := buildNodeTestShasums(nodeTestVersion)
	all := mergeShasums(dist, musl)

	cfg := nodePullConfig{
		version:     nodeTestVersion,
		distBaseURL: nodeDistBaseURL,
		muslBaseURL: nodeMuslBaseURL,
	}
	binaries, err := buildNodeBinaries(cfg, dist, musl)
	if err != nil {
		t.Fatalf("buildNodeBinaries: %v", err)
	}

	for _, spec := range nodeArchiveSpecs(nodeTestVersion) {
		info, ok := binaries[spec.os][spec.arch][spec.libc]
		if !ok {
			t.Errorf("%s/%s/%s: missing entry", spec.os, spec.arch, spec.libc)
			continue
		}

		base := nodeDistBaseURL
		if spec.musl {
			base = nodeMuslBaseURL
		}
		wantURL := fmt.Sprintf("%s/v%s/%s", base, nodeTestVersion, spec.filename)
		if info.URL != wantURL {
			t.Errorf("%s: URL = %q, want %q", spec.filename, info.URL, wantURL)
		}
		if info.Hash != all[spec.filename] {
			t.Errorf("%s: Hash = %q, want %q", spec.filename, info.Hash, all[spec.filename])
		}
		if info.ContentType != spec.contentType {
			t.Errorf("%s: ContentType = %q, want %q", spec.filename, info.ContentType, spec.contentType)
		}
		if info.BinaryPath == nil || *info.BinaryPath != nodeBinaryPath(spec) {
			t.Errorf("%s: BinaryPath = %v, want %q", spec.filename, info.BinaryPath, nodeBinaryPath(spec))
		}
		if !info.ExtractDir {
			t.Errorf("%s: ExtractDir should be true", spec.filename)
		}
	}

	// musl entries must differ from glibc (separate hosts + files)
	linuxAmd64 := binaries[syslist.OsTypeLinux][syslist.ArchTypeAmd64]
	if linuxAmd64["glibc"].URL == linuxAmd64["musl"].URL {
		t.Error("linux/amd64: glibc and musl should have different URLs")
	}
}

func TestBuildNodeBinaries_MissingMuslAsset(t *testing.T) {
	dist, musl := buildNodeTestShasums(nodeTestVersion)
	// Drop one musl asset → should be a hard error (no hash, no entry).
	delete(musl, "node-v"+nodeTestVersion+"-linux-x64-musl.tar.xz")

	cfg := nodePullConfig{version: nodeTestVersion, distBaseURL: nodeDistBaseURL, muslBaseURL: nodeMuslBaseURL}
	if _, err := buildNodeBinaries(cfg, dist, musl); err == nil {
		t.Fatal("expected error for missing musl asset hash")
	}
}

// TestBuildNodeBinaries_FailsClosedOnDroppedHash pins the fail-closed contract
// (review #11): parseSHASUMS silently drops malformed lines, so a required
// archive whose hash never made it into the parsed map MUST abort
// buildNodeBinaries with an error rather than emit a hash-less binary entry
// (security policy: no hash, no entry).
func TestBuildNodeBinaries_FailsClosedOnDroppedHash(t *testing.T) {
	dist, musl := buildNodeTestShasums(nodeTestVersion)

	// Render the dist manifest with one required archive's line malformed (the
	// filename field missing) so parseSHASUMS drops it on the floor.
	dropped := "node-v" + nodeTestVersion + "-linux-x64.tar.xz"
	var b strings.Builder
	for name, hash := range dist {
		if name == dropped {
			fmt.Fprintf(&b, "%s\n", hash) // malformed: single field → dropped
			continue
		}
		fmt.Fprintf(&b, "%s  %s\n", hash, name)
	}
	parsedDist := parseSHASUMS(b.String())
	if _, present := parsedDist[dropped]; present {
		t.Fatalf("test setup: expected %s to be dropped by parseSHASUMS", dropped)
	}

	cfg := nodePullConfig{version: nodeTestVersion, distBaseURL: nodeDistBaseURL, muslBaseURL: nodeMuslBaseURL}
	_, err := buildNodeBinaries(cfg, parsedDist, musl)
	if err == nil {
		t.Fatal("expected hard error when a required archive hash is absent from the SHASUMS map")
	}
	if !strings.Contains(err.Error(), dropped) {
		t.Errorf("error should name the missing archive %q, got: %v", dropped, err)
	}
}

func TestBuildNodeBinaries_InvalidHash(t *testing.T) {
	dist, musl := buildNodeTestShasums(nodeTestVersion)
	dist["node-v"+nodeTestVersion+"-linux-x64.tar.xz"] = "not-a-valid-sha256"

	cfg := nodePullConfig{version: nodeTestVersion, distBaseURL: nodeDistBaseURL, muslBaseURL: nodeMuslBaseURL}
	if _, err := buildNodeBinaries(cfg, dist, musl); err == nil {
		t.Fatal("expected error for malformed SHA-256 hash")
	}
}

// buildUppercaseNodeShasums assigns each Node archive tuple an UPPERCASE hex
// SHA-256-shaped hash containing letters (A–F), so the value differs from its
// lowercase form and exercises the case-normalization path. Split into the
// dist/musl manifests like the real sources.
func buildUppercaseNodeShasums(v string) (dist, musl map[string]string) {
	dist = map[string]string{}
	musl = map[string]string{}
	const hexLetters = "ABCDEF"
	for i, spec := range nodeArchiveSpecs(v) {
		h := strings.Repeat(string(hexLetters[i%len(hexLetters)]), 64)
		if spec.musl {
			musl[spec.filename] = h
		} else {
			dist[spec.filename] = h
		}
	}
	return dist, musl
}

// TestBuildNodeBinaries_UppercaseHashNormalizedAndValid pins review finding #1:
// a SHASUMS manifest with UPPERCASE hex must flow through buildNodeBinaries and
// the recorded hash must be accepted by config.ValidateRuntimes (which requires
// 64 lowercase hex characters). Without normalization the generated config is
// unloadable.
func TestBuildNodeBinaries_UppercaseHashNormalizedAndValid(t *testing.T) {
	dist, musl := buildUppercaseNodeShasums(nodeTestVersion)
	all := mergeShasums(dist, musl)

	cfg := nodePullConfig{version: nodeTestVersion, distBaseURL: nodeDistBaseURL, muslBaseURL: nodeMuslBaseURL}
	binaries, err := buildNodeBinaries(cfg, dist, musl)
	if err != nil {
		t.Fatalf("buildNodeBinaries: %v", err)
	}

	// Stored hash must equal the lowercase form of the upstream (uppercase) hash.
	for _, spec := range nodeArchiveSpecs(nodeTestVersion) {
		info, ok := binaries[spec.os][spec.arch][spec.libc]
		if !ok {
			t.Errorf("%s/%s/%s: missing entry", spec.os, spec.arch, spec.libc)
			continue
		}
		want := strings.ToLower(all[spec.filename])
		if info.Hash != want {
			t.Errorf("%s: Hash = %q, want lowercase %q", spec.filename, info.Hash, want)
		}
		if info.Hash != strings.ToLower(info.Hash) {
			t.Errorf("%s: Hash %q is not all-lowercase", spec.filename, info.Hash)
		}
	}

	// The whole runtime entry must pass config validation (no "must be 64
	// lowercase hex" errors).
	rt := config.RuntimeConfig{
		Kind: config.RuntimeKindNode,
		Mode: config.RuntimeModeManaged,
		Node: &config.RuntimeConfigNode{
			NodeVersion: nodeTestVersion,
			PNPMVersion: "11.2.2",
			PNPMHash:    strings.Repeat("a", 64),
		},
		Managed: &config.RuntimeConfigManaged{Binaries: binaries},
	}
	if err := config.ValidateRuntimes(config.MapOfRuntimes{"node": rt}); err != nil {
		t.Fatalf("ValidateRuntimes rejected normalized hashes: %v", err)
	}
}

// TestBuildNodeBinaries_InvalidUppercaseHashRejected ensures normalization does
// not paper over a genuinely malformed hash: a 64-char non-hex value (uppercase
// "G") must still hard-error.
func TestBuildNodeBinaries_InvalidUppercaseHashRejected(t *testing.T) {
	dist, musl := buildNodeTestShasums(nodeTestVersion)
	dist["node-v"+nodeTestVersion+"-linux-x64.tar.xz"] = strings.Repeat("G", 64)

	cfg := nodePullConfig{version: nodeTestVersion, distBaseURL: nodeDistBaseURL, muslBaseURL: nodeMuslBaseURL}
	if _, err := buildNodeBinaries(cfg, dist, musl); err == nil {
		t.Fatal("expected error for 64-char non-hex hash even after lowercasing")
	}
}

func TestDetectNodeBinaries_MockServers(t *testing.T) {
	signer := newTestEntity(t)
	dist, musl := buildNodeTestShasums(nodeTestVersion)
	all := mergeShasums(dist, musl)

	distAsc := clearsignText(t, signer, shasumsText(dist))
	distBase, muslBase := nodeMockServers(t, distAsc, shasumsText(musl))

	cfg := nodePullConfig{
		version:     nodeTestVersion,
		distBaseURL: distBase,
		muslBaseURL: muslBase,
		keyring:     openpgp.EntityList{signer},
		client:      &http.Client{},
	}

	binaries, err := detectNodeBinaries(context.Background(), cfg)
	if err != nil {
		t.Fatalf("detectNodeBinaries: %v", err)
	}

	for _, spec := range nodeArchiveSpecs(nodeTestVersion) {
		info, ok := binaries[spec.os][spec.arch][spec.libc]
		if !ok {
			t.Errorf("%s/%s/%s: missing entry", spec.os, spec.arch, spec.libc)
			continue
		}
		base := distBase
		if spec.musl {
			base = muslBase
		}
		wantURL := fmt.Sprintf("%s/v%s/%s", base, nodeTestVersion, spec.filename)
		if info.URL != wantURL {
			t.Errorf("%s: URL = %q, want %q", spec.filename, info.URL, wantURL)
		}
		if info.Hash != all[spec.filename] {
			t.Errorf("%s: Hash = %q, want %q", spec.filename, info.Hash, all[spec.filename])
		}
		if info.BinaryPath == nil || *info.BinaryPath != nodeBinaryPath(spec) {
			t.Errorf("%s: BinaryPath = %v, want %q", spec.filename, info.BinaryPath, nodeBinaryPath(spec))
		}
	}
}

func TestDetectNodeBinaries_BadSignature(t *testing.T) {
	signer := newTestEntity(t)
	stranger := newTestEntity(t)
	dist, musl := buildNodeTestShasums(nodeTestVersion)

	// Manifest is signed by `signer`, but the verification keyring only knows
	// `stranger` → provenance check must fail.
	distAsc := clearsignText(t, signer, shasumsText(dist))
	distBase, muslBase := nodeMockServers(t, distAsc, shasumsText(musl))

	cfg := nodePullConfig{
		version:     nodeTestVersion,
		distBaseURL: distBase,
		muslBaseURL: muslBase,
		keyring:     openpgp.EntityList{stranger},
		client:      &http.Client{},
	}

	if _, err := detectNodeBinaries(context.Background(), cfg); err == nil {
		t.Fatal("expected error when dist SHASUMS signature is from an untrusted key")
	}
}

func TestDetectNodeBinaries_MissingMuslAssetFromServer(t *testing.T) {
	signer := newTestEntity(t)
	dist, musl := buildNodeTestShasums(nodeTestVersion)
	delete(musl, "node-v"+nodeTestVersion+"-linux-arm64-musl.tar.xz")

	distAsc := clearsignText(t, signer, shasumsText(dist))
	distBase, muslBase := nodeMockServers(t, distAsc, shasumsText(musl))

	cfg := nodePullConfig{
		version:     nodeTestVersion,
		distBaseURL: distBase,
		muslBaseURL: muslBase,
		keyring:     openpgp.EntityList{signer},
		client:      &http.Client{},
	}

	if _, err := detectNodeBinaries(context.Background(), cfg); err == nil {
		t.Fatal("expected error when an expected musl asset is absent from the manifest")
	}
}

func TestBuildNodeRuntimeJSON(t *testing.T) {
	data := &NodeRuntimeData{
		NodeVersion: "26.2.0",
		PNPMVersion: "11.2.2",
		PNPMHash:    testHash1,
	}
	result := buildNodeRuntimeJSON(data, make(binmanager.MapOfBinaries))

	if result.Kind != "node" {
		t.Errorf("Kind = %q, want %q", result.Kind, "node")
	}
	if result.Mode != "managed" {
		t.Errorf("Mode = %q, want %q", result.Mode, "managed")
	}
	if result.Node == nil {
		t.Fatal("Node config should not be nil")
	}
	if result.Node.NodeVersion != "26.2.0" {
		t.Errorf("NodeVersion = %q, want %q", result.Node.NodeVersion, "26.2.0")
	}
	if result.Node.PNPMVersion != "11.2.2" {
		t.Errorf("PNPMVersion = %q", result.Node.PNPMVersion)
	}
	if result.UV != nil || result.JVM != nil {
		t.Error("only Node config should be set for a node runtime")
	}
}

func TestRuntimeVersion_Node(t *testing.T) {
	r := &RuntimeJSON{
		Node: &NodeConfigJSON{NodeVersion: "26.2.0", PNPMVersion: "11.2.2"},
	}
	v := runtimeVersion(r)
	if !strings.Contains(v, "node=26.2.0") {
		t.Errorf("expected node version, got %q", v)
	}
	if !strings.Contains(v, "pnpm=11.2.2") {
		t.Errorf("expected pnpm version, got %q", v)
	}
}
