package binmanager

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
	"go.uber.org/zap"
)

// extractBinary extracts binary from archive or copies file
func extractBinary(archivePath string, contentType BinContentType, binaryPath *string, destDir string) (string, error) {
	switch contentType {
	case BinContentTypeBinary:
		return extractBinaryFile(archivePath, destDir)

	case BinContentTypeGz:
		return extractGz(archivePath, binaryPath, destDir)

	case BinContentTypeBz2:
		return extractBz2(archivePath, binaryPath, destDir)

	case BinContentTypeXz:
		return extractXz(archivePath, binaryPath, destDir)

	case BinContentTypeZst:
		return extractZst(archivePath, binaryPath, destDir)

	case BinContentTypeTarGz:
		return extractTarGz(archivePath, binaryPath, destDir)

	case BinContentTypeTarXz:
		return extractTarXz(archivePath, binaryPath, destDir)

	case BinContentTypeTarBz2:
		return extractTarBz2(archivePath, binaryPath, destDir)

	case BinContentTypeTarZst:
		return extractTarZst(archivePath, binaryPath, destDir)

	case BinContentTypeTar:
		return extractTar(archivePath, binaryPath, destDir)

	case BinContentTypeZip:
		return extractZip(archivePath, binaryPath, destDir)

	default:
		return "", fmt.Errorf("unsupported content type: %s", contentType)
	}
}

func extractBinaryFile(srcPath string, destDir string) (string, error) {
	tmpFile, err := os.CreateTemp(destDir, "binary-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
	}

	src, err := os.Open(srcPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			log.Warn("failed to close source file", zap.Error(err))
		}
	}()

	dst, err := os.Create(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		if err := dst.Close(); err != nil {
			log.Warn("failed to close destination file", zap.Error(err))
		}
	}()

	written, err := io.Copy(dst, io.LimitReader(src, MaxBinarySize+1))
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to copy file: %w", err)
	}
	if written > MaxBinarySize {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("binary file exceeds maximum size of %d bytes", MaxBinarySize)
	}

	log.Debug("binary file copied", zap.String("dst", tmpPath))

	return tmpPath, nil
}

func extractGz(gzPath string, binaryPath *string, destDir string) (string, error) {
	gzFile, err := os.Open(gzPath)
	if err != nil {
		return "", fmt.Errorf("failed to open gz file: %w", err)
	}
	defer func() {
		if err := gzFile.Close(); err != nil {
			log.Warn("failed to close gz file", zap.Error(err))
		}
	}()

	gzReader, err := gzip.NewReader(gzFile)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() {
		if err := gzReader.Close(); err != nil {
			log.Warn("failed to close gzip reader", zap.Error(err))
		}
	}()

	tmpFile, err := os.CreateTemp(destDir, "extracted-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if err := tmpFile.Close(); err != nil {
			log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
		}
	}()

	written, err := io.Copy(tmpFile, io.LimitReader(gzReader, MaxBinarySize+1))
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to extract gz: %w", err)
	}
	if written > MaxBinarySize {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("extracted content exceeds maximum size of %d bytes", MaxBinarySize)
	}

	log.Debug("gz extracted", zap.String("dst", tmpPath))

	return tmpPath, nil
}

func extractTarGz(tarGzPath string, binaryPath *string, destDir string) (string, error) {
	if binaryPath == nil {
		return "", fmt.Errorf("binaryPath is required for tar.gz archives")
	}

	file, err := os.Open(tarGzPath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar.gz file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar.gz file", zap.Error(err))
		}
	}()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() {
		if err := gzReader.Close(); err != nil {
			log.Warn("failed to close gzip reader", zap.Error(err))
		}
	}()

	tarReader := tar.NewReader(gzReader)

	targetPath := *binaryPath
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to read tar header: %w", err)
		}

		if header.Typeflag == tar.TypeReg && matchPath(header.Name, targetPath) {
			tmpFile, err := os.CreateTemp(destDir, "extracted-*")
			if err != nil {
				return "", fmt.Errorf("failed to create temp file: %w", err)
			}
			tmpPath := tmpFile.Name()
			defer func() {
				if err := tmpFile.Close(); err != nil {
					log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
				}
			}()

			written, err := io.Copy(tmpFile, io.LimitReader(tarReader, MaxBinarySize+1))
			if err != nil {
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("failed to extract file from tar: %w", err)
			}
			if written > MaxBinarySize {
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("extracted entry exceeds maximum size of %d bytes", MaxBinarySize)
			}

			log.Debug("file extracted from tar.gz",
				zap.String("archive", tarGzPath),
				zap.String("file", header.Name),
				zap.String("dst", tmpPath),
			)

			return tmpPath, nil
		}
	}

	return "", fmt.Errorf("file '%s' not found in tar.gz archive", targetPath)
}

