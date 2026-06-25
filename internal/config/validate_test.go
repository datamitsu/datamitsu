package config

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func TestValidateApps_Valid(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Files: map[string]string{
				"eslint-base.js": "module.exports = {};",
			},
			Links: map[string]string{
				"eslint-base.js": "eslint-base.js",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_NoLinksNoFiles(t *testing.T) {
	apps := binmanager.MapOfApps{
		"lefthook": {
			Required: true,
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_FilesWithoutLinks(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Files: map[string]string{
				"config.js": "module.exports = {};",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_LinkPathIsRelative(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Links: map[string]string{
				"eslint-base.js": "dist/eslint.config.js",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error for valid relative link path: %v", err)
	}
}

func TestValidateApps_LinkWithNilFiles(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Links: map[string]string{
				"eslint-base.js": "eslint-base.js",
			},
		},
	}

	// With the new behavior, link values are relative paths - no Files required
	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_DuplicateLinkName(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Files: map[string]string{
				"base.js": "module.exports = {};",
			},
			Links: map[string]string{
				"shared-config.js": "base.js",
			},
		},
		"prettier": {
			Files: map[string]string{
				"base.js": "module.exports = {};",
			},
			Links: map[string]string{
				"shared-config.js": "base.js",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error, got nil")
	}
	if !strings.Contains(err.Error(), `link name "shared-config.js" defined in both`) {
		t.Errorf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), "eslint") || !strings.Contains(err.Error(), "prettier") {
		t.Errorf("error should mention both app names: %v", err)
	}
}

func TestValidateApps_MultipleErrors(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Links: map[string]string{
				"config.js": "../escape",
			},
		},
		"prettier": {
			Required: true,
			Links: map[string]string{
				"prettier.js": "../../also-escape",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "eslint") {
		t.Errorf("error should mention eslint: %v", err)
	}
	if !strings.Contains(errStr, "prettier") {
		t.Errorf("error should mention prettier: %v", err)
	}
}

func TestValidateApps_MultipleLinksValid(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Files: map[string]string{
				"base.js":    "module.exports = {};",
				"react.js":   "module.exports = {};",
				"ts-base.js": "module.exports = {};",
			},
			Links: map[string]string{
				"eslint-base.js":    "base.js",
				"eslint-react.js":   "react.js",
				"eslint-ts-base.js": "ts-base.js",
			},
		},
		"prettier": {
			Required: true,
			Files: map[string]string{
				"config.js": "module.exports = {};",
			},
			Links: map[string]string{
				"prettier-config.js": "config.js",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_EmptyApps(t *testing.T) {
	if _, err := ValidateApps(binmanager.MapOfApps{}, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_NilApps(t *testing.T) {
	if _, err := ValidateApps(nil, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_FileKeyPathTraversal(t *testing.T) {
	apps := binmanager.MapOfApps{
		"evil": {
			Files: map[string]string{
				"../../etc/passwd": "malicious",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "unsafe path components") {
		t.Errorf("error should mention unsafe path: %v", err)
	}
}

func TestValidateApps_LinkPathTraversal(t *testing.T) {
	apps := binmanager.MapOfApps{
		"evil": {
			Required: true,
			Links: map[string]string{
				"link.js": "../../etc/passwd",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for link path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "escapes parent directory") {
		t.Errorf("error should mention escape: %v", err)
	}
}

func TestValidateApps_FileKeyAbsolutePath(t *testing.T) {
	apps := binmanager.MapOfApps{
		"evil": {
			Files: map[string]string{
				"/etc/passwd": "malicious",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for absolute path, got nil")
	}
	if !strings.Contains(err.Error(), "unsafe path components") {
		t.Errorf("error should mention unsafe path: %v", err)
	}
}

func TestValidateApps_EmptyLinkName(t *testing.T) {
	apps := binmanager.MapOfApps{
		"app": {
			Files: map[string]string{
				"config.js": "content",
			},
			Links: map[string]string{
				"": "config.js",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for empty link name, got nil")
	}
	if !strings.Contains(err.Error(), "link name must not be empty") {
		t.Errorf("error should mention empty link name: %v", err)
	}
}

func TestValidateApps_LinkNamePathTraversal(t *testing.T) {
	apps := binmanager.MapOfApps{
		"evil": {
			Files: map[string]string{
				"config.js": "content",
			},
			Links: map[string]string{
				"../../etc/cron.d/evil": "config.js",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for linkName path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "unsafe path components") {
		t.Errorf("error should mention unsafe path: %v", err)
	}
}

func TestValidateApps_LinkNameAbsolutePath(t *testing.T) {
	apps := binmanager.MapOfApps{
		"evil": {
			Files: map[string]string{
				"config.js": "content",
			},
			Links: map[string]string{
				"/etc/passwd": "config.js",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for absolute linkName, got nil")
	}
	if !strings.Contains(err.Error(), "unsafe path components") {
		t.Errorf("error should mention unsafe path: %v", err)
	}
}

func TestValidateApps_ReservedGitignoreLinkName(t *testing.T) {
	apps := binmanager.MapOfApps{
		"myapp": {
			Files: map[string]string{
				"config.js": "content",
			},
			Links: map[string]string{
				".gitignore": "config.js",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for reserved .gitignore link name, got nil")
	}
	if !strings.Contains(err.Error(), "reserved for internal use") {
		t.Errorf("error should mention reserved: %v", err)
	}
}

func TestValidateApps_ReservedTypeDefinitionsLinkName(t *testing.T) {
	apps := binmanager.MapOfApps{
		"myapp": {
			Files: map[string]string{
				"config.js": "content",
			},
			Links: map[string]string{
				"datamitsu.config.d.ts": "config.js",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for reserved datamitsu.config.d.ts link name, got nil")
	}
	if !strings.Contains(err.Error(), "reserved for internal use") {
		t.Errorf("error should mention reserved: %v", err)
	}
}

func TestValidateApps_BinaryAppWithFilesRejected(t *testing.T) {
	apps := binmanager.MapOfApps{
		"tool": {
			Binary: &binmanager.AppConfigBinary{},
			Files: map[string]string{
				"config.js": "content",
			},
			Links: map[string]string{
				"config.js": "config.js",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for binary app with files/links, got nil")
	}
	if !strings.Contains(err.Error(), "files/links/archives are only supported on uv and node apps") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_ShellAppWithFilesRejected(t *testing.T) {
	apps := binmanager.MapOfApps{
		"tool": {
			Shell: &binmanager.AppConfigShell{Name: "echo"},
			Files: map[string]string{
				"config.js": "content",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for shell app with files, got nil")
	}
	if !strings.Contains(err.Error(), "files/links/archives are only supported on uv and node apps") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_LinksWithRequiredFalse(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: false,
			Files: map[string]string{
				"base.js": "module.exports = {};",
			},
			Links: map[string]string{
				"eslint-base.js": "base.js",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error for links with required=false: %v", err)
	}
}

func TestValidateApps_LinksWithNoRequiredField(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Files: map[string]string{
				"base.js": "module.exports = {};",
			},
			Links: map[string]string{
				"eslint-base.js": "base.js",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error for links without required field: %v", err)
	}
}

func TestValidateApps_LinksWithRequiredTrue(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Files: map[string]string{
				"base.js": "module.exports = {};",
			},
			Links: map[string]string{
				"eslint-base.js": "base.js",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_LinkPathValidCases(t *testing.T) {
	tests := []struct {
		name     string
		linkPath string
	}{
		{"simple file", "config.js"},
		{"nested path", "dist/eslint.config.js"},
		{"deeply nested", "dist/nested/deep/config.js"},
		{"dot prefix", "./config.js"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{
				"app": {
					Required: true,
					Links: map[string]string{
						"link": tt.linkPath,
					},
				},
			}

			if _, err := ValidateApps(apps, nil); err != nil {
				t.Errorf("ValidateApps() unexpected error for link path %q: %v", tt.linkPath, err)
			}
		})
	}
}

func TestValidateApps_LinkPathInvalidCases(t *testing.T) {
	tests := []struct {
		name        string
		linkPath    string
		errContains string
	}{
		{"empty path", "", "path must not be empty"},
		{"absolute path", "/etc/passwd", "must be a relative path"},
		{"parent traversal", "../escape", "escapes parent directory"},
		{"deep traversal", "../../etc/passwd", "escapes parent directory"},
		{"mid traversal", "dist/../../etc/passwd", "escapes parent directory"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{
				"app": {
					Required: true,
					Links: map[string]string{
						"link": tt.linkPath,
					},
				},
			}

			_, err := ValidateApps(apps, nil)
			if err == nil {
				t.Fatalf("ValidateApps() expected error for link path %q, got nil", tt.linkPath)
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error should contain %q, got: %v", tt.errContains, err)
			}
		})
	}
}

func TestValidateApps_EmptyFileKey(t *testing.T) {
	apps := binmanager.MapOfApps{
		"app": {
			Files: map[string]string{
				"": "content",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for empty file key, got nil")
	}
	if !strings.Contains(err.Error(), "file key must not be empty") {
		t.Errorf("error should mention empty file key: %v", err)
	}
}

func testManagedConfig() *RuntimeConfigManaged {
	return &RuntimeConfigManaged{
		Binaries: binmanager.MapOfBinaries{},
	}
}

func TestValidateRuntimes_Valid(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind:    RuntimeKindNode,
			Mode:    RuntimeModeManaged,
			Managed: testManagedConfig(),
			Node: &RuntimeConfigNode{
				NodeVersion: "22.14.0",
				PNPMVersion: "10.7.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
		"uv": {
			Kind:    RuntimeKindUV,
			Mode:    RuntimeModeManaged,
			Managed: testManagedConfig(),
		},
	}

	if err := ValidateRuntimes(runtimes); err != nil {
		t.Errorf("ValidateRuntimes() unexpected error: %v", err)
	}
}

func TestValidateRuntimes_Node_Valid(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind:    RuntimeKindNode,
			Mode:    RuntimeModeManaged,
			Managed: testManagedConfig(),
			Node: &RuntimeConfigNode{
				NodeVersion: "26.2.0",
				PNPMVersion: "11.0.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
	}

	if err := ValidateRuntimes(runtimes); err != nil {
		t.Errorf("ValidateRuntimes() unexpected error: %v", err)
	}
}

func TestValidateRuntimes_Node_MissingConfig(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
		},
	}

	err := ValidateRuntimes(runtimes)
	if err == nil {
		t.Fatal("ValidateRuntimes() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Node runtime requires node config with nodeVersion, pnpmVersion, and pnpmHash") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateRuntimes_Node_MissingNodeVersion(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
			Node: &RuntimeConfigNode{
				PNPMVersion: "11.0.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
	}

	err := ValidateRuntimes(runtimes)
	if err == nil {
		t.Fatal("ValidateRuntimes() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "node.nodeVersion is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateRuntimes_Node_MissingPNPMVersion(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
			Node: &RuntimeConfigNode{
				NodeVersion: "26.2.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
	}

	err := ValidateRuntimes(runtimes)
	if err == nil {
		t.Fatal("ValidateRuntimes() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "node.pnpmVersion is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateRuntimes_Node_MissingPNPMHash(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
			Node: &RuntimeConfigNode{
				NodeVersion: "26.2.0",
				PNPMVersion: "11.0.0",
			},
		},
	}

	err := ValidateRuntimes(runtimes)
	if err == nil {
		t.Fatal("ValidateRuntimes() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "node.pnpmHash is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateRuntimes_Node_InvalidPNPMHashFormat(t *testing.T) {
	tests := []struct {
		name     string
		pnpmHash string
	}{
		{"too short", "abc123"},
		{"path traversal", "../../../../../../tmp/evil/../../../../../../../tmp/evil"},
		{"contains slash", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6/xx"},
		{"non-hex chars", "g1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"},
		{"too long", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b200"},
		{"uppercase", "A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimes := MapOfRuntimes{
				"node": {
					Kind: RuntimeKindNode,
					Mode: RuntimeModeManaged,
					Node: &RuntimeConfigNode{
						NodeVersion: "26.2.0",
						PNPMVersion: "11.0.0",
						PNPMHash:    tt.pnpmHash,
					},
				},
			}

			err := ValidateRuntimes(runtimes)
			if err == nil {
				t.Fatalf("ValidateRuntimes() expected error for pnpmHash %q, got nil", tt.pnpmHash)
			}
			if !strings.Contains(err.Error(), "node.pnpmHash must be a valid SHA-256 hex string") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

func TestValidateRuntimes_Node_InvalidNodeVersion(t *testing.T) {
	tests := []struct {
		name        string
		nodeVersion string
	}{
		{"path traversal", "../../etc/passwd"},
		{"contains slash", "26/2/0"},
		{"shell metacharacter", "26.2.0; rm -rf /"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimes := MapOfRuntimes{
				"node": {
					Kind: RuntimeKindNode,
					Mode: RuntimeModeManaged,
					Node: &RuntimeConfigNode{
						NodeVersion: tt.nodeVersion,
						PNPMVersion: "11.0.0",
						PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
					},
				},
			}

			err := ValidateRuntimes(runtimes)
			if err == nil {
				t.Fatalf("ValidateRuntimes() expected error for nodeVersion %q, got nil", tt.nodeVersion)
			}
			if !strings.Contains(err.Error(), "node.nodeVersion") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

func TestValidateRuntimes_Node_InvalidPNPMVersion(t *testing.T) {
	tests := []struct {
		name        string
		pnpmVersion string
	}{
		{"path traversal", "../../etc/passwd"},
		{"contains slash", "11/0/0"},
		{"shell metacharacter", "11.0.0 && curl evil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimes := MapOfRuntimes{
				"node": {
					Kind: RuntimeKindNode,
					Mode: RuntimeModeManaged,
					Node: &RuntimeConfigNode{
						NodeVersion: "26.2.0",
						PNPMVersion: tt.pnpmVersion,
						PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
					},
				},
			}

			err := ValidateRuntimes(runtimes)
			if err == nil {
				t.Fatalf("ValidateRuntimes() expected error for pnpmVersion %q, got nil", tt.pnpmVersion)
			}
			if !strings.Contains(err.Error(), "node.pnpmVersion") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

func TestValidateRuntimes_Node_ValidVersionFormats(t *testing.T) {
	versions := []string{"26.2.0", "26.2.0-nightly", "22.14.0", "v1_test"}

	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			runtimes := MapOfRuntimes{
				"node": {
					Kind:    RuntimeKindNode,
					Mode:    RuntimeModeManaged,
					Managed: testManagedConfig(),
					Node: &RuntimeConfigNode{
						NodeVersion: v,
						PNPMVersion: v,
						PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
					},
				},
			}

			if err := ValidateRuntimes(runtimes); err != nil {
				t.Errorf("ValidateRuntimes() unexpected error: %v", err)
			}
		})
	}
}

// TestValidateRuntimes_UnrecognizedKindSkipped verifies that an unrecognized
// runtime kind gets none of the node.* validation. A runtime entry whose Kind
// does not match any known RuntimeKind* constant is treated as an unrecognized
// kind, so ValidateRuntimes applies none of the node.* field validation to it —
// even with no node config present, it must not produce node.nodeVersion /
// node.pnpmVersion / node.pnpmHash errors.
func TestValidateRuntimes_UnrecognizedKindSkipped(t *testing.T) {
	runtimes := MapOfRuntimes{
		"legacy": {
			Kind:    "legacy-removed",
			Mode:    RuntimeModeManaged,
			Managed: testManagedConfig(),
		},
	}

	if err := ValidateRuntimes(runtimes); err != nil {
		t.Errorf("ValidateRuntimes() should not apply node validation to unrecognized kind, got: %v", err)
	}
}

func TestValidateRuntimes_Empty(t *testing.T) {
	if err := ValidateRuntimes(MapOfRuntimes{}); err != nil {
		t.Errorf("ValidateRuntimes() unexpected error: %v", err)
	}
}

func TestValidateRuntimes_Nil(t *testing.T) {
	if err := ValidateRuntimes(nil); err != nil {
		t.Errorf("ValidateRuntimes() unexpected error: %v", err)
	}
}

func TestValidateRuntimes_UV_NoPythonVersionOK(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind:    RuntimeKindUV,
			Mode:    RuntimeModeManaged,
			Managed: testManagedConfig(),
		},
	}

	if err := ValidateRuntimes(runtimes); err != nil {
		t.Errorf("ValidateRuntimes() unexpected error: %v", err)
	}
}

func TestValidateApps_Lockfile_UV_Missing(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
		},
	}
	apps := binmanager.MapOfApps{
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.35.1",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for missing lockFile, got nil")
	}
	if !strings.Contains(err.Error(), `app "yamllint": lockFile is required`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_Lockfile_UV_Present(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
		},
	}
	apps := binmanager.MapOfApps{
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.35.1",
				LockFile:    "br:some-compressed-content",
			},
		},
	}

	if _, err := ValidateApps(apps, runtimes); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateAppsSkipLockfile_AllowsMissingLockfile(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
		},
	}
	apps := binmanager.MapOfApps{
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.35.1",
			},
		},
	}

	if _, err := ValidateAppsSkipLockfile(apps, runtimes); err != nil {
		t.Errorf("ValidateAppsSkipLockfile() unexpected error: %v", err)
	}
}

func TestValidateAppsSkipLockfile_AllowsMissingLockfile_Node(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
			Node: &RuntimeConfigNode{
				NodeVersion: "22.14.0",
				PNPMVersion: "10.7.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
	}
	apps := binmanager.MapOfApps{
		"eslint": {
			Node: &binmanager.AppConfigNode{
				PackageName: "eslint",
				Version:     "9.0.0",
				BinPath:     "node_modules/.bin/eslint",
			},
		},
	}

	if _, err := ValidateAppsSkipLockfile(apps, runtimes); err != nil {
		t.Errorf("ValidateAppsSkipLockfile() unexpected error: %v", err)
	}
}

func TestValidateApps_Lockfile_Node_Missing(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
			Node: &RuntimeConfigNode{
				NodeVersion: "22.14.0",
				PNPMVersion: "10.7.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
	}
	apps := binmanager.MapOfApps{
		"eslint": {
			Node: &binmanager.AppConfigNode{
				PackageName: "eslint",
				Version:     "9.0.0",
				BinPath:     "node_modules/.bin/eslint",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for missing lockFile, got nil")
	}
	if !strings.Contains(err.Error(), `app "eslint": lockFile is required`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_Lockfile_Node_Present(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
			Node: &RuntimeConfigNode{
				NodeVersion: "22.14.0",
				PNPMVersion: "10.7.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
	}
	apps := binmanager.MapOfApps{
		"eslint": {
			Node: &binmanager.AppConfigNode{
				PackageName: "eslint",
				Version:     "9.0.0",
				BinPath:     "node_modules/.bin/eslint",
				LockFile:    "br:some-lock-content",
			},
		},
	}

	if _, err := ValidateApps(apps, runtimes); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_Lockfile_ExplicitRuntime(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv-main": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
		},
	}
	apps := binmanager.MapOfApps{
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.35.1",
				Runtime:     "uv-main",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for missing lockFile with explicit runtime, got nil")
	}
	if !strings.Contains(err.Error(), `app "yamllint": lockFile is required`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_JVM_Valid(t *testing.T) {
	apps := binmanager.MapOfApps{
		"openapi-generator": {
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				JarHash: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
				Version: "7.0.0",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_JVM_MissingJarUrl(t *testing.T) {
	apps := binmanager.MapOfApps{
		"openapi-generator": {
			Jvm: &binmanager.AppConfigJVM{
				JarHash: "abc123",
				Version: "7.0.0",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for missing jarUrl, got nil")
	}
	if !strings.Contains(err.Error(), "jvm.jarUrl is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_JVM_MissingJarHash(t *testing.T) {
	apps := binmanager.MapOfApps{
		"openapi-generator": {
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				Version: "7.0.0",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for missing jarHash, got nil")
	}
	if !strings.Contains(err.Error(), "jvm.jarHash is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_JVM_MissingVersion(t *testing.T) {
	apps := binmanager.MapOfApps{
		"openapi-generator": {
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				JarHash: "abc123",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for missing version, got nil")
	}
	if !strings.Contains(err.Error(), "jvm.version is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_JVM_FilesLinksRejected(t *testing.T) {
	apps := binmanager.MapOfApps{
		"openapi-generator": {
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				JarHash: "abc123",
				Version: "7.0.0",
			},
			Files: map[string]string{
				"config.yaml": "content",
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() expected error for JVM app with files, got nil")
	}
	if !strings.Contains(err.Error(), "files/links/archives are only supported on uv and node apps") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateRuntimes_JVM_Valid(t *testing.T) {
	runtimes := MapOfRuntimes{
		"jvm": {
			Kind:    RuntimeKindJVM,
			Mode:    RuntimeModeManaged,
			Managed: testManagedConfig(),
			JVM: &RuntimeConfigJVM{
				JavaVersion: "21",
			},
		},
	}

	if err := ValidateRuntimes(runtimes); err != nil {
		t.Errorf("ValidateRuntimes() unexpected error: %v", err)
	}
}

func TestValidateRuntimes_JVM_MissingConfig(t *testing.T) {
	runtimes := MapOfRuntimes{
		"jvm": {
			Kind: RuntimeKindJVM,
			Mode: RuntimeModeManaged,
		},
	}

	err := ValidateRuntimes(runtimes)
	if err == nil {
		t.Fatal("ValidateRuntimes() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "JVM runtime requires jvm config") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_UnknownRuntimeRef_UV(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
		},
	}
	apps := binmanager.MapOfApps{
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.35.1",
				Runtime:     "nonexistent",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for unknown runtime ref, got nil")
	}
	if !strings.Contains(err.Error(), `references unknown runtime "nonexistent"`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_WrongKindRuntimeRef_UV(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
			Node: &RuntimeConfigNode{
				NodeVersion: "22.14.0",
				PNPMVersion: "10.7.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
	}
	apps := binmanager.MapOfApps{
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.35.1",
				Runtime:     "node",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for wrong-kind runtime ref, got nil")
	}
	if !strings.Contains(err.Error(), `runtime "node" is kind "node", expected "uv"`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_UnknownRuntimeRef_Node(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
			Node: &RuntimeConfigNode{
				NodeVersion: "22.14.0",
				PNPMVersion: "10.7.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
	}
	apps := binmanager.MapOfApps{
		"eslint": {
			Node: &binmanager.AppConfigNode{
				PackageName: "eslint",
				Version:     "9.0.0",
				BinPath:     "node_modules/.bin/eslint",
				Runtime:     "missing-runtime",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for unknown runtime ref, got nil")
	}
	if !strings.Contains(err.Error(), `references unknown runtime "missing-runtime"`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_UnknownRuntimeRef_JVM(t *testing.T) {
	runtimes := MapOfRuntimes{
		"jvm": {
			Kind: RuntimeKindJVM,
			Mode: RuntimeModeManaged,
			JVM: &RuntimeConfigJVM{
				JavaVersion: "21",
			},
		},
	}
	apps := binmanager.MapOfApps{
		"openapi-generator": {
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				JarHash: "abc123",
				Version: "7.0.0",
				Runtime: "nonexistent-jvm",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for unknown JVM runtime ref, got nil")
	}
	if !strings.Contains(err.Error(), `references unknown runtime "nonexistent-jvm"`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_WrongKindRuntimeRef_JVM(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
		},
	}
	apps := binmanager.MapOfApps{
		"openapi-generator": {
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				JarHash: "abc123",
				Version: "7.0.0",
				Runtime: "uv",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for wrong-kind runtime ref, got nil")
	}
	if !strings.Contains(err.Error(), `runtime "uv" is kind "uv", expected "jvm"`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_ValidExplicitRuntimeRef(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
		},
		"node": {
			Kind: RuntimeKindNode,
			Mode: RuntimeModeManaged,
			Node: &RuntimeConfigNode{
				NodeVersion: "22.14.0",
				PNPMVersion: "10.7.0",
				PNPMHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
		"jvm": {
			Kind: RuntimeKindJVM,
			Mode: RuntimeModeManaged,
			JVM: &RuntimeConfigJVM{
				JavaVersion: "21",
			},
		},
	}
	apps := binmanager.MapOfApps{
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.35.1",
				Runtime:     "uv",
				LockFile:    "br:fakeLockFileContent",
			},
		},
		"eslint": {
			Node: &binmanager.AppConfigNode{
				PackageName: "eslint",
				Version:     "9.0.0",
				BinPath:     "node_modules/.bin/eslint",
				Runtime:     "node",
				LockFile:    "br:fakeLockFileContent",
			},
		},
		"openapi-generator": {
			Jvm: &binmanager.AppConfigJVM{
				JarURL:  "https://example.com/openapi-generator.jar",
				JarHash: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
				Version: "7.0.0",
				Runtime: "jvm",
			},
		},
	}

	if _, err := ValidateApps(apps, runtimes); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateRuntimes_JVM_MissingJavaVersion(t *testing.T) {
	runtimes := MapOfRuntimes{
		"jvm": {
			Kind: RuntimeKindJVM,
			Mode: RuntimeModeManaged,
			JVM:  &RuntimeConfigJVM{},
		},
	}

	err := ValidateRuntimes(runtimes)
	if err == nil {
		t.Fatal("ValidateRuntimes() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "jvm.javaVersion is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_UVSystemModeWarning_NoUVConfig(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind:   RuntimeKindUV,
			Mode:   RuntimeModeSystem,
			System: &RuntimeConfigSystem{Command: "/usr/bin/uv"},
		},
	}
	apps := binmanager.MapOfApps{
		"yamllint": {
			Uv: &binmanager.AppConfigUV{
				PackageName: "yamllint",
				Version:     "1.35.1",
				LockFile:    "br:fakeLockFileContent",
			},
		},
	}

	warnings, err := ValidateApps(apps, runtimes)
	if err != nil {
		t.Fatalf("ValidateApps() unexpected error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning for UV system mode without pythonVersion, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "UV system mode without pythonVersion") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about UV system mode, got: %v", warnings)
	}
}

func TestValidateApps_UVSystemModeWarning_EmptyPythonVersion(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind:   RuntimeKindUV,
			Mode:   RuntimeModeSystem,
			System: &RuntimeConfigSystem{Command: "/usr/bin/uv"},
			UV:     &RuntimeConfigUV{PythonVersion: ""},
		},
	}
	apps := binmanager.MapOfApps{}

	warnings, err := ValidateApps(apps, runtimes)
	if err != nil {
		t.Fatalf("ValidateApps() unexpected error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning for UV system mode with empty pythonVersion, got none")
	}
}

func TestValidateApps_UVSystemModeNoWarning_WithPythonVersion(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind:   RuntimeKindUV,
			Mode:   RuntimeModeSystem,
			System: &RuntimeConfigSystem{Command: "/usr/bin/uv"},
			UV:     &RuntimeConfigUV{PythonVersion: "3.12"},
		},
	}
	apps := binmanager.MapOfApps{}

	warnings, err := ValidateApps(apps, runtimes)
	if err != nil {
		t.Fatalf("ValidateApps() unexpected error: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("expected no warnings with pythonVersion set, got: %v", warnings)
	}
}

func TestValidateApps_UVManagedModeNoWarning(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
		},
	}
	apps := binmanager.MapOfApps{}

	warnings, err := ValidateApps(apps, runtimes)
	if err != nil {
		t.Fatalf("ValidateApps() unexpected error: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for managed mode, got: %v", warnings)
	}
}

func TestValidateRuntimes_JVM_InvalidJavaVersion(t *testing.T) {
	tests := []struct {
		name        string
		javaVersion string
	}{
		{"path traversal", "../../evil"},
		{"contains slash", "21/evil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimes := MapOfRuntimes{
				"jvm": {
					Kind: RuntimeKindJVM,
					Mode: RuntimeModeManaged,
					JVM: &RuntimeConfigJVM{
						JavaVersion: tt.javaVersion,
					},
				},
			}

			err := ValidateRuntimes(runtimes)
			if err == nil {
				t.Fatalf("ValidateRuntimes() expected error for javaVersion %q, got nil", tt.javaVersion)
			}
			if !strings.Contains(err.Error(), "jvm.javaVersion") || !strings.Contains(err.Error(), "invalid characters") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

func TestValidateApps_NodeSystemModeNoWarning(t *testing.T) {
	runtimes := MapOfRuntimes{
		"node": {
			Kind:   RuntimeKindNode,
			Mode:   RuntimeModeSystem,
			System: &RuntimeConfigSystem{Command: "/usr/bin/node"},
			Node:   &RuntimeConfigNode{NodeVersion: "22.14.0", PNPMVersion: "10.7.0", PNPMHash: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"},
		},
	}
	apps := binmanager.MapOfApps{}

	warnings, err := ValidateApps(apps, runtimes)
	if err != nil {
		t.Fatalf("ValidateApps() unexpected error: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for Node system mode, got: %v", warnings)
	}
}

// ==============================
// Go runtime/app validation tests
// ==============================

func TestValidateApps_Lockfile_Go_Missing(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind: RuntimeKindGo,
			Mode: RuntimeModeManaged,
			Go:   &RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}
	apps := binmanager.MapOfApps{
		"govulncheck": {
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for missing lockFile, got nil")
	}
	if !strings.Contains(err.Error(), `app "govulncheck": lockFile is required`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_Lockfile_Go_Present(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind: RuntimeKindGo,
			Mode: RuntimeModeManaged,
			Go:   &RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}
	apps := binmanager.MapOfApps{
		"govulncheck": {
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				LockFile:    "br:some-compressed-content",
			},
		},
	}

	if _, err := ValidateApps(apps, runtimes); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateAppsSkipLockfile_AllowsMissingLockfile_Go(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind: RuntimeKindGo,
			Mode: RuntimeModeManaged,
			Go:   &RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}
	apps := binmanager.MapOfApps{
		"govulncheck": {
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
			},
		},
	}

	if _, err := ValidateAppsSkipLockfile(apps, runtimes); err != nil {
		t.Errorf("ValidateAppsSkipLockfile() unexpected error: %v", err)
	}
}

func TestValidateApps_UnknownRuntimeRef_Go(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind: RuntimeKindGo,
			Mode: RuntimeModeManaged,
			Go:   &RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}
	apps := binmanager.MapOfApps{
		"govulncheck": {
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				Runtime:     "nonexistent-go",
				LockFile:    "br:some-compressed-content",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for unknown Go runtime ref, got nil")
	}
	if !strings.Contains(err.Error(), `references unknown runtime "nonexistent-go"`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_WrongKindRuntimeRef_Go(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
		},
	}
	apps := binmanager.MapOfApps{
		"govulncheck": {
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				Runtime:     "uv",
				LockFile:    "br:some-compressed-content",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for wrong-kind Go runtime ref, got nil")
	}
	if !strings.Contains(err.Error(), `runtime "uv" is kind "uv", expected "go"`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_ValidGoRuntimeRef(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind:    RuntimeKindGo,
			Mode:    RuntimeModeManaged,
			Managed: testManagedConfig(),
			Go:      &RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}
	apps := binmanager.MapOfApps{
		"govulncheck": {
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				Runtime:     "go",
				LockFile:    "br:fakeLockFileContent",
			},
		},
	}

	if _, err := ValidateApps(apps, runtimes); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_Go_FilesLinksArchivesRejected(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind: RuntimeKindGo,
			Mode: RuntimeModeManaged,
			Go:   &RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}
	apps := binmanager.MapOfApps{
		"govulncheck": {
			Go: &binmanager.AppConfigGo{
				PackageName: "golang.org/x/vuln/cmd/govulncheck",
				Version:     "v1.1.4",
				LockFile:    "br:fakeLockFileContent",
			},
			Files: map[string]string{
				"config.yaml": "content",
			},
			Links: map[string]string{
				"govulncheck-config": "config.yaml",
			},
		},
	}

	_, err := ValidateApps(apps, runtimes)
	if err == nil {
		t.Fatal("ValidateApps() expected error for Go app with files/links, got nil")
	}
	if !strings.Contains(err.Error(), "files/links/archives are only supported on uv and node apps") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_Go_PackageNameAndVersion(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind: RuntimeKindGo,
			Mode: RuntimeModeManaged,
			Go:   &RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}

	tests := []struct {
		name        string
		packageName string
		version     string
		wantErr     string // empty means expect success
	}{
		{
			name:        "valid",
			packageName: "golang.org/x/vuln/cmd/govulncheck",
			version:     "v1.1.4",
			wantErr:     "",
		},
		{
			name:        "empty packageName",
			packageName: "",
			version:     "v1.1.4",
			wantErr:     "go.packageName is required",
		},
		{
			name:        "packageName is dotdot",
			packageName: "..",
			version:     "v1.1.4",
			wantErr:     `go.packageName ".." must not contain`,
		},
		{
			name:        "packageName escapes parent",
			packageName: "../escape",
			version:     "v1.1.4",
			wantErr:     "must not contain",
		},
		{
			name:        "packageName embeds traversal",
			packageName: "golang.org/x/../escape",
			version:     "v1.1.4",
			wantErr:     "must not contain",
		},
		{
			name:        "packageName invalid chars",
			packageName: "golang.org/x/vuln cmd",
			version:     "v1.1.4",
			wantErr:     "contains invalid characters",
		},
		{
			name:        "empty version",
			packageName: "golang.org/x/vuln/cmd/govulncheck",
			version:     "",
			wantErr:     "go.version is required",
		},
		{
			name:        "version latest",
			packageName: "golang.org/x/vuln/cmd/govulncheck",
			version:     "latest",
			wantErr:     "must be a pinned version",
		},
		{
			name:        "version with space",
			packageName: "golang.org/x/vuln/cmd/govulncheck",
			version:     "v1.1.4 5",
			wantErr:     "go.version",
		},
		{
			name:        "version with at sign",
			packageName: "golang.org/x/vuln/cmd/govulncheck",
			version:     "v1.1.4@",
			wantErr:     "go.version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{
				"govulncheck": {
					Go: &binmanager.AppConfigGo{
						PackageName: tt.packageName,
						Version:     tt.version,
						Runtime:     "go",
						LockFile:    "br:fakeLockFileContent",
					},
				},
			}

			_, err := ValidateApps(apps, runtimes)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateApps() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateApps() expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRuntimes_Go_Valid(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind:    RuntimeKindGo,
			Mode:    RuntimeModeManaged,
			Managed: testManagedConfig(),
			Go: &RuntimeConfigGo{
				GoVersion: "1.22.0",
			},
		},
	}

	if err := ValidateRuntimes(runtimes); err != nil {
		t.Errorf("ValidateRuntimes() unexpected error: %v", err)
	}
}

func TestValidateRuntimes_Go_MissingConfig(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind: RuntimeKindGo,
			Mode: RuntimeModeManaged,
		},
	}

	err := ValidateRuntimes(runtimes)
	if err == nil {
		t.Fatal("ValidateRuntimes() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Go runtime requires go config with goVersion") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateRuntimes_Go_MissingGoVersion(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind: RuntimeKindGo,
			Mode: RuntimeModeManaged,
			Go:   &RuntimeConfigGo{},
		},
	}

	err := ValidateRuntimes(runtimes)
	if err == nil {
		t.Fatal("ValidateRuntimes() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "go.goVersion is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateRuntimes_Go_InvalidGoVersion(t *testing.T) {
	tests := []struct {
		name      string
		goVersion string
	}{
		{"path traversal", "../../evil"},
		{"contains slash", "1.22.0/evil"},
		{"contains backslash", "1.22.0\\evil"},
		{"starts with dot", ".1.22.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimes := MapOfRuntimes{
				"go": {
					Kind: RuntimeKindGo,
					Mode: RuntimeModeManaged,
					Go: &RuntimeConfigGo{
						GoVersion: tt.goVersion,
					},
				},
			}

			err := ValidateRuntimes(runtimes)
			if err == nil {
				t.Fatalf("ValidateRuntimes() expected error for goVersion %q, got nil", tt.goVersion)
			}
			if !strings.Contains(err.Error(), "go.goVersion") || !strings.Contains(err.Error(), "invalid characters") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

func TestValidateRuntimes_Go_ValidVersionFormats(t *testing.T) {
	tests := []struct {
		name      string
		goVersion string
	}{
		{"semver", "1.22.0"},
		{"with pre-release", "1.22.0-rc.1"},
		{"major only", "1"},
		{"go-prefixed", "go1.22.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimes := MapOfRuntimes{
				"go": {
					Kind:    RuntimeKindGo,
					Mode:    RuntimeModeManaged,
					Managed: testManagedConfig(),
					Go: &RuntimeConfigGo{
						GoVersion: tt.goVersion,
					},
				},
			}

			if err := ValidateRuntimes(runtimes); err != nil {
				t.Errorf("ValidateRuntimes() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateRuntimes_Go_SystemModeWithoutGoVersion(t *testing.T) {
	// In system mode goVersion is optional (mirrors UV); a missing config or
	// empty version must not be a validation error.
	tests := []struct {
		name  string
		goCfg *RuntimeConfigGo
	}{
		{"nil go config", nil},
		{"empty goVersion", &RuntimeConfigGo{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimes := MapOfRuntimes{
				"go": {
					Kind:   RuntimeKindGo,
					Mode:   RuntimeModeSystem,
					System: &RuntimeConfigSystem{Command: "/usr/bin/go"},
					Go:     tt.goCfg,
				},
			}

			if err := ValidateRuntimes(runtimes); err != nil {
				t.Errorf("ValidateRuntimes() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateRuntimes_Go_SystemModeInvalidGoVersion(t *testing.T) {
	// When goVersion IS provided in system mode it must still be valid.
	runtimes := MapOfRuntimes{
		"go": {
			Kind:   RuntimeKindGo,
			Mode:   RuntimeModeSystem,
			System: &RuntimeConfigSystem{Command: "/usr/bin/go"},
			Go:     &RuntimeConfigGo{GoVersion: "../../evil"},
		},
	}

	err := ValidateRuntimes(runtimes)
	if err == nil {
		t.Fatal("ValidateRuntimes() expected error for invalid goVersion in system mode, got nil")
	}
	if !strings.Contains(err.Error(), "go.goVersion") || !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateApps_GoSystemModeWarning_NoGoConfig(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind:   RuntimeKindGo,
			Mode:   RuntimeModeSystem,
			System: &RuntimeConfigSystem{Command: "/usr/bin/go"},
		},
	}
	apps := binmanager.MapOfApps{}

	warnings, err := ValidateApps(apps, runtimes)
	if err != nil {
		t.Fatalf("ValidateApps() unexpected error: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Go system mode without goVersion") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about Go system mode without goVersion, got: %v", warnings)
	}
}

func TestValidateApps_GoSystemModeWarning_EmptyGoVersion(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind:   RuntimeKindGo,
			Mode:   RuntimeModeSystem,
			System: &RuntimeConfigSystem{Command: "/usr/bin/go"},
			Go:     &RuntimeConfigGo{GoVersion: ""},
		},
	}
	apps := binmanager.MapOfApps{}

	warnings, err := ValidateApps(apps, runtimes)
	if err != nil {
		t.Fatalf("ValidateApps() unexpected error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning for Go system mode with empty goVersion, got none")
	}
}

func TestValidateApps_GoSystemModeNoWarning_WithGoVersion(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind:   RuntimeKindGo,
			Mode:   RuntimeModeSystem,
			System: &RuntimeConfigSystem{Command: "/usr/bin/go"},
			Go:     &RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}
	apps := binmanager.MapOfApps{}

	warnings, err := ValidateApps(apps, runtimes)
	if err != nil {
		t.Fatalf("ValidateApps() unexpected error: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("expected no warnings with goVersion set, got: %v", warnings)
	}
}

func TestValidateApps_GoManagedModeNoWarning(t *testing.T) {
	runtimes := MapOfRuntimes{
		"go": {
			Kind:    RuntimeKindGo,
			Mode:    RuntimeModeManaged,
			Managed: testManagedConfig(),
			Go:      &RuntimeConfigGo{GoVersion: "1.22.0"},
		},
	}
	apps := binmanager.MapOfApps{}

	warnings, err := ValidateApps(apps, runtimes)
	if err != nil {
		t.Fatalf("ValidateApps() unexpected error: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for Go managed mode, got: %v", warnings)
	}
}

func makeTestInlineArchive(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("module.exports = {};")
	if err := tw.WriteHeader(&tar.Header{Name: "config.js", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	compressed, err := binmanager.CompressArchive(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return compressed
}

func TestValidateApps_Archives_ValidInline(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {Inline: makeTestInlineArchive(t)},
			},
			Links: map[string]string{
				"eslint-config": "dist",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_ValidExternal(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {
					URL:    "https://example.com/dist.tar.gz",
					Hash:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
					Format: binmanager.BinContentTypeTarGz,
				},
			},
			Links: map[string]string{
				"eslint-config": "dist",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_EmptyName(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"": {Inline: makeTestInlineArchive(t)},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for empty archive name")
	}
	if !strings.Contains(err.Error(), "archive name must not be empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_PathTraversal(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"../../evil": {Inline: makeTestInlineArchive(t)},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for path traversal archive name")
	}
	if !strings.Contains(err.Error(), "unsafe path components") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_AbsolutePath(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"/etc/config": {Inline: makeTestInlineArchive(t)},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for absolute archive name")
	}
	if !strings.Contains(err.Error(), "unsafe path components") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_NilSpec(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": nil,
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for nil archive spec")
	}
	if !strings.Contains(err.Error(), `archive "dist" is nil`) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_NeitherInlineNorExternal(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for archive with neither inline nor url")
	}
	if !strings.Contains(err.Error(), "must have either inline or url field set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_BothInlineAndExternal(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {
					Inline: makeTestInlineArchive(t),
					URL:    "https://example.com/dist.tar.gz",
				},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for archive with both inline and url")
	}
	if !strings.Contains(err.Error(), "cannot have both inline and url fields set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_InlineMissingPrefix(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {Inline: "invalid-data"},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for inline archive without tar.br: prefix")
	}
	if !strings.Contains(err.Error(), "must have 'tar.br:' prefix") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_InlineInvalidContent(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {Inline: "tar.br:not-valid-base64!!!"},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for invalid inline archive content")
	}
	if !strings.Contains(err.Error(), "inline content is invalid") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_ExternalMissingHash(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {
					URL:    "https://example.com/dist.tar.gz",
					Format: binmanager.BinContentTypeTarGz,
				},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for external archive without hash")
	}
	if !strings.Contains(err.Error(), "hash is required for external archives") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_ExternalInvalidHash(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {
					URL:    "https://example.com/dist.tar.gz",
					Hash:   "not-a-valid-sha256",
					Format: binmanager.BinContentTypeTarGz,
				},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for invalid hash")
	}
	if !strings.Contains(err.Error(), "hash must be a valid SHA-256 hex string") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_ExternalMissingFormat(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {
					URL:  "https://example.com/dist.tar.gz",
					Hash: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
				},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for external archive without format")
	}
	if !strings.Contains(err.Error(), "format is required for external archives") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_ExternalInvalidFormat(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {
					URL:    "https://example.com/dist.zip",
					Hash:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
					Format: binmanager.BinContentTypeZip,
				},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for non-tar format")
	}
	if !strings.Contains(err.Error(), "format must be one of: tar, tar.gz, tar.xz, tar.bz2, tar.zst") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_BinaryAppRejected(t *testing.T) {
	apps := binmanager.MapOfApps{
		"tool": {
			Binary: &binmanager.AppConfigBinary{},
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {Inline: makeTestInlineArchive(t)},
			},
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("expected error for binary app with archives")
	}
	if !strings.Contains(err.Error(), "files/links/archives are only supported on uv and node apps") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_LinkReferencesArchive(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {Inline: makeTestInlineArchive(t)},
			},
			Links: map[string]string{
				"eslint-config": "dist",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_LinkWithRelativePath(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {Inline: makeTestInlineArchive(t)},
			},
			Links: map[string]string{
				"eslint-config": "dist/eslint.config.js",
			},
		},
	}

	// Link values are now relative paths, not references to Files/Archives keys
	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

func TestValidateApps_Archives_LinkReferencesFileNotArchive(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Files: map[string]string{
				"config.js": "module.exports = {};",
			},
			Archives: map[string]*binmanager.ArchiveSpec{
				"dist": {Inline: makeTestInlineArchive(t)},
			},
			Links: map[string]string{
				"eslint-config": "config.js",
				"eslint-dist":   "dist",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

// ==============================
// ValidateBundles tests
// ==============================

func TestValidateBundles_Valid(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"agent-skills": {
			Version: "1.0",
			Files: map[string]string{
				"agents.md": "# Agent instructions",
			},
			Links: map[string]string{
				"agents-md": "agents.md",
			},
		},
	}

	if err := ValidateBundles(bundles, nil); err != nil {
		t.Errorf("ValidateBundles() unexpected error: %v", err)
	}
}

func TestValidateBundles_ValidWithArchives(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"skills": {
			Version: "2.0",
			Archives: map[string]*binmanager.ArchiveSpec{
				"data": {
					URL:    "https://example.com/data.tar.gz",
					Hash:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
					Format: binmanager.BinContentTypeTarGz,
				},
			},
			Links: map[string]string{
				"skills-dir": ".",
			},
		},
	}

	if err := ValidateBundles(bundles, nil); err != nil {
		t.Errorf("ValidateBundles() unexpected error: %v", err)
	}
}

func TestValidateBundles_EmptyBundleNoFilesNoArchives(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"empty": {
			Version: "1.0",
			Links: map[string]string{
				"some-link": "file.txt",
			},
		},
	}

	err := ValidateBundles(bundles, nil)
	if err == nil {
		t.Fatal("ValidateBundles() expected error for empty bundle, got nil")
	}
	if !strings.Contains(err.Error(), "must have at least files or archives") {
		t.Errorf("expected 'must have at least files or archives' in error, got: %v", err)
	}
}

func TestValidateBundles_LinkNameCollisionWithApp(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Links: map[string]string{
				"shared-link": "config.js",
			},
		},
	}

	bundles := binmanager.MapOfBundles{
		"my-bundle": {
			Version: "1.0",
			Files: map[string]string{
				"data.txt": "content",
			},
			Links: map[string]string{
				"shared-link": "data.txt",
			},
		},
	}

	err := ValidateBundles(bundles, apps)
	if err == nil {
		t.Fatal("ValidateBundles() expected error for link name collision, got nil")
	}
	if !strings.Contains(err.Error(), "shared-link") {
		t.Errorf("expected 'shared-link' in error, got: %v", err)
	}
}

func TestValidateBundles_LinkNameCollisionBetweenBundles(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"bundle-a": {
			Version: "1.0",
			Files: map[string]string{
				"a.txt": "content a",
			},
			Links: map[string]string{
				"shared-link": "a.txt",
			},
		},
		"bundle-b": {
			Version: "1.0",
			Files: map[string]string{
				"b.txt": "content b",
			},
			Links: map[string]string{
				"shared-link": "b.txt",
			},
		},
	}

	err := ValidateBundles(bundles, nil)
	if err == nil {
		t.Fatal("ValidateBundles() expected error for link name collision between bundles, got nil")
	}
	if !strings.Contains(err.Error(), "shared-link") {
		t.Errorf("expected 'shared-link' in error, got: %v", err)
	}
}

func TestValidateBundles_LinkPathTraversal(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"bad-bundle": {
			Version: "1.0",
			Files: map[string]string{
				"data.txt": "content",
			},
			Links: map[string]string{
				"bad-link": "../../../etc/passwd",
			},
		},
	}

	err := ValidateBundles(bundles, nil)
	if err == nil {
		t.Fatal("ValidateBundles() expected error for link path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "escapes parent directory") {
		t.Errorf("expected 'escapes parent directory' in error, got: %v", err)
	}
}

func TestValidateBundles_EmptyLinkPath(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"bad-bundle": {
			Version: "1.0",
			Files: map[string]string{
				"data.txt": "content",
			},
			Links: map[string]string{
				"bad-link": "",
			},
		},
	}

	err := ValidateBundles(bundles, nil)
	if err == nil {
		t.Fatal("ValidateBundles() expected error for empty link path, got nil")
	}
}

func TestValidateBundles_NilBundles(t *testing.T) {
	if err := ValidateBundles(nil, nil); err != nil {
		t.Errorf("ValidateBundles() unexpected error for nil bundles: %v", err)
	}
}

func TestValidateBundles_EmptyMap(t *testing.T) {
	if err := ValidateBundles(binmanager.MapOfBundles{}, nil); err != nil {
		t.Errorf("ValidateBundles() unexpected error for empty map: %v", err)
	}
}

func TestValidateBundles_ExistingAppValidationUnaffected(t *testing.T) {
	apps := binmanager.MapOfApps{
		"eslint": {
			Required: true,
			Files: map[string]string{
				"eslint-base.js": "module.exports = {};",
			},
			Links: map[string]string{
				"eslint-base.js": "eslint-base.js",
			},
		},
	}

	if _, err := ValidateApps(apps, nil); err != nil {
		t.Errorf("ValidateApps() should still work independently: %v", err)
	}
}

func TestValidateBundles_DotLinkPath(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"full-dir-bundle": {
			Version: "1.0",
			Files: map[string]string{
				"readme.md": "# Hello",
			},
			Links: map[string]string{
				"full-dir": ".",
			},
		},
	}

	if err := ValidateBundles(bundles, nil); err != nil {
		t.Errorf("ValidateBundles() unexpected error for '.' link path: %v", err)
	}
}

func TestValidateBundles_AbsoluteLinkName(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"bad-bundle": {
			Version: "1.0",
			Files: map[string]string{
				"data.txt": "content",
			},
			Links: map[string]string{
				"/etc/evil": "data.txt",
			},
		},
	}

	err := ValidateBundles(bundles, nil)
	if err == nil {
		t.Fatal("ValidateBundles() expected error for absolute link name, got nil")
	}
	if !strings.Contains(err.Error(), "unsafe path components") {
		t.Errorf("expected 'unsafe path components' in error, got: %v", err)
	}
}

func TestValidateBundles_ReservedGitignoreLinkName(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"bad-bundle": {
			Version: "1.0",
			Files: map[string]string{
				"data.txt": "content",
			},
			Links: map[string]string{
				".gitignore": "data.txt",
			},
		},
	}

	err := ValidateBundles(bundles, nil)
	if err == nil {
		t.Fatal("ValidateBundles() expected error for reserved .gitignore link name, got nil")
	}
	if !strings.Contains(err.Error(), "reserved for internal use") {
		t.Errorf("expected 'reserved for internal use' in error, got: %v", err)
	}
}

func TestValidateBundles_ReservedTypeDefinitionsLinkName(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"bad-bundle": {
			Version: "1.0",
			Files: map[string]string{
				"data.txt": "content",
			},
			Links: map[string]string{
				"datamitsu.config.d.ts": "data.txt",
			},
		},
	}

	err := ValidateBundles(bundles, nil)
	if err == nil {
		t.Fatal("ValidateBundles() expected error for reserved datamitsu.config.d.ts link name, got nil")
	}
	if !strings.Contains(err.Error(), "reserved for internal use") {
		t.Errorf("expected 'reserved for internal use' in error, got: %v", err)
	}
}

func TestValidateInit_ValidScopes(t *testing.T) {
	tests := []struct {
		name  string
		scope string
	}{
		{"empty scope", ""},
		{"project scope", ScopeProject},
		{"git-root scope", ScopeGitRoot},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initConfigs := MapOfConfigSetup{
				"test.yml": {Scope: tt.scope},
			}
			if err := ValidateSetup(initConfigs); err != nil {
				t.Errorf("ValidateSetup() unexpected error for scope %q: %v", tt.scope, err)
			}
		})
	}
}

func TestValidateInit_UnknownScope(t *testing.T) {
	initConfigs := MapOfConfigSetup{
		"test.yml": {Scope: "invalid-scope"},
	}
	err := ValidateSetup(initConfigs)
	if err == nil {
		t.Fatal("ValidateSetup() expected error for unknown scope, got nil")
	}
	if !strings.Contains(err.Error(), "scope must be") {
		t.Errorf("expected 'scope must be' in error, got: %v", err)
	}
}

func TestValidateInitToolRefs(t *testing.T) {
	tools := MapOfTools{
		"golangci-lint": {Name: "golangci-lint"},
		"prettier":      {Name: "prettier"},
	}

	t.Run("known tool refs produce no warnings", func(t *testing.T) {
		initConfigs := MapOfConfigSetup{
			".golangci.yml": {Tools: []string{"golangci-lint"}},
			".prettierrc":   {Tools: []string{"prettier"}},
			".gitignore":    {}, // no tools is fine
		}
		if w := ValidateSetupToolRefs(initConfigs, tools); len(w) != 0 {
			t.Errorf("expected no warnings, got %v", w)
		}
	})

	t.Run("unknown tool ref produces a warning", func(t *testing.T) {
		initConfigs := MapOfConfigSetup{
			".golangci.yml": {Tools: []string{"golangci"}}, // typo
		}
		w := ValidateSetupToolRefs(initConfigs, tools)
		if len(w) != 1 {
			t.Fatalf("expected 1 warning, got %d: %v", len(w), w)
		}
		if !strings.Contains(w[0], "golangci") || !strings.Contains(w[0], ".golangci.yml") {
			t.Errorf("warning should name the file and unknown tool, got: %q", w[0])
		}
	})

	t.Run("multiple unknown refs are reported deterministically", func(t *testing.T) {
		initConfigs := MapOfConfigSetup{
			"b.yml": {Tools: []string{"nope"}},
			"a.yml": {Tools: []string{"bogus"}},
		}
		w := ValidateSetupToolRefs(initConfigs, tools)
		if len(w) != 2 {
			t.Fatalf("expected 2 warnings, got %d: %v", len(w), w)
		}
		// Sorted by config name: a.yml before b.yml.
		if !strings.Contains(w[0], "a.yml") || !strings.Contains(w[1], "b.yml") {
			t.Errorf("warnings should be sorted by config name, got: %v", w)
		}
	})
}

func TestValidateTools(t *testing.T) {
	t.Run("supported placeholders pass", func(t *testing.T) {
		tools := MapOfTools{
			"golangci-lint": {
				Name: "golangci-lint",
				Operations: map[OperationType]ToolOperation{
					OpFix: {
						Args: []string{"run", "--fix", "{cwd}/x", "{files}", "{file}", "{root}"},
						Env:  map[string]string{"GOLANGCI_LINT_CACHE": "{toolCache}"},
					},
				},
			},
		}
		if err := ValidateTools(tools, nil); err != nil {
			t.Errorf("ValidateTools() = %v, want nil", err)
		}
	})

	t.Run("unsupported placeholder in args fails", func(t *testing.T) {
		tools := MapOfTools{
			"t": {Operations: map[OperationType]ToolOperation{
				OpLint: {Args: []string{"--cache={toolcache}"}}, // wrong case
			}},
		}
		err := ValidateTools(tools, nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "{toolcache}") || !strings.Contains(err.Error(), "args") {
			t.Errorf("error should name the bad placeholder and 'args', got: %v", err)
		}
	})

	t.Run("unsupported placeholder in env fails", func(t *testing.T) {
		tools := MapOfTools{
			"t": {Operations: map[OperationType]ToolOperation{
				OpFix: {Env: map[string]string{"CACHE": "{cache}"}},
			}},
		}
		err := ValidateTools(tools, nil)
		if err == nil || !strings.Contains(err.Error(), "{cache}") {
			t.Errorf("expected error naming {cache}, got: %v", err)
		}
	})

	t.Run("args-only placeholder rejected in env", func(t *testing.T) {
		tools := MapOfTools{
			"t": {Operations: map[OperationType]ToolOperation{
				OpFix: {Env: map[string]string{"F": "{file}"}},
			}},
		}
		err := ValidateTools(tools, nil)
		if err == nil || !strings.Contains(err.Error(), "{file}") {
			t.Errorf("expected {file} rejected in env, got: %v", err)
		}
	})

	t.Run("shell globs and go templates are not flagged", func(t *testing.T) {
		tools := MapOfTools{
			"t": {Operations: map[OperationType]ToolOperation{
				OpLint: {
					Args: []string{"**/*.{js,ts}", "--format={{.Path}}", "{{Field}}"},
					Env:  map[string]string{"GLOB": "a.{go,mod}"},
				},
			}},
		}
		if err := ValidateTools(tools, nil); err != nil {
			t.Errorf("globs/templates should not be flagged, got: %v", err)
		}
	})

	t.Run("valid input/output modes pass", func(t *testing.T) {
		tools := MapOfTools{
			"fmt": {Operations: map[OperationType]ToolOperation{
				OpFix: {Input: ToolInputStdin, Output: ToolOutputStdout},
			}},
			"lint": {Operations: map[OperationType]ToolOperation{
				OpLint: {Input: ToolInputFile, Output: ToolOutputInplace},
			}},
		}
		if err := ValidateTools(tools, nil); err != nil {
			t.Errorf("valid input/output modes should pass, got: %v", err)
		}
	})

	t.Run("invalid input mode fails", func(t *testing.T) {
		tools := MapOfTools{
			"t": {Operations: map[OperationType]ToolOperation{
				OpFix: {Input: ToolInputMode("pipe")},
			}},
		}
		err := ValidateTools(tools, nil)
		if err == nil || !strings.Contains(err.Error(), "invalid input mode") {
			t.Errorf("expected invalid input mode error, got: %v", err)
		}
	})

	t.Run("invalid output mode fails", func(t *testing.T) {
		tools := MapOfTools{
			"t": {Operations: map[OperationType]ToolOperation{
				OpLint: {Output: ToolOutputMode("file")},
			}},
		}
		err := ValidateTools(tools, nil)
		if err == nil || !strings.Contains(err.Error(), "invalid output mode") {
			t.Errorf("expected invalid output mode error, got: %v", err)
		}
	})

	t.Run("valid outputParser reference passes", func(t *testing.T) {
		tools := MapOfTools{
			"hadolint": {
				Name:         "hadolint",
				OutputParser: "hadolint",
				Operations: map[OperationType]ToolOperation{
					OpLint: {Args: []string{"{file}"}},
				},
			},
		}
		parsers := MapOfParsers{
			"hadolint": {URL: "https://example.com/h.wasm", Hash: strings.Repeat("a", 64)},
		}
		if err := ValidateTools(tools, parsers); err != nil {
			t.Errorf("ValidateTools() = %v, want nil", err)
		}
	})

	t.Run("dangling outputParser reference fails", func(t *testing.T) {
		tools := MapOfTools{
			"hadolint": {
				Name:         "hadolint",
				OutputParser: "missing",
				Operations: map[OperationType]ToolOperation{
					OpLint: {Args: []string{"{file}"}},
				},
			},
		}
		err := ValidateTools(tools, nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "hadolint") || !strings.Contains(err.Error(), "missing") {
			t.Errorf("error should name the tool and the missing parser, got: %v", err)
		}
	})

	t.Run("empty outputParser is ignored", func(t *testing.T) {
		tools := MapOfTools{
			"t": {Operations: map[OperationType]ToolOperation{
				OpFix: {Args: []string{"{file}"}},
			}},
		}
		if err := ValidateTools(tools, nil); err != nil {
			t.Errorf("ValidateTools() = %v, want nil", err)
		}
	})
}
