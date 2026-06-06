package target

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// detectViaLdd attempts to detect libc by parsing "ldd --version" output.
// glibc prints version info to stdout; musl prints to stderr.
func detectViaLdd(ctx context.Context) LibcType {
	if runtime.GOOS != "linux" {
		return LibcUnknown
	}
	return detectViaLddOutput(runLddVersion(ctx))
}

func runLddVersion(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "ldd", "--version")
	stdout, err := cmd.Output()
	if err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			return string(exitErr.Stderr)
		}
		if len(stdout) > 0 {
			return string(stdout)
		}
		return ""
	}
	return string(stdout)
}

func detectViaLddOutput(output string) LibcType {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "musl") {
		return LibcMusl
	}
	if strings.Contains(lower, "glibc") || strings.Contains(lower, "gnu libc") || strings.Contains(lower, "gnu c library") {
		return LibcGlibc
	}
	return LibcUnknown
}

// detectViaELF reads the ELF interpreter (PT_INTERP) from the current binary.
func detectViaELF() LibcType {
	if runtime.GOOS != "linux" {
		return LibcUnknown
	}
	exePath, err := os.Executable()
	if err != nil {
		return LibcUnknown
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return LibcUnknown
	}
	return detectViaELFInterpreter(exePath)
}

func detectViaELFInterpreter(path string) LibcType {
	f, err := elf.Open(path)
	if err != nil {
		return LibcUnknown
	}
	defer func() { _ = f.Close() }()

	for _, prog := range f.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}
		data := make([]byte, prog.Filesz)
		if _, err := prog.ReadAt(data, 0); err != nil {
			return LibcUnknown
		}
		interp := strings.TrimRight(string(data), "\x00")
		return classifyInterpreter(interp)
	}
	return LibcUnknown
}

func classifyInterpreter(interp string) LibcType {
	lower := strings.ToLower(interp)
	if strings.Contains(lower, "ld-musl") {
		return LibcMusl
	}
	if strings.Contains(lower, "ld-linux") {
		return LibcGlibc
	}
	return LibcUnknown
}

// detectViaLoaderPaths checks for the existence of known loader files.
func detectViaLoaderPaths() LibcType {
	if runtime.GOOS != "linux" {
		return LibcUnknown
	}
	return detectViaLoaderGlob(defaultLoaderGlob)
}

func defaultLoaderGlob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob loader pattern %q: %w", pattern, err)
	}
	return matches, nil
}

func detectViaLoaderGlob(globFn func(string) ([]string, error)) LibcType {
	muslPatterns := []string{
		"/lib/ld-musl-*.so*",
		"/lib64/ld-musl-*.so*",
	}
	for _, p := range muslPatterns {
		if matches, err := globFn(p); err == nil && len(matches) > 0 {
			return LibcMusl
		}
	}

	glibcPatterns := []string{
		"/lib/ld-linux-*.so*",
		"/lib64/ld-linux-*.so*",
		"/lib/x86_64-linux-gnu/ld-linux-*.so*",
		"/lib/aarch64-linux-gnu/ld-linux-*.so*",
	}
	for _, p := range glibcPatterns {
		if matches, err := globFn(p); err == nil && len(matches) > 0 {
			return LibcGlibc
		}
	}

	return LibcUnknown
}
