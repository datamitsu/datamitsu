package binmanager

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"go.uber.org/zap"
)

// MaxBinarySize is the maximum allowed download size (500 MiB).
const MaxBinarySize = 500 * 1024 * 1024

var httpClient = &http.Client{
	Timeout: 5 * time.Minute,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
			return fmt.Errorf("HTTPS to HTTP redirect rejected: %s", req.URL)
		}
		return nil
	},
}

func downloadFileInternal(url string, destDir string, name string, progress *mpb.Progress) (string, error) {
	if name != "" {
		log.Debug("downloading file", zap.String("url", url), zap.String("name", name))
	} else {
		log.Debug("downloading file", zap.String("url", url))
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(destDir, "download-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if err := tmpFile.Close(); err != nil {
			log.Warn("failed to close temp file", zap.String("path", tmpPath), zap.Error(err))
		}
	}()

	resp, err := httpClient.Get(url)
	if err != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after download error", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after bad status", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	if resp.ContentLength > MaxBinarySize {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after size rejection", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("file too large: Content-Length %d exceeds maximum %d bytes", resp.ContentLength, MaxBinarySize)
	}

	// LimitReader with MaxBinarySize+1 allows us to distinguish a file that is
	// exactly MaxBinarySize (written == MaxBinarySize, allowed) from one that
	// exceeds it (written > MaxBinarySize, rejected).
	limitedReader := io.LimitReader(resp.Body, MaxBinarySize+1)
	var reader = limitedReader
	if progress != nil {
		bar := progress.AddBar(resp.ContentLength,
			mpb.PrependDecorators(
				decor.Name(name, decor.WC{W: 20, C: decor.DSyncWidthR}),
				decor.CountersKibiByte("% .2f / % .2f"),
			),
			mpb.AppendDecorators(
				decor.NewPercentage(" %.0f ", decor.WCSyncSpace),
				decor.EwmaSpeed(decor.SizeB1024(0), " % .2f", 60),
			),
		)

		proxyReader := bar.ProxyReader(limitedReader)
		defer func() {
			if err := proxyReader.Close(); err != nil {
				log.Warn("failed to close proxy reader", zap.Error(err))
			}
		}()
		reader = proxyReader
	}

	written, err := io.Copy(tmpFile, reader)
	if err != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after write error", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	if written > MaxBinarySize {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove oversized temp file", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("file too large: exceeded maximum %d bytes", MaxBinarySize)
	}

	log.Debug("file downloaded",
		zap.String("path", tmpPath),
		zap.Int64("size", written),
	)

	return tmpPath, nil
}

func downloadFile(url string, destDir string) (string, error) {
	return downloadFileInternal(url, destDir, "", nil)
}

func downloadAndVerifyInternal(url string, expectedHash string, hashType BinHashType, destDir string, name string, progress *mpb.Progress) (string, error) {
	tmpPath, err := downloadFileInternal(url, destDir, name, progress)
	if err != nil {
		return "", err
	}

	if err := verifyFileHash(tmpPath, expectedHash, hashType); err != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Warn("failed to remove temp file after hash verification failure", zap.String("path", tmpPath), zap.Error(removeErr))
		}
		return "", fmt.Errorf("hash verification failed: %w", err)
	}

	log.Debug("file hash verified", zap.String("path", tmpPath))

	return tmpPath, nil
}

func downloadAndVerify(url string, expectedHash string, hashType BinHashType, destDir string) (string, error) {
	return downloadAndVerifyInternal(url, expectedHash, hashType, destDir, "", nil)
}

func downloadAndVerifyWithProgress(url string, expectedHash string, hashType BinHashType, destDir string, name string, progress *mpb.Progress) (string, error) {
	return downloadAndVerifyInternal(url, expectedHash, hashType, destDir, name, progress)
}

func moveFile(src, dst string) error {
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	if _, err := os.Stat(dst); err == nil {
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove existing file: %w", err)
		}
	}

	if err := os.Rename(src, dst); err != nil {
		if copyErr := copyFile(src, dst); copyErr != nil {
			return fmt.Errorf("rename failed: %w, and copy fallback failed: %w", err, copyErr)
		}
		_ = os.Remove(src)
	}

	if err := os.Chmod(dst, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	log.Debug("file moved and permissions set", zap.String("dst", dst))

	return nil
}

func moveDir(src, dst string) error {
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination parent directory: %w", err)
	}

	if _, err := os.Stat(dst); err == nil {
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("failed to remove existing directory: %w", err)
		}
	}

	if err := os.Rename(src, dst); err != nil {
		if copyErr := copyDir(src, dst); copyErr != nil {
			return fmt.Errorf("rename failed: %w, and copy fallback failed: %w", err, copyErr)
		}
		_ = os.RemoveAll(src)
	}

	log.Debug("directory moved", zap.String("dst", dst))
	return nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, dstPath); err != nil {
				return err
			}
		} else if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) (retErr error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := srcFile.Close(); cErr != nil && retErr == nil {
			retErr = cErr
		}
	}()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer func() {
		if cErr := dstFile.Close(); cErr != nil && retErr == nil {
			retErr = cErr
		}
	}()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