func extractTarXz(tarXzPath string, binaryPath *string, destDir string) (string, error) {
	if binaryPath == nil {
		return "", fmt.Errorf("binaryPath is required for tar.xz archives")
	}

	file, err := os.Open(tarXzPath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar.xz file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar.xz file", zap.Error(err))
		}
	}()

	xzReader, err := xz.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create xz reader: %w", err)
	}

	tarReader := tar.NewReader(xzReader)

	targetPath := *binaryPath
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to read tar header: %w", err)
		}

		if header.Typeflag == tar.TypeReg && matchPath(header.Name, targetPath) {
			tmpFile, err := os.CreateTemp(destDir, "extracted-*")
			if err != nil {
				return "", fmt.Errorf("failed to create temp file: %w", err)
			}
			tmpPath := tmpFile.Name()
			defer func() {
				if err := tmpFile.Close(); err != nil {
					log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
				}
			}()

			written, err := io.Copy(tmpFile, io.LimitReader(tarReader, MaxBinarySize+1))
			if err != nil {
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("failed to extract file from tar: %w", err)
			}
			if written > MaxBinarySize {
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("extracted entry exceeds maximum size of %d bytes", MaxBinarySize)
			}

			log.Debug("file extracted from tar.xz",
				zap.String("archive", tarXzPath),
				zap.String("file", header.Name),
				zap.String("dst", tmpPath),
			)

			return tmpPath, nil
		}
	}

	return "", fmt.Errorf("file '%s' not found in tar.xz archive", targetPath)
}

func extractZip(zipPath string, binaryPath *string, destDir string) (string, error) {
	if binaryPath == nil {
		return "", fmt.Errorf("binaryPath is required for zip archives")
	}

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			log.Warn("failed to close zip reader", zap.Error(err))
		}
	}()

	targetPath := *binaryPath

	for _, file := range reader.File {
		if matchPath(file.Name, targetPath) {
			rc, err := file.Open()
			if err != nil {
				return "", fmt.Errorf("failed to open file from zip: %w", err)
			}
			defer func() {
				if err := rc.Close(); err != nil {
					log.Warn("failed to close zip file reader", zap.Error(err))
				}
			}()

			tmpFile, err := os.CreateTemp(destDir, "extracted-*")
			if err != nil {
				return "", fmt.Errorf("failed to create temp file: %w", err)
			}
			tmpPath := tmpFile.Name()
			defer func() {
				if err := tmpFile.Close(); err != nil {
					log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
				}
			}()

			written, err := io.Copy(tmpFile, io.LimitReader(rc, MaxBinarySize+1))
			if err != nil {
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("failed to extract file from zip: %w", err)
			}
			if written > MaxBinarySize {
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("extracted entry exceeds maximum size of %d bytes", MaxBinarySize)
			}

			log.Debug("file extracted from zip",
				zap.String("archive", zipPath),
				zap.String("file", file.Name),
				zap.String("dst", tmpPath),
			)

			return tmpPath, nil
		}
	}

	return "", fmt.Errorf("file '%s' not found in zip archive", targetPath)
}

func extractTarBz2(tarBz2Path string, binaryPath *string, destDir string) (string, error) {
	if binaryPath == nil {
		return "", fmt.Errorf("binaryPath is required for tar.bz2 archives")
	}

	file, err := os.Open(tarBz2Path)
	if err != nil {
		return "", fmt.Errorf("failed to open tar.bz2 file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar.bz2 file", zap.Error(err))
		}
	}()

	bz2Reader := bzip2.NewReader(file)
	return extractFromTar(tar.NewReader(bz2Reader), *binaryPath, "tar.bz2", tarBz2Path, destDir)
}

