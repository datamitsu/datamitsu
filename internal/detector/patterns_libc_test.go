package detector

import "testing"

func TestMatchLibc(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		libcType string
		expected bool
	}{
		{"musl in filename", "tool-linux-amd64-musl.tar.gz", "musl", true},
		{"alpine in filename", "tool-alpine-amd64.tar.gz", "musl", true},
		{"static in filename not musl", "tool-linux-amd64-static.tar.gz", "musl", false},
		{"gnu in filename", "tool-linux-gnu-amd64.tar.gz", "glibc", true},
		{"glibc in filename", "tool-linux-glibc-amd64.tar.gz", "glibc", true},
		{"no libc indicator", "tool-linux-amd64.tar.gz", "musl", false},
		{"no libc indicator for glibc", "tool-linux-amd64.tar.gz", "glibc", false},
		{"musl case insensitive", "tool-Linux-MUSL-amd64.tar.gz", "musl", true},
		{"gnu case insensitive", "tool-linux-GNU-amd64.tar.gz", "glibc", true},
		{"unknown libc type", "tool-linux-amd64.tar.gz", "unknown", false},
		{"musl not matching glibc", "tool-linux-musl-amd64.tar.gz", "glibc", false},
		{"glibc not matching musl", "tool-linux-gnu-amd64.tar.gz", "musl", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchLibc(tt.filename, tt.libcType)
			if result != tt.expected {
				t.Errorf("MatchLibc(%q, %q) = %v, want %v", tt.filename, tt.libcType, result, tt.expected)
			}
		})
	}
}

func TestDetectLibcFromFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected string
	}{
		{"musl keyword", "tool-linux-musl-amd64.tar.gz", "musl"},
		{"alpine keyword", "tool-alpine-amd64.tar.gz", "musl"},
		{"static keyword not musl", "tool-linux-amd64-static", ""},
		{"gnu keyword", "tool-linux-gnu-amd64.tar.gz", "glibc"},
		{"glibc keyword", "tool-linux-glibc-amd64.tar.gz", "glibc"},
		{"no libc indicator", "tool-linux-amd64.tar.gz", ""},
		{"darwin no libc", "tool-darwin-arm64.tar.gz", ""},
		{"windows no libc", "tool-windows-amd64.exe", ""},
		{"case insensitive musl", "tool-MUSL-linux-amd64.tar.gz", "musl"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectLibcFromFilename(tt.filename)
			if result != tt.expected {
				t.Errorf("DetectLibcFromFilename(%q) = %q, want %q", tt.filename, result, tt.expected)
			}
		})
	}
}
