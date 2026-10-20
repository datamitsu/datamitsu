package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

// An extractDir entry with a binaryPath is verified by what runs: the file at that path inside
// the extracted tree, which must be an executable. A tree that merely unpacks is not enough.
func TestVerifyBinaryOrDir_ExtractDirBinaryPath(t *testing.T) {
	ctx := context.Background()
	archive := vcTarGz(t, map[string]string{
		"protoc/bin/protoc":                      "#!/bin/sh\necho protoc\n",
		"protoc/include/google/protobuf/x.proto": "syntax = \"proto3\";\n",
		"protoc/readme.txt":                      "readme",
	})
	srv := vcServe(t, archive)
	entry := func(binaryPath string) binmanager.BinaryOsArchInfo {
		return binmanager.BinaryOsArchInfo{
			URL:         srv.URL,
			Hash:        vcSHA256Hex(archive),
			ContentType: binmanager.BinContentTypeTarGz,
			BinaryPath:  &binaryPath,
			ExtractDir:  true,
		}
	}

	tests := []struct {
		name       string
		binaryPath string
		wantErr    string
	}{
		{"executable at binaryPath", "protoc/bin/protoc", ""},
		{"binaryPath missing from the tree", "bin/protoc", `binaryPath "bin/protoc": extracted file not found`},
		{"binaryPath names a directory", "protoc/bin", "is a directory"},
		{"binaryPath is not an executable", "protoc/readme.txt", "not an executable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyBinaryOrDir(ctx, entry(tt.binaryPath))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("verifyBinaryOrDir() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("verifyBinaryOrDir() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