func extractTarZst(tarZstPath string, binaryPath *string, destDir string) (string, error) {
	if binaryPath == nil {
		return "", fmt.Errorf("binaryPath is required for tar.zst archives")
	}

	file, err := os.Open(tarZstPath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar.zst file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar.zst file", zap.Error(err))
		}
	}()

	zstReader, err := zstd.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer zstReader.Close()

	return extractFromTar(tar.NewReader(zstReader), *binaryPath, "tar.zst", tarZstPath, destDir)
}

func extractTar(tarPath string, binaryPath *string, destDir string) (string, error) {
	if binaryPath == nil {
		return "", fmt.Errorf("binaryPath is required for tar archives")
	}

	file, err := os.Open(tarPath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar file", zap.Error(err))
		}
	}()

	return extractFromTar(tar.NewReader(file), *binaryPath, "tar", tarPath, destDir)
}

func extractFromTar(tarReader *tar.Reader, targetPath, archiveType, archivePath, destDir string) (string, error) {
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to read tar header: %w", err)
		}

		if header.Typeflag == tar.TypeReg && matchPath(header.Name, targetPath) {
			tmpFile, err := os.CreateTemp(destDir, "extracted-*")
			if err != nil {
				return "", fmt.Errorf("failed to create temp file: %w", err)
			}
			tmpPath := tmpFile.Name()
			defer func() {
				if err := tmpFile.Close(); err != nil {
					log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
				}
			}()

			written, err := io.Copy(tmpFile, io.LimitReader(tarReader, MaxBinarySize+1))
			if err != nil {
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("failed to extract file from tar: %w", err)
			}
			if written > MaxBinarySize {
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("extracted entry exceeds maximum size of %d bytes", MaxBinarySize)
			}

			log.Debug("file extracted from "+archiveType,
				zap.String("archive", archivePath),
				zap.String("file", header.Name),
				zap.String("dst", tmpPath),
			)

			return tmpPath, nil
		}
	}

	return "", fmt.Errorf("file '%s' not found in %s archive", targetPath, archiveType)
}

func extractBz2(bz2Path string, binaryPath *string, destDir string) (string, error) {
	file, err := os.Open(bz2Path)
	if err != nil {
		return "", fmt.Errorf("failed to open bz2 file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close bz2 file", zap.Error(err))
		}
	}()

	bz2Reader := bzip2.NewReader(file)
	return extractSingleFile(bz2Reader, "bz2", destDir)
}

func extractXz(xzPath string, binaryPath *string, destDir string) (string, error) {
	file, err := os.Open(xzPath)
	if err != nil {
		return "", fmt.Errorf("failed to open xz file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close xz file", zap.Error(err))
		}
	}()

	xzReader, err := xz.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create xz reader: %w", err)
	}

	return extractSingleFile(xzReader, "xz", destDir)
}

func extractZst(zstPath string, binaryPath *string, destDir string) (string, error) {
	file, err := os.Open(zstPath)
	if err != nil {
		return "", fmt.Errorf("failed to open zst file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close zst file", zap.Error(err))
		}
	}()

	zstReader, err := zstd.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer zstReader.Close()

	return extractSingleFile(zstReader, "zst", destDir)
}

func extractSingleFile(reader io.Reader, format, destDir string) (string, error) {
	tmpFile, err := os.CreateTemp(destDir, "extracted-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if err := tmpFile.Close(); err != nil {
			log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
		}
	}()

	written, err := io.Copy(tmpFile, io.LimitReader(reader, MaxBinarySize+1))
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to extract %s: %w", format, err)
	}
	if written > MaxBinarySize {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("extracted content exceeds maximum size of %d bytes", MaxBinarySize)
	}

	log.Debug(format+" extracted", zap.String("dst", tmpPath))

	return tmpPath, nil
}

