package target

import (
	"os"
	"runtime"
	"testing"
)

func TestDetectViaLddOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   LibcType
	}{
		{
			name:   "glibc ldd output",
			output: "ldd (GNU libc) 2.35\nCopyright (C) 2022 Free Software Foundation, Inc.",
			want:   LibcGlibc,
		},
		{
			name:   "glibc ubuntu style",
			output: "ldd (Ubuntu GLIBC 2.35-0ubuntu3.1) 2.35",
			want:   LibcGlibc,
		},
		{
			name:   "glibc gnu c library",
			output: "ldd (GNU C Library) 2.31",
			want:   LibcGlibc,
		},
		{
			name:   "musl ldd output",
			output: "musl libc (x86_64)\nVersion 1.2.4",
			want:   LibcMusl,
		},
		{
			name:   "musl stderr style",
			output: "musl libc\nVersion 1.2.3\nDynamic Program Loader",
			want:   LibcMusl,
		},
		{
			name:   "empty output",
			output: "",
			want:   LibcUnknown,
		},
		{
			name:   "unrecognized output",
			output: "some other libc implementation v1.0",
			want:   LibcUnknown,
		},
		{
			name:   "case insensitive musl",
			output: "MUSL libc (aarch64)",
			want:   LibcMusl,
		},
		{
			name:   "case insensitive glibc",
			output: "LDD (GLIBC 2.35) 2.35",
			want:   LibcGlibc,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectViaLddOutput(tt.output)
			if got != tt.want {
				t.Errorf("detectViaLddOutput(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

func TestClassifyInterpreter(t *testing.T) {
	tests := []struct {
		name   string
		interp string
		want   LibcType
	}{
		{
			name:   "musl amd64",
			interp: "/lib/ld-musl-x86_64.so.1",
			want:   LibcMusl,
		},
		{
			name:   "musl arm64",
			interp: "/lib/ld-musl-aarch64.so.1",
			want:   LibcMusl,
		},
		{
			name:   "glibc amd64",
			interp: "/lib64/ld-linux-x86-64.so.2",
			want:   LibcGlibc,
		},
		{
			name:   "glibc arm64",
			interp: "/lib/ld-linux-aarch64.so.1",
			want:   LibcGlibc,
		},
		{
			name:   "empty",
			interp: "",
			want:   LibcUnknown,
		},
		{
			name:   "unknown interpreter",
			interp: "/lib/ld-other.so.1",
			want:   LibcUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyInterpreter(tt.interp)
			if got != tt.want {
				t.Errorf("classifyInterpreter(%q) = %q, want %q", tt.interp, got, tt.want)
			}
		})
	}
}

func TestDetectViaELFInterpreter(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF interpreter test only runs on Linux")
	}
	exePath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() failed: %v", err)
	}
	result := detectViaELFInterpreter(exePath)
	if result != LibcGlibc && result != LibcMusl {
		t.Logf("detectViaELFInterpreter returned %q (may be statically linked)", result)
	}
}

func TestDetectViaELFInterpreterInvalidPath(t *testing.T) {
	result := detectViaELFInterpreter("/nonexistent/binary")
	if result != LibcUnknown {
		t.Errorf("detectViaELFInterpreter(nonexistent) = %q, want %q", result, LibcUnknown)
	}
}

func TestDetectViaELFInterpreterNonELF(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "not-elf-*")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	if _, err := tmpFile.WriteString("not an ELF file"); err != nil {
		t.Fatalf("WriteString failed: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	result := detectViaELFInterpreter(tmpFile.Name())
	if result != LibcUnknown {
		t.Errorf("detectViaELFInterpreter(non-ELF) = %q, want %q", result, LibcUnknown)
	}
}

func TestDetectViaLoaderGlob(t *testing.T) {
	tests := []struct {
		name    string
		globFn  func(string) ([]string, error)
		want    LibcType
	}{
		{
			name: "musl loader found",
			globFn: func(pattern string) ([]string, error) {
				if pattern == "/lib/ld-musl-*.so*" {
					return []string{"/lib/ld-musl-x86_64.so.1"}, nil
				}
				return nil, nil
			},
			want: LibcMusl,
		},
		{
			name: "glibc loader found",
			globFn: func(pattern string) ([]string, error) {
				return nil, nil
			},
			want: LibcUnknown,
		},
		{
			name: "glibc via lib64",
			globFn: func(pattern string) ([]string, error) {
				if pattern == "/lib64/ld-linux-*.so*" {
					return []string{"/lib64/ld-linux-x86-64.so.2"}, nil
				}
				return nil, nil
			},
			want: LibcGlibc,
		},
		{
			name: "musl in lib64",
			globFn: func(pattern string) ([]string, error) {
				if pattern == "/lib64/ld-musl-*.so*" {
					return []string{"/lib64/ld-musl-x86_64.so.1"}, nil
				}
				return nil, nil
			},
			want: LibcMusl,
		},
		{
			name: "no loaders found",
			globFn: func(pattern string) ([]string, error) {
				return nil, nil
			},
			want: LibcUnknown,
		},
		{
			name: "glob error returns unknown",
			globFn: func(pattern string) ([]string, error) {
				return nil, os.ErrPermission
			},
			want: LibcUnknown,
		},
		{
			name: "musl takes priority over glibc",
			globFn: func(pattern string) ([]string, error) {
				if pattern == "/lib/ld-musl-*.so*" {
					return []string{"/lib/ld-musl-x86_64.so.1"}, nil
				}
				if pattern == "/lib/ld-linux-*.so*" {
					return []string{"/lib/ld-linux-x86-64.so.2"}, nil
				}
				return nil, nil
			},
			want: LibcMusl,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectViaLoaderGlob(tt.globFn)
			if got != tt.want {
				t.Errorf("detectViaLoaderGlob() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectLibcNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("this test only runs on non-Linux platforms")
	}
	result := DetectLibc()
	if result != LibcUnknown {
		t.Errorf("DetectLibc() on non-Linux = %q, want %q", result, LibcUnknown)
	}
}

func TestDetectLibcOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("this test only runs on Linux")
	}
	result := DetectLibc()
	if result != LibcGlibc && result != LibcMusl && result != LibcUnknown {
		t.Errorf("DetectLibc() = %q, want one of glibc/musl/unknown", result)
	}
}

func TestDetectViaLddNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("only runs on non-Linux")
	}
	if result := detectViaLdd(); result != LibcUnknown {
		t.Errorf("detectViaLdd() on non-Linux = %q, want unknown", result)
	}
}

func TestDetectViaELFNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("only runs on non-Linux")
	}
	if result := detectViaELF(); result != LibcUnknown {
		t.Errorf("detectViaELF() on non-Linux = %q, want unknown", result)
	}
}

func TestDetectViaLoaderPathsNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("only runs on non-Linux")
	}
	if result := detectViaLoaderPaths(); result != LibcUnknown {
		t.Errorf("detectViaLoaderPaths() on non-Linux = %q, want unknown", result)
	}
}
