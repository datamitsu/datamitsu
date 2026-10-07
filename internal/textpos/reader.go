package textpos

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// maxFileBytes bounds what a Reader loads. A finding in a larger file is
// anchored by its row instead of its line.
const maxFileBytes = 64 << 20

// ErrNoLine is what LineAt returns for a row the file does not have.
var ErrNoLine = errors.New("no such line")

var bom = []byte{0xEF, 0xBB, 0xBF}

// Reader reads lines of files, loading each file once for as long as it is
// unchanged: a run asks for the lines of one file for every finding on it. A
// file that changed since it was loaded — a fixer ran on it — is loaded again.
// It is safe for concurrent use.
type Reader struct {
	mu    sync.Mutex
	files map[string]*loaded
}

type loaded struct {
	size  int64
	mod   time.Time
	lines [][]byte
}

// NewReader returns a Reader with nothing loaded.
func NewReader() *Reader {
	return &Reader{files: map[string]*loaded{}}
}

// LineAt returns line row (1-based) of path without its terminator, and the
// first line without a byte-order mark.
func (r *Reader) LineAt(path string, row int) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read line: %w", err)
	}
	r.mu.Lock()
	f, ok := r.files[path]
	r.mu.Unlock()
	if !ok || f.size != info.Size() || !f.mod.Equal(info.ModTime()) {
		f, err = load(path, info)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.files[path] = f
		r.mu.Unlock()
	}
	if row < 1 || row > len(f.lines) {
		return nil, fmt.Errorf("%s:%d: %w", path, row, ErrNoLine)
	}
	return f.lines[row-1], nil
}

func load(path string, info os.FileInfo) (*loaded, error) {
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, maxFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read lines: %w", err)
	}
	return &loaded{size: info.Size(), mod: info.ModTime(), lines: Lines(data)}, nil
}

// Lines splits content into lines as LineAt returns them. Content that ends
// with a terminator has no empty line after it.
func Lines(content []byte) [][]byte {
	content = bytes.TrimPrefix(content, bom)
	if len(content) == 0 {
		return nil
	}
	lines := bytes.Split(content, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = bytes.TrimSuffix(line, []byte("\r"))
	}
	return lines
}
