package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/github"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func pickAsset(name string) github.Asset {
	return github.Asset{
		Name:               name,
		BrowserDownloadURL: "https://example.test/" + name,
		Digest:             "sha256:" + strings.Repeat("a", 64),
	}
}

// archives fail verification, raw binaries pass — models yq/terragrunt, whose
// archive contains a differently-named binary than the guessed path.
func archiveFailsVerifier(_ context.Context, _, _ string, contentType binmanager.BinContentType, binaryPath *string) error {
	if contentType != binmanager.BinContentTypeBinary {
		bp := "<nil>"
		if binaryPath != nil {
			bp = *binaryPath
		}
		return errors.New("guessed path " + bp + " not found in archive")
	}
	return nil
}

var glibcAmd64 = platformTuple{os: syslist.OsTypeLinux, arch: syslist.ArchTypeAmd64, libc: "glibc"}

func TestPickBinaryForPlatform_FallsBackToRawBinaryOnVerifyFailure(t *testing.T) {
	candidates := []github.Asset{
		pickAsset("yq_linux_amd64.tar.gz"), // preferred, but archive verify fails
		pickAsset("yq_linux_amd64.zip"),    // also fails
		pickAsset("yq_linux_amd64"),        // raw binary rescues the platform
	}

	pick, status, err := pickBinaryForPlatform(
		context.Background(), "yq", candidates, glibcAmd64, nil, archiveFailsVerifier)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "success" {
		t.Fatalf("status = %q, want success", status)
	}
	if pick.asset.Name != "yq_linux_amd64" {
		t.Errorf("picked %q, want raw binary yq_linux_amd64", pick.asset.Name)
	}
	if pick.contentType != binmanager.BinContentTypeBinary {
		t.Errorf("contentType = %v, want binary", pick.contentType)
	}
	if pick.binaryPath != nil {
		t.Errorf("binaryPath = %v, want nil for a raw binary", *pick.binaryPath)
	}
}

func TestPickBinaryForPlatform_NoVerifierKeepsTopCandidate(t *testing.T) {
	candidates := []github.Asset{
		pickAsset("yq_linux_amd64.tar.gz"),
		pickAsset("yq_linux_amd64"),
	}
	pick, status, err := pickBinaryForPlatform(
		context.Background(), "yq", candidates, glibcAmd64, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "success" || pick.asset.Name != "yq_linux_amd64.tar.gz" {
		t.Fatalf("got (%q, %q), want (success, yq_linux_amd64.tar.gz)", status, pick.asset.Name)
	}
}

func TestPickBinaryForPlatform_NoHashWhenNoCandidateHasDigest(t *testing.T) {
	candidates := []github.Asset{
		{Name: "yq_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.test/a"},
		{Name: "yq_linux_amd64", BrowserDownloadURL: "https://example.test/b"},
	}
	pick, status, err := pickBinaryForPlatform(
		context.Background(), "yq", candidates, glibcAmd64, nil, nil)
	if pick != nil {
		t.Fatalf("expected nil pick, got %q", pick.asset.Name)
	}
	if status != "no_hash" || err == nil {
		t.Fatalf("got (%q, err=%v), want (no_hash, non-nil)", status, err)
	}
}

func TestPickBinaryForPlatform_VerificationFailedWhenAllCandidatesFail(t *testing.T) {
	candidates := []github.Asset{
		pickAsset("yq_linux_amd64.tar.gz"),
		pickAsset("yq_linux_amd64"),
	}
	allFail := func(_ context.Context, _, _ string, _ binmanager.BinContentType, _ *string) error {
		return errors.New("boom")
	}
	_, status, err := pickBinaryForPlatform(
		context.Background(), "yq", candidates, glibcAmd64, nil, allFail)
	if status != "verification_failed" || err == nil {
		t.Fatalf("got (%q, err=%v), want (verification_failed, non-nil)", status, err)
	}
}

func TestPickBinaryForPlatform_SkipsLibcMismatch(t *testing.T) {
	t.Run("falls through to matching libc", func(t *testing.T) {
		candidates := []github.Asset{
			pickAsset("tool-linux-amd64-musl.tar.gz"), // wrong libc, skipped
			pickAsset("tool-linux-amd64-gnu.tar.gz"),  // glibc match
		}
		pick, status, err := pickBinaryForPlatform(
			context.Background(), "tool", candidates, glibcAmd64, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != "success" || !strings.Contains(pick.asset.Name, "gnu") {
			t.Fatalf("got (%q, %q), want (success, *gnu*)", status, pick.asset.Name)
		}
	})

	t.Run("only mismatching libc is not_available", func(t *testing.T) {
		candidates := []github.Asset{pickAsset("tool-linux-amd64-musl.tar.gz")}
		pick, status, err := pickBinaryForPlatform(
			context.Background(), "tool", candidates, glibcAmd64, nil, nil)
		if pick != nil {
			t.Fatalf("expected nil pick, got %q", pick.asset.Name)
		}
		if status != "not_available" || err == nil {
			t.Fatalf("got (%q, err=%v), want (not_available, non-nil)", status, err)
		}
	})
}
