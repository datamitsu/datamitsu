package cmd

import (
	"archive/tar"
	"bytes"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/constants"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var packInlineArchiveCmd = &cobra.Command{
	Use:   "pack-inline-archive <directory>",
	Short: "Pack a directory into an inline archive (tar.br: format)",
	Long: `Pack a directory into a brotli-compressed tar archive for use in inline archive configs.

Output is written to stdout in tar.br: format (tar → brotli level 11 → base64).
Progress and warnings are written to stderr.

Archives are deterministic: identical directory contents will produce identical output
regardless of file modification times, system users, or creation location.

Example:
  datamitsu devtools pack-inline-archive ./my-config
  ARCHIVE=$(datamitsu devtools pack-inline-archive ./my-config)`,
	Args: cobra.ExactArgs(1),
	RunE: runPackInlineArchive,
}

func runPackInlineArchive(cmd *cobra.Command, args []string) error {
	dirPath := args[0]

	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dirPath)
	}

	tarData, err := createTarFromDir(absDir, constants.MaxInlineArchiveSize)
	if err != nil {
		return fmt.Errorf("creating tar: %w", err)
	}

	tarSize := int64(len(tarData))

	if tarSize > constants.WarnInlineArchiveSize {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: decompressed size %d bytes (%.1f MiB) exceeds recommended %d bytes (%.1f MiB)\n",
			tarSize, float64(tarSize)/(1<<20),
			constants.WarnInlineArchiveSize, float64(constants.WarnInlineArchiveSize)/(1<<20))
	}

	encoded, err := binmanager.CompressArchive(tarData)
	if err != nil {
		return fmt.Errorf("compressing archive: %w", err)
	}

	_, _ = fmt.Fprint(cmd.OutOrStdout(), encoded)
	return nil
}

type limitedWriter struct {
	w     io.Writer
	n     int64
	limit int64
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.n+int64(len(p)) > lw.limit {
		return 0, fmt.Errorf("decompressed size exceeds maximum of %d bytes (%.1f MiB)",
			lw.limit, float64(lw.limit)/(1<<20))
	}
	n, err := lw.w.Write(p)
	lw.n += int64(n)
	return n, err
}

type fileEntry struct {
	relPath  string
	info     os.FileInfo
	fullPath string
}

func collectFiles(dirPath string) ([]fileEntry, error) {
	var entries []fileEntry

	err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		if relPath == "." {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", relPath, err)
		}

		entries = append(entries, fileEntry{
			relPath:  relPath,
			info:     info,
			fullPath: path,
		})

		return nil
	})

	if err != nil {
		return nil, err
	}

	slices.SortFunc(entries, func(a, b fileEntry) int {
		return strings.Compare(a.relPath, b.relPath)
	})

	return entries, nil
}

func normalizeTarHeader(header *tar.Header) {
	header.ModTime = time.Unix(0, 0).UTC()
	header.Uid = 0
	header.Gid = 0
	header.Uname = ""
	header.Gname = ""
	header.AccessTime = time.Time{}
	header.ChangeTime = time.Time{}
}

func createTarFromDir(dirPath string, maxSize int64) ([]byte, error) {
	entries, err := collectFiles(dirPath)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	lw := &limitedWriter{w: &buf, limit: maxSize}
	tw := tar.NewWriter(lw)

	for _, entry := range entries {
		header, err := tar.FileInfoHeader(entry.info, "")
		if err != nil {
			return nil, fmt.Errorf("creating tar header for %s: %w", entry.relPath, err)
		}

		header.Name = filepath.ToSlash(entry.relPath)
		if entry.info.IsDir() {
			header.Name += "/"
		}

		normalizeTarHeader(header)

		if err := tw.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("writing tar header for %s: %w", entry.relPath, err)
		}

		if entry.info.IsDir() {
			continue
		}

		f, err := os.Open(entry.fullPath)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", entry.relPath, err)
		}

		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()

		if copyErr != nil {
			return nil, fmt.Errorf("writing %s to tar: %w", entry.relPath, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing %s: %w", entry.relPath, closeErr)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("closing tar writer: %w", err)
	}

	return buf.Bytes(), nil
}
