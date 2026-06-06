package runtimemanager

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/datamitsu/datamitsu/internal/constants"

	"github.com/andybalholm/brotli"
)

const (
	brotliPrefix                = "br:"
	maxDecompressedLockFileSize = constants.MaxInlineArchiveSize
)

// CompressLockFile brotli-compresses content and returns it base64-encoded with
// a "br:" prefix so DecompressLockFile can detect the encoding.
func CompressLockFile(content string) (string, error) {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, 11)
	if _, err := io.WriteString(w, content); err != nil {
		return "", fmt.Errorf("brotli compress: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("brotli close: %w", err)
	}
	return brotliPrefix + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// DecompressLockFile reverses CompressLockFile: data with the "br:" prefix is
// base64-decoded and brotli-decompressed (bounded to the max lock file size),
// while data without the prefix is returned unchanged.
func DecompressLockFile(data string) (string, error) {
	if !strings.HasPrefix(data, brotliPrefix) {
		return data, nil
	}
	encoded := data[len(brotliPrefix):]
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	decompressed, err := io.ReadAll(io.LimitReader(brotli.NewReader(bytes.NewReader(compressed)), maxDecompressedLockFileSize+1))
	if err != nil {
		return "", fmt.Errorf("brotli decompress: %w", err)
	}
	if int64(len(decompressed)) > maxDecompressedLockFileSize {
		return "", fmt.Errorf("decompressed lock file exceeds maximum size of %d bytes", maxDecompressedLockFileSize)
	}
	return string(decompressed), nil
}
