package binmanager

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
	// TarBrotliPrefix marks a string as a brotli-compressed, base64-encoded tar archive.
	TarBrotliPrefix = "tar.br:"
	// MaxDecompressedArchiveSize caps the size of a decompressed inline archive.
	MaxDecompressedArchiveSize = constants.MaxInlineArchiveSize
)

// CompressArchive brotli-compresses tar data and returns it base64-encoded with the TarBrotliPrefix.
func CompressArchive(tarData []byte) (string, error) {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, 11)
	if _, err := w.Write(tarData); err != nil {
		return "", fmt.Errorf("brotli compress: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("brotli close: %w", err)
	}
	return TarBrotliPrefix + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// DecompressArchive decodes and brotli-decompresses a TarBrotliPrefix-tagged string back into tar data.
func DecompressArchive(data string) ([]byte, error) {
	if !strings.HasPrefix(data, TarBrotliPrefix) {
		return nil, fmt.Errorf("invalid archive format: expected %q prefix", TarBrotliPrefix)
	}

	encoded := data[len(TarBrotliPrefix):]
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	decompressed, err := io.ReadAll(io.LimitReader(
		brotli.NewReader(bytes.NewReader(compressed)),
		MaxDecompressedArchiveSize+1,
	))
	if err != nil {
		return nil, fmt.Errorf("brotli decompress: %w", err)
	}

	if int64(len(decompressed)) > MaxDecompressedArchiveSize {
		return nil, fmt.Errorf("decompressed archive exceeds maximum size of %d bytes", MaxDecompressedArchiveSize)
	}

	return decompressed, nil
}
