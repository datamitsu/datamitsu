package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"

	"go.uber.org/zap/zapcore"
)

const testOCIDigest = "sha256:abababababababababababababababababababababababababababababababab"

// runOCIConfigSource evaluates one inline config source with the given input,
// returning the parsed config. The source must define getConfig; getMinVersion
// is prepended automatically.
func runOCIConfigSource(t *testing.T, input *config.Config, name, js string) *config.Config {
	t.Helper()
	content := "function getMinVersion() { return \"0.0.0\"; }\n" + js
	result, _, err := processConfigSource(context.Background(), input, configSource{
		name:    name,
		content: content,
	}, make(map[string]bool), make(map[string]bool))
	if err != nil {
		t.Fatalf("processConfigSource(%s) error: %v", name, err)
	}
	return result
}

func TestProcessConfigSourceOCIParsing(t *testing.T) {
	result := runOCIConfigSource(t, nil, "oci-parse", `
function getConfig(input) {
    return {
        oci: {
            ref: "ghcr.io/owner/repo",
            digest: "`+testOCIDigest+`",
            signer: { identity: "workflow-ref", issuer: "https://token.actions.githubusercontent.com" },
        },
    };
}`)

	if result.OCI == nil {
		t.Fatal("OCI = nil, want parsed declaration")
	}
	if result.OCI.Ref != "ghcr.io/owner/repo" {
		t.Errorf("OCI.Ref = %q, want %q", result.OCI.Ref, "ghcr.io/owner/repo")
	}
	if result.OCI.Digest != testOCIDigest {
		t.Errorf("OCI.Digest = %q, want %q", result.OCI.Digest, testOCIDigest)
	}
	if result.OCI.Signer == nil {
		t.Fatal("OCI.Signer = nil, want parsed signer")
	}
	if result.OCI.Signer.Identity != "workflow-ref" {
		t.Errorf("OCI.Signer.Identity = %q, want %q", result.OCI.Signer.Identity, "workflow-ref")
	}
	if result.OCI.Signer.Issuer != "https://token.actions.githubusercontent.com" {
		t.Errorf("OCI.Signer.Issuer = %q", result.OCI.Signer.Issuer)
	}
}

func TestProcessConfigSourceOCILastWins(t *testing.T) {
	first := runOCIConfigSource(t, nil, "oci-first", `
function getConfig(input) {
    return { ...input, oci: { ref: "ghcr.io/owner/first", digest: "`+testOCIDigest+`" } };
}`)
	second := runOCIConfigSource(t, first, "oci-second", `
function getConfig(input) {
    return { ...input, oci: { ref: "ghcr.io/owner/second", digest: "`+testOCIDigest+`" } };
}`)

	if second.OCI == nil {
		t.Fatal("OCI = nil after second layer")
	}
	if second.OCI.Ref != "ghcr.io/owner/second" {
		t.Errorf("OCI.Ref = %q, want the last layer to win", second.OCI.Ref)
	}
}

func TestProcessConfigSourceOCISpreadInherits(t *testing.T) {
	first := runOCIConfigSource(t, nil, "oci-set", `
function getConfig(input) {
    return { ...input, oci: { ref: "ghcr.io/owner/repo", digest: "`+testOCIDigest+`" } };
}`)
	second := runOCIConfigSource(t, first, "oci-inherit", `
function getConfig(input) {
    return { ...input, ignoreRules: ["docs/**: *"] };
}`)

	if second.OCI == nil {
		t.Fatal("OCI = nil, want inherited declaration through {...input}")
	}
	if second.OCI.Ref != "ghcr.io/owner/repo" || second.OCI.Digest != testOCIDigest {
		t.Errorf("inherited OCI = %+v, want original declaration", second.OCI)
	}
}

func TestProcessConfigSourceOCINonSpreadDrops(t *testing.T) {
	observed := swapLoggerWithObserver(t, zapcore.DebugLevel)

	first := runOCIConfigSource(t, nil, "oci-set", `
function getConfig(input) {
    return { ...input, oci: { ref: "ghcr.io/owner/repo", digest: "`+testOCIDigest+`" } };
}`)
	second := runOCIConfigSource(t, first, "oci-drop", `
function getConfig(input) {
    return { ignoreRules: ["docs/**: *"] };
}`)

	if second.OCI != nil {
		t.Fatalf("OCI = %+v, want nil after a layer that does not spread input", second.OCI)
	}

	found := false
	for _, entry := range observed.All() {
		if strings.Contains(entry.Message, "dropped inherited oci") {
			found = true
		}
	}
	if !found {
		t.Error("expected a debug log about the dropped inherited oci declaration")
	}
}

func TestProcessConfigSourceOCIUndefinedAndNullReset(t *testing.T) {
	for name, resetJS := range map[string]string{
		"undefined": "undefined",
		"null":      "null",
	} {
		t.Run(name, func(t *testing.T) {
			first := runOCIConfigSource(t, nil, "oci-set", `
function getConfig(input) {
    return { ...input, oci: { ref: "ghcr.io/owner/repo", digest: "`+testOCIDigest+`" } };
}`)
			second := runOCIConfigSource(t, first, "oci-reset", `
function getConfig(input) {
    return { ...input, oci: `+resetJS+` };
}`)
			if second.OCI != nil {
				t.Errorf("OCI = %+v, want nil after explicit %s reset", second.OCI, name)
			}
		})
	}
}

// TestProcessConfigSourceOCINilUnderSpread pins the goja behavior the chaining
// contract relies on: a Go Config with a nil *OCIRef is reflected into JS with
// an own-enumerable `oci` key holding null (omitempty affects encoding/json
// only, not VM enumeration), and exporting that null back collapses to Go nil.
func TestProcessConfigSourceOCINilUnderSpread(t *testing.T) {
	first := runOCIConfigSource(t, nil, "oci-absent", `
function getConfig(input) {
    return { ...input, ignoreRules: [] };
}`)
	if first.OCI != nil {
		t.Fatalf("OCI = %+v, want nil for a config that never set it", first.OCI)
	}

	second := runOCIConfigSource(t, first, "oci-nil-spread", `
function getConfig(input) {
    const spread = { ...input };
    return {
        ...input,
        sharedStorage: {
            ociKeyOnInput: String("oci" in input),
            ociKeyOnSpread: Object.prototype.hasOwnProperty.call(spread, "oci") ? "true" : "false",
            ociIsNull: String(input.oci === null || input.oci === undefined),
        },
    };
}`)

	if second.OCI != nil {
		t.Errorf("OCI = %+v, want nil after spreading a nil oci", second.OCI)
	}
	if got := second.SharedStorage["ociKeyOnInput"]; got != "true" {
		t.Errorf("ociKeyOnInput = %q, want \"true\" (goja enumerates all exported fields)", got)
	}
	if got := second.SharedStorage["ociKeyOnSpread"]; got != "true" {
		t.Errorf("ociKeyOnSpread = %q, want \"true\"", got)
	}
	if got := second.SharedStorage["ociIsNull"]; got != "true" {
		t.Errorf("ociIsNull = %q, want \"true\" (nil pointer surfaces as null/undefined)", got)
	}
}