// extractBinaryToDir extracts an entire archive to a directory
func extractBinaryToDir(archivePath string, contentType BinContentType, destDir string) (string, error) {
	switch contentType {
	case BinContentTypeTarGz:
		return extractTarGzToDir(archivePath, destDir)
	case BinContentTypeTarXz:
		return extractTarXzToDir(archivePath, destDir)
	case BinContentTypeTarBz2:
		return extractTarBz2ToDir(archivePath, destDir)
	case BinContentTypeTarZst:
		return extractTarZstToDir(archivePath, destDir)
	case BinContentTypeTar:
		return extractTarPlainToDir(archivePath, destDir)
	case BinContentTypeZip:
		return extractZipToDir(archivePath, destDir)
	default:
		return "", fmt.Errorf("unsupported content type for directory extraction: %s", contentType)
	}
}

func extractTarGzToDir(archivePath, destDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar.gz file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar.gz file", zap.Error(err))
		}
	}()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() {
		if err := gzReader.Close(); err != nil {
			log.Warn("failed to close gzip reader", zap.Error(err))
		}
	}()

	return extractTarToDir(tar.NewReader(gzReader), destDir)
}

func extractTarXzToDir(archivePath, destDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar.xz file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar.xz file", zap.Error(err))
		}
	}()

	xzReader, err := xz.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create xz reader: %w", err)
	}

	return extractTarToDir(tar.NewReader(xzReader), destDir)
}

func extractTarBz2ToDir(archivePath, destDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar.bz2 file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar.bz2 file", zap.Error(err))
		}
	}()

	bz2Reader := bzip2.NewReader(file)
	return extractTarToDir(tar.NewReader(bz2Reader), destDir)
}

func extractTarZstToDir(archivePath, destDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar.zst file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar.zst file", zap.Error(err))
		}
	}()

	zstReader, err := zstd.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer zstReader.Close()

	return extractTarToDir(tar.NewReader(zstReader), destDir)
}

func extractTarPlainToDir(archivePath, destDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close tar file", zap.Error(err))
		}
	}()

	return extractTarToDir(tar.NewReader(file), destDir)
}

func extractZipToDir(zipPath, destDir string) (string, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			log.Warn("failed to close zip reader", zap.Error(err))
		}
	}()

	tmpDir, err := os.MkdirTemp(destDir, "extractdir-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	const maxTotalExtractedSize int64 = 2 * 1024 * 1024 * 1024
	var totalExtracted int64

	for _, file := range reader.File {
		if err := validateArchivePath(file.Name); err != nil {
			log.Warn("skipping unsafe archive entry", zap.String("path", file.Name), zap.Error(err))
			continue
		}

		target := filepath.Join(tmpDir, file.Name)

		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(cleanTarget, tmpDir+string(filepath.Separator)) && cleanTarget != tmpDir {
			log.Warn("skipping archive entry that escapes destination", zap.String("path", file.Name))
			continue
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("failed to create directory %q: %w", file.Name, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("failed to create parent directory for %q: %w", file.Name, err)
		}

		rc, err := file.Open()
		if err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("failed to open file from zip: %w", err)
		}

		outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode()&0777)
		if err != nil {
			_ = rc.Close()
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("failed to create file %q: %w", file.Name, err)
		}

		written, err := io.Copy(outFile, io.LimitReader(rc, MaxBinarySize+1))
		closeErr := outFile.Close()
		_ = rc.Close()
		if err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("failed to write file %q: %w", file.Name, err)
		}
		if closeErr != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("failed to close file %q: %w", file.Name, closeErr)
		}
		if written > MaxBinarySize {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("file %q exceeds maximum size of %d bytes", file.Name, MaxBinarySize)
		}
		totalExtracted += written
		if totalExtracted > maxTotalExtractedSize {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("total extracted size exceeds maximum of %d bytes", maxTotalExtractedSize)
		}
	}

	return tmpDir, nil
}

