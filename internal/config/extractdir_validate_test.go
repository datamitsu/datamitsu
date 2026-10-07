package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

// An extractDir entry runs binaryPath inside the extracted directory, so an entry without
// one, or one whose content is not a file tree, has nothing to run.
func TestValidateApps_ExtractDirRequiresBinaryPathAndArchive(t *testing.T) {
	entry := func(contentType binmanager.BinContentType, binaryPath *string) binmanager.MapOfApps {
		return binmanager.MapOfApps{
			"protoc": {
				Binary: &binmanager.AppConfigBinary{Binaries: binmanager.MapOfBinaries{
					syslist.OsTypeLinux: {syslist.ArchTypeAmd64: {"glibc": binmanager.BinaryOsArchInfo{
						URL:         "https://example.com/protoc-36.2-linux-x86_64.zip",
						Hash:        strings.Repeat("a", 64),
						ContentType: contentType,
						BinaryPath:  binaryPath,
						ExtractDir:  true,
					}}},
				}},
			},
		}
	}

	tests := []struct {
		name    string
		apps    binmanager.MapOfApps
		wantErr string
	}{
		{"zip with binaryPath", entry(binmanager.BinContentTypeZip, new("bin/protoc")), ""},
		{"tar.gz with binaryPath", entry(binmanager.BinContentTypeTarGz, new("protoc/bin/protoc")), ""},
		{"no binaryPath", entry(binmanager.BinContentTypeZip, nil), "extractDir requires binaryPath"},
		{"binaryPath is the directory", entry(binmanager.BinContentTypeZip, new(".")), "names the extracted directory itself"},
		{"binaryPath cleans to the directory", entry(binmanager.BinContentTypeZip, new("bin/..")), "names the extracted directory itself"},
		{"single binary", entry(binmanager.BinContentTypeBinary, new("protoc")), "extractDir requires an archive contentType"},
		{"gz", entry(binmanager.BinContentTypeGz, new("protoc")), "extractDir requires an archive contentType"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateApps(tt.apps, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateApps() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateApps() = %v, want error containing %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), `app "protoc" (linux/amd64/glibc)`) {
				t.Errorf("error does not name the app and platform: %v", err)
			}
		})
	}
}
