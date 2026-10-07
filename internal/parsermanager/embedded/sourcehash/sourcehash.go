// Package sourcehash fingerprints the sources of the embedded fallback parser
// module, so a Go test can tell a module that no longer matches its sources
// without a Rust toolchain.
//
// The fingerprint is XXH3-128: it is compared only with the value this package
// wrote into the repository, never with anything from outside (Hashing Policy).
package sourcehash

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// ListFile is the list of the module's sources, relative to the repository
// root.
const ListFile = "parsers/datamitsu-parsers/embedded-sources.txt"

// HashFile is where the fingerprint of those sources is committed, relative to
// the repository root.
const HashFile = "internal/parsermanager/embedded/fallback.wasm.sources"

// Sources reads the list of sources under root: one repository-relative path
// per line, `#` comments and blank lines skipped.
func Sources(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ListFile)))
	if err != nil {
		return nil, fmt.Errorf("read the source list: %w", err)
	}
	var paths []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		paths = append(paths, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the source list: %w", err)
	}
	return paths, nil
}

// Hash fingerprints the files at paths under root. Each contributes, in path
// order, its repository-relative path, a NUL, its length, a NUL, its bytes and
// a NUL, with every CRLF read as LF so a checkout that converts line endings
// fingerprints the same.
func Hash(root string, paths []string) (string, error) {
	sorted := slices.Sorted(slices.Values(paths))
	var buf bytes.Buffer
	for _, path := range sorted {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return "", fmt.Errorf("read a source: %w", err)
		}
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		buf.WriteString(path)
		buf.WriteByte(0)
		buf.WriteString(strconv.Itoa(len(data)))
		buf.WriteByte(0)
		buf.Write(data)
		buf.WriteByte(0)
	}
	return hashutil.XXH3Hex(buf.Bytes()), nil
}

// Current is the fingerprint of the listed sources as they are under root.
func Current(root string) (string, error) {
	paths, err := Sources(root)
	if err != nil {
		return "", err
	}
	return Hash(root, paths)
}

// Committed is the fingerprint committed beside the module.
func Committed(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(HashFile)))
	if err != nil {
		return "", fmt.Errorf("read the committed source hash: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Root is the repository root: the nearest directory at or above dir holding
// the source list.
func Root(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(ListFile))); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s above the working directory", ListFile)
		}
		dir = parent
	}
}