func extractTarToDir(tarReader *tar.Reader, destDir string) (string, error) {
	tmpDir, err := os.MkdirTemp(destDir, "extractdir-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	const maxTotalExtractedSize int64 = 2 * 1024 * 1024 * 1024
	var totalExtracted int64

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("failed to read tar header: %w", err)
		}

		if err := validateArchivePath(header.Name); err != nil {
			log.Warn("skipping unsafe archive entry", zap.String("path", header.Name), zap.Error(err))
			continue
		}

		target := filepath.Join(tmpDir, header.Name)

		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(cleanTarget, tmpDir+string(filepath.Separator)) && cleanTarget != tmpDir {
			log.Warn("skipping archive entry that escapes destination", zap.String("path", header.Name))
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0777|0755); err != nil {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("failed to create directory %q: %w", header.Name, err)
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("failed to create parent directory for %q: %w", header.Name, err)
			}

			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0777)
			if err != nil {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("failed to create file %q: %w", header.Name, err)
			}

			written, err := io.Copy(outFile, io.LimitReader(tarReader, MaxBinarySize+1))
			closeErr := outFile.Close()
			if err != nil {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("failed to write file %q: %w", header.Name, err)
			}
			if closeErr != nil {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("failed to close file %q: %w", header.Name, closeErr)
			}
			if written > MaxBinarySize {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("file %q exceeds maximum size of %d bytes", header.Name, MaxBinarySize)
			}
			totalExtracted += written
			if totalExtracted > maxTotalExtractedSize {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("total extracted size exceeds maximum of %d bytes", maxTotalExtractedSize)
			}

		case tar.TypeSymlink:
			linkTarget := header.Linkname
			if filepath.IsAbs(linkTarget) {
				log.Warn("skipping absolute symlink", zap.String("path", header.Name), zap.String("target", linkTarget))
				continue
			}
			resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(target), linkTarget))
			if !strings.HasPrefix(resolvedTarget, tmpDir+string(filepath.Separator)) && resolvedTarget != tmpDir {
				log.Warn("skipping symlink that escapes destination", zap.String("path", header.Name), zap.String("target", linkTarget))
				continue
			}

			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("failed to create parent directory for symlink %q: %w", header.Name, err)
			}

			if err := os.Symlink(linkTarget, target); err != nil {
				_ = os.RemoveAll(tmpDir)
				return "", fmt.Errorf("failed to create symlink %q -> %q: %w", header.Name, linkTarget, err)
			}
		}
	}

	log.Debug("archive extracted to directory", zap.String("dst", tmpDir))
	return tmpDir, nil
}

