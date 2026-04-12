package runtimemanager

import (
	"bytes"
	"github.com/datamitsu/datamitsu/internal/constants"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
)

const brotliPrefix = "br:"
const maxDecompressedLockFileSize = constants.MaxInlineArchiveSize

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
