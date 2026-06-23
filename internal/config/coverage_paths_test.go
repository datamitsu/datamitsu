package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
)

// validHex is a syntactically valid lowercase 64-char SHA-256 hex string.
const validHex = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

// managedBinaries builds a one-entry MapOfBinaries for linux/amd64 with the
// given libc key and binary info, exercising the managed-mode validation loop.
func managedBinaries(libc string, info binmanager.BinaryOsArchInfo) binmanager.MapOfBinaries {
	return binmanager.MapOfBinaries{
		syslist.OsTypeLinux: {
			syslist.ArchTypeAmd64: {
				libc: info,
			},
		},
	}
}

func TestValidateRuntimes_ManagedBinaries_Valid(t *testing.T) {
	bp := "node-v22/bin/node"
	runtimes := MapOfRuntimes{
		"uv": {
			Kind: RuntimeKindUV,
			Mode: RuntimeModeManaged,
			Managed: &RuntimeConfigManaged{
				Binaries: managedBinaries(string(target.LibcGlibc), binmanager.BinaryOsArchInfo{
					URL:        "https://example.com/uv.tar.gz",
					Hash:       validHex,
					BinaryPath: &bp,
				}),
			},
		},
	}
	if err := ValidateRuntimes(runtimes); err != nil {
		t.Errorf("ValidateRuntimes() unexpected error: %v", err)
	}
}

func TestValidateRuntimes_ManagedBinaries_Errors(t *testing.T) {
	badHashType := binmanager.BinHashTypeSHA512
	unsafePath := "../escape"

	tests := []struct {
		name    string
		libc    string
		info    binmanager.BinaryOsArchInfo
		wantSub string
	}{
		{
			name:    "invalid libc key",
			libc:    "bogus",
			info:    binmanager.BinaryOsArchInfo{URL: "https://x/u.tgz", Hash: validHex},
			wantSub: "libc key",
		},
		{
			name:    "missing url",
			libc:    string(target.LibcMusl),
			info:    binmanager.BinaryOsArchInfo{Hash: validHex},
			wantSub: "url is required",
		},
		{
			name:    "missing hash",
			libc:    string(target.LibcUnknown),
			info:    binmanager.BinaryOsArchInfo{URL: "https://x/u.tgz"},
			wantSub: "hash is required",
		},
		{
			name:    "invalid hash",
			libc:    string(target.LibcGlibc),
			info:    binmanager.BinaryOsArchInfo{URL: "https://x/u.tgz", Hash: "zzzz"},
			wantSub: "valid SHA-256",
		},
		{
			name:    "disallowed hash type",
			libc:    string(target.LibcGlibc),
			info:    binmanager.BinaryOsArchInfo{URL: "https://x/u.tgz", Hash: validHex, HashType: &badHashType},
			wantSub: "hash type",
		},
		{
			name:    "unsafe binary path",
			libc:    string(target.LibcGlibc),
			info:    binmanager.BinaryOsArchInfo{URL: "https://x/u.tgz", Hash: validHex, BinaryPath: &unsafePath},
			wantSub: "escapes parent directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimes := MapOfRuntimes{
				"uv": {
					Kind:    RuntimeKindUV,
					Mode:    RuntimeModeManaged,
					Managed: &RuntimeConfigManaged{Binaries: managedBinaries(tt.libc, tt.info)},
				},
			}
			err := ValidateRuntimes(runtimes)
			if err == nil {
				t.Fatalf("ValidateRuntimes() expected error containing %q, got nil", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("ValidateRuntimes() error = %v, want substring %q", err, tt.wantSub)
			}
		})
	}
}

func TestValidateRuntimes_SystemMode_MissingConfig(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {Kind: RuntimeKindUV, Mode: RuntimeModeSystem},
	}
	err := ValidateRuntimes(runtimes)
	if err == nil || !strings.Contains(err.Error(), "system mode requires system config") {
		t.Errorf("ValidateRuntimes() error = %v, want system-config error", err)
	}
}

func TestValidateRuntimes_ManagedMode_NilManaged(t *testing.T) {
	runtimes := MapOfRuntimes{
		"uv": {Kind: RuntimeKindUV, Mode: RuntimeModeManaged},
	}
	err := ValidateRuntimes(runtimes)
	if err == nil || !strings.Contains(err.Error(), "managed mode requires managed config") {
		t.Errorf("ValidateRuntimes() error = %v, want managed-config error", err)
	}
}