// extractArchiveToPath extracts an archive to the specified destination path.
// For inline archives (tar data in memory), pass tarData. For external archives (file on disk), pass archivePath.
// Returns the destination path on success.
func extractArchiveToPath(destPath string, tarData []byte, archivePath string, format BinContentType) (string, error) {
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	var tarReader *tar.Reader

	if tarData != nil {
		tarReader = tar.NewReader(bytes.NewReader(tarData))
	} else if archivePath != "" {
		file, err := os.Open(archivePath)
		if err != nil {
			return "", fmt.Errorf("failed to open archive file: %w", err)
		}
		defer func() {
			if err := file.Close(); err != nil {
				log.Warn("failed to close archive file", zap.Error(err))
			}
		}()

		switch format {
		case BinContentTypeTarGz:
			gzReader, err := gzip.NewReader(file)
			if err != nil {
				return "", fmt.Errorf("failed to create gzip reader: %w", err)
			}
			defer func() {
				if err := gzReader.Close(); err != nil {
					log.Warn("failed to close gzip reader", zap.Error(err))
				}
			}()
			tarReader = tar.NewReader(gzReader)

		case BinContentTypeTarXz:
			xzReader, err := xz.NewReader(file)
			if err != nil {
				return "", fmt.Errorf("failed to create xz reader: %w", err)
			}
			tarReader = tar.NewReader(xzReader)

		case BinContentTypeTarBz2:
			bz2Reader := bzip2.NewReader(file)
			tarReader = tar.NewReader(bz2Reader)

		case BinContentTypeTarZst:
			zstReader, err := zstd.NewReader(file)
			if err != nil {
				return "", fmt.Errorf("failed to create zstd reader: %w", err)
			}
			defer zstReader.Close()
			tarReader = tar.NewReader(zstReader)

		case BinContentTypeTar:
			tarReader = tar.NewReader(file)

		default:
			return "", fmt.Errorf("unsupported archive format: %s (expected tar, tar.gz, tar.xz, tar.bz2, or tar.zst)", format)
		}
	} else {
		return "", fmt.Errorf("either tarData or archivePath must be provided")
	}

	const maxTotalExtractedSize int64 = 2 * 1024 * 1024 * 1024
	var totalExtracted int64

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to read tar header: %w", err)
		}

		if err := validateArchivePath(header.Name); err != nil {
			log.Warn("skipping unsafe archive entry", zap.String("path", header.Name), zap.Error(err))
			continue
		}

		target := filepath.Join(destPath, header.Name)

		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(cleanTarget, destPath+string(filepath.Separator)) && cleanTarget != destPath {
			log.Warn("skipping archive entry that escapes destination", zap.String("path", header.Name))
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0777|0755); err != nil {
				return "", fmt.Errorf("failed to create directory %q: %w", header.Name, err)
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return "", fmt.Errorf("failed to create parent directory for %q: %w", header.Name, err)
			}

			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0777)
			if err != nil {
				return "", fmt.Errorf("failed to create file %q: %w", header.Name, err)
			}

			written, err := io.Copy(outFile, io.LimitReader(tarReader, MaxBinarySize+1))
			closeErr := outFile.Close()
			if err != nil {
				return "", fmt.Errorf("failed to write file %q: %w", header.Name, err)
			}
			if closeErr != nil {
				return "", fmt.Errorf("failed to close file %q: %w", header.Name, closeErr)
			}
			if written > MaxBinarySize {
				return "", fmt.Errorf("file %q exceeds maximum size of %d bytes", header.Name, MaxBinarySize)
			}

			totalExtracted += written
			if totalExtracted > maxTotalExtractedSize {
				return "", fmt.Errorf("total extracted size exceeds maximum of %d bytes", maxTotalExtractedSize)
			}

		case tar.TypeSymlink:
			linkTarget := header.Linkname
			if filepath.IsAbs(linkTarget) {
				log.Warn("skipping absolute symlink", zap.String("path", header.Name), zap.String("target", linkTarget))
				continue
			}
			resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(target), linkTarget))
			if !strings.HasPrefix(resolvedTarget, destPath+string(filepath.Separator)) && resolvedTarget != destPath {
				log.Warn("skipping symlink that escapes destination", zap.String("path", header.Name), zap.String("target", linkTarget))
				continue
			}

			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return "", fmt.Errorf("failed to create parent directory for symlink %q: %w", header.Name, err)
			}

			if err := os.Symlink(linkTarget, target); err != nil {
				return "", fmt.Errorf("failed to create symlink %q -> %q: %w", header.Name, linkTarget, err)
			}
		}
	}

	log.Debug("archive extracted to path", zap.String("dest", destPath), zap.Int64("totalBytes", totalExtracted))
	return destPath, nil
}

// validateArchivePath ensures archive path doesn't escape destination directory
func validateArchivePath(archivePath string) error {
	cleaned := filepath.Clean(archivePath)

	if filepath.IsAbs(cleaned) {
		return fmt.Errorf("archive contains absolute path: %s", archivePath)
	}

	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+"..") {
		return fmt.Errorf("archive contains path traversal: %s", archivePath)
	}

	if cleaned == "" || cleaned == "." {
		return fmt.Errorf("archive contains invalid path: %s", archivePath)
	}

	return nil
}

// matchPath checks if archive path matches target path
// Supports exact match and filename-only match
func matchPath(archivePath, targetPath string) bool {
	if err := validateArchivePath(archivePath); err != nil {
		log.Warn("rejecting unsafe archive path",
			zap.String("path", archivePath),
			zap.Error(err))
		return false
	}

	// validateArchivePath checks the cleaned path, but "bin/../evil/tool" cleans to
	// "evil/tool" and passes. Check each path component individually to reject ".."
	// traversal segments without false-positives on benign names like "my..lib.so".
	for _, part := range strings.Split(filepath.ToSlash(archivePath), "/") {
		if part == ".." {
			log.Warn("rejecting archive path with traversal component",
				zap.String("path", archivePath))
			return false
		}
	}

	archivePath = filepath.ToSlash(archivePath)
	targetPath = filepath.ToSlash(targetPath)

	if archivePath == targetPath {
		return true
	}

	if strings.HasSuffix(archivePath, "/"+targetPath) {
		return true
	}

	if filepath.Base(archivePath) == filepath.Base(targetPath) {
		return true
	}

	return false
}
