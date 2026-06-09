package ocibundle

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	"go.uber.org/zap"
)

// Extractor caps. Deliberately wider than the generic extractor's (a Node
// runtime is >100 MiB compressed; large CPython .so files make 500 MiB per
// file a tight fit). Per-subtree layers keep real sizes far below — these are
// zip-bomb protection only, plain constants by design (§11.1 of the plan).
const (
	maxCompressedBlobBytes int64 = 2 << 30
	maxFileBytes           int64 = 2 << 30
	maxUnpackedLayerBytes  int64 = 8 << 30
)

// maxTextRewriteBytes bounds files eligible for textual relocation
// (pyvenv.cfg, shebang scripts). Real ones are well under a megabyte.
const maxTextRewriteBytes = 1 << 20

// layerExtractOptions parameterize the relocating extraction of one layer.
type layerExtractOptions struct {
	// Subtree is the store-relative subtree this layer owns (write-allowlist).
	Subtree string
	// BuilderStoreRoot is the absolute store root at build time, from the
	// manifest annotation (e.g. "/dm/store").
	BuilderStoreRoot string
	// ConsumerStoreRoot is this host's store root that absolute references
	// are rewritten to.
	ConsumerStoreRoot string
}

// extractLayerSubtree unpacks the declared subtree of a layer blob into a
// fresh location under stagingDir and returns the staged subtree path (a file
// for single-file binary subtrees, a directory otherwise).
//
// Differences from the generic binmanager extractor, all load-bearing here:
//   - write-allowlist: any entry outside the declared subtree is fatal, so a
//     drifted producer mapping is a loud failure instead of a cross-app write;
//   - absolute symlinks under the builder store root are relocated to the
//     consumer store root instead of dropped (uv venv interpreters);
//   - hardlinks are restored (pnpm), required to stay inside the subtree;
//   - pyvenv.cfg and shebang lines carrying the builder store root are
//     rewritten textually;
//   - caps are the wider bundle-specific constants above.
func extractLayerSubtree(blobPath string, comp layerCompression, opts layerExtractOptions, stagingDir string) (string, error) {
	if err := validateSubtree(opts.Subtree); err != nil {
		return "", err
	}
	if !path.IsAbs(opts.BuilderStoreRoot) {
		return "", fmt.Errorf("builder store root %q must be absolute", opts.BuilderStoreRoot)
	}

	file, err := os.Open(blobPath)
	if err != nil {
		return "", fmt.Errorf("open layer blob: %w", err)
	}
	defer func() { _ = file.Close() }()

	var decompressed io.Reader
	switch comp {
	case compressionGzip:
		gz, err := gzip.NewReader(file)
		if err != nil {
			return "", fmt.Errorf("open gzip layer: %w", err)
		}
		defer func() { _ = gz.Close() }()
		decompressed = gz
	case compressionZstd:
		zr, err := zstd.NewReader(file)
		if err != nil {
			return "", fmt.Errorf("open zstd layer: %w", err)
		}
		defer zr.Close()
		decompressed = zr
	default:
		return "", fmt.Errorf("unknown layer compression %d", comp)
	}

	// Entry names in a buildx/oci layer are rootfs-relative: "dm/store/...".
	builderPrefix := strings.TrimPrefix(path.Clean(opts.BuilderStoreRoot), "/")
	subtreePrefix := builderPrefix + "/" + opts.Subtree

	stagedRoot := filepath.Join(stagingDir, "content")
	stagingResolved, err := filepath.EvalSymlinks(stagingDir)
	if err != nil {
		return "", fmt.Errorf("resolve staging directory: %w", err)
	}

	var total int64
	var deferredLinks []deferredHardlink
	sawSubtree := false
	tr := tar.NewReader(decompressed)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read layer tar: %w", err)
		}

		name := normalizeEntryName(hdr.Name)
		if name == "" || name == "." {
			continue
		}

		rel, inside := entryRelToSubtree(name, subtreePrefix)
		if !inside {
			// Structural parents of the subtree (dm/, dm/store/, …) are part
			// of any layer tar; anything else violates the declared mapping.
			if hdr.Typeflag == tar.TypeDir && isAncestorOf(name, subtreePrefix) {
				continue
			}
			return "", fmt.Errorf("layer declares subtree %q but contains entry %q outside it", opts.Subtree, hdr.Name)
		}
		if strings.HasPrefix(path.Base(name), ".wh.") {
			return "", fmt.Errorf("unexpected whiteout entry %q in a store content layer", hdr.Name)
		}
		sawSubtree = true

		dest, err := secureDestPath(stagedRoot, stagingResolved, rel)
		if err != nil {
			return "", err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, tarEntryMode(hdr.Mode)|0o755); err != nil {
				return "", fmt.Errorf("create directory %q: %w", rel, err)
			}

		case tar.TypeReg, tar.TypeGNUSparse:
			if hdr.Size > maxFileBytes {
				return "", fmt.Errorf("file %q exceeds the %d byte per-file limit", hdr.Name, maxFileBytes)
			}
			total += hdr.Size
			if total > maxUnpackedLayerBytes {
				return "", fmt.Errorf("layer exceeds the %d byte unpacked limit", maxUnpackedLayerBytes)
			}
			if err := writeRegularFile(dest, tr, hdr); err != nil {
				return "", fmt.Errorf("write file %q: %w", rel, err)
			}

		case tar.TypeSymlink:
			if err := createRelocatedSymlink(dest, stagedRoot, hdr.Linkname, opts); err != nil {
				return "", err
			}

		case tar.TypeLink:
			linkName := normalizeEntryName(hdr.Linkname)
			linkRel, linkInside := entryRelToSubtree(linkName, subtreePrefix)
			if !linkInside {
				return "", fmt.Errorf("hardlink %q points outside its subtree (%q); producers must materialize external hardlinks", hdr.Name, hdr.Linkname)
			}
			linkSrc, err := secureDestPath(stagedRoot, stagingResolved, linkRel)
			if err != nil {
				return "", err
			}
			// The tar format does not guarantee the link target precedes the
			// link entry; restore hardlinks after the full pass.
			deferredLinks = append(deferredLinks, deferredHardlink{src: linkSrc, dest: dest, rel: rel, linkRel: linkRel})

		default:
			return "", fmt.Errorf("unsupported tar entry type %q for %q in a store content layer", hdr.Typeflag, hdr.Name)
		}
	}

	if !sawSubtree {
		return "", fmt.Errorf("layer declares subtree %q but contains no entries for it", opts.Subtree)
	}

	for _, link := range deferredLinks {
		if err := os.Link(link.src, link.dest); err != nil {
			return "", fmt.Errorf("restore hardlink %q -> %q: %w", link.rel, link.linkRel, err)
		}
	}

	if err := rewriteTextualStoreRefs(stagedRoot, opts); err != nil {
		return "", err
	}
	return stagedRoot, nil
}

// deferredHardlink is a TypeLink entry whose creation is postponed until the
// whole layer is read — tar does not guarantee the target precedes the link.
type deferredHardlink struct {
	src     string
	dest    string
	rel     string
	linkRel string
}

// normalizeEntryName cleans a tar entry name to a rootfs-relative slash path.
func normalizeEntryName(name string) string {
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimPrefix(name, "./")
	return path.Clean(name)
}

// entryRelToSubtree maps a normalized entry name to its path relative to the
// subtree root. rel "." denotes the subtree root itself (a file for
// single-file binary subtrees).
func entryRelToSubtree(name, subtreePrefix string) (rel string, inside bool) {
	if name == subtreePrefix {
		return ".", true
	}
	if rest, ok := strings.CutPrefix(name, subtreePrefix+"/"); ok {
		return rest, true
	}
	return "", false
}

// isAncestorOf reports whether dir is a path ancestor of target.
func isAncestorOf(dir, target string) bool {
	return strings.HasPrefix(target+"/", dir+"/")
}

// secureDestPath joins rel onto stagedRoot, rejecting traversal and refusing
// to write through a symlinked parent (a hostile layer could otherwise plant
// a symlink directory and write through it on a later entry).
func secureDestPath(stagedRoot, stagingResolved, rel string) (string, error) {
	if rel != "." && (strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") || rel == ".." || path.IsAbs(rel)) {
		return "", fmt.Errorf("entry path %q escapes its subtree", rel)
	}
	dest := stagedRoot
	if rel != "." {
		dest = filepath.Join(stagedRoot, filepath.FromSlash(rel))
	}
	if dest != stagedRoot && !strings.HasPrefix(dest, stagedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("entry path %q escapes its subtree", rel)
	}

	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create parent directory for %q: %w", rel, err)
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("resolve parent directory for %q: %w", rel, err)
	}
	if resolvedParent != stagingResolved && !strings.HasPrefix(resolvedParent, stagingResolved+string(filepath.Separator)) {
		return "", fmt.Errorf("entry %q would be written through a symlink escaping the staging directory", rel)
	}
	return dest, nil
}

func writeRegularFile(dest string, tr io.Reader, hdr *tar.Header) (retErr error) {
	mode := tarEntryMode(hdr.Mode)
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() {
		if cErr := out.Close(); cErr != nil && retErr == nil {
			retErr = cErr
		}
	}()
	written, err := io.Copy(out, io.LimitReader(tr, maxFileBytes+1))
	if err != nil {
		return fmt.Errorf("write content: %w", err)
	}
	if written > maxFileBytes {
		return fmt.Errorf("exceeds the %d byte per-file limit", maxFileBytes)
	}
	return nil
}

// createRelocatedSymlink recreates a symlink, rewriting an absolute target
// under the builder store root to the consumer store root. Relative targets
// are kept (cleaned) as long as they stay inside the subtree; absolute
// targets outside the builder store root are skipped with a warning, matching
// the generic extractor's posture. The value handed to os.Symlink is always
// re-derived from a containment-checked path, never the raw archive header.
func createRelocatedSymlink(dest, stagedRoot, linkTarget string, opts layerExtractOptions) error {
	var target string
	if path.IsAbs(linkTarget) {
		rest, ok := strings.CutPrefix(linkTarget, opts.BuilderStoreRoot+"/")
		switch {
		case ok:
			// filepath.Join cleans the result, so a hostile "store/../../etc"
			// tail could otherwise climb out of the consumer store root.
			target = filepath.Join(opts.ConsumerStoreRoot, filepath.FromSlash(rest))
			if target != opts.ConsumerStoreRoot && !strings.HasPrefix(target, opts.ConsumerStoreRoot+string(filepath.Separator)) {
				return fmt.Errorf("symlink %q target %q escapes the store after relocation", dest, linkTarget)
			}
		case linkTarget == opts.BuilderStoreRoot:
			target = opts.ConsumerStoreRoot
		default:
			log.Warn("skipping absolute symlink outside the builder store root",
				zap.String("path", dest), zap.String("target", linkTarget))
			return nil
		}
	} else {
		// Relative targets must stay inside the subtree, mirroring the
		// hardlink rule: a link reaching outside its subtree is a producer
		// error, not a layout we silently materialize. The created target is
		// recomputed from the checked absolute path, so only contained values
		// ever reach os.Symlink.
		resolved := filepath.Clean(filepath.Join(filepath.Dir(dest), filepath.FromSlash(linkTarget)))
		if resolved != stagedRoot && !strings.HasPrefix(resolved, stagedRoot+string(filepath.Separator)) {
			log.Warn("skipping relative symlink escaping its subtree",
				zap.String("path", dest), zap.String("target", linkTarget))
			return nil
		}
		rel, err := filepath.Rel(filepath.Dir(dest), resolved)
		if err != nil {
			return fmt.Errorf("resolve relative symlink %q -> %q: %w", dest, linkTarget, err)
		}
		target = rel
	}
	if err := os.Symlink(target, dest); err != nil {
		return fmt.Errorf("create symlink %q -> %q: %w", dest, target, err)
	}
	return nil
}

// rewriteTextualStoreRefs performs the §2.7.a2 textual relocation: pyvenv.cfg
// files (uv writes `home = /abs/...`) and shebang lines (`#!/abs/.../python`)
// carrying the builder store root are rewritten to the consumer store root.
// Only these whitelisted shapes are touched; binary files never match (a
// shebang rewrite requires the file to START with "#!" + the builder prefix).
func rewriteTextualStoreRefs(stagedRoot string, opts layerExtractOptions) error {
	builderPrefix := opts.BuilderStoreRoot
	consumerPrefix := opts.ConsumerStoreRoot

	err := filepath.Walk(stagedRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxTextRewriteBytes {
			return nil
		}

		if filepath.Base(p) == "pyvenv.cfg" {
			data, err := os.ReadFile(p) //nolint:gosec // G122: staging dir is created by MkdirTemp (0o700), private to this process
			if err != nil {
				return fmt.Errorf("read %q for relocation: %w", p, err)
			}
			if !bytes.Contains(data, []byte(builderPrefix)) {
				return nil
			}
			rewritten := bytes.ReplaceAll(data, []byte(builderPrefix), []byte(consumerPrefix))
			if err := os.WriteFile(p, rewritten, info.Mode().Perm()); err != nil { //nolint:gosec // G122: staging dir is process-private
				return fmt.Errorf("rewrite %q: %w", p, err)
			}
			return nil
		}

		return rewriteShebang(p, info, builderPrefix, consumerPrefix)
	})
	if err != nil {
		return fmt.Errorf("relocate textual store references: %w", err)
	}
	return nil
}

// tarEntryMode converts tar header permission bits to an os.FileMode. The
// mask keeps only the 9 permission bits, so the int64→FileMode conversion
// cannot overflow.
func tarEntryMode(mode int64) os.FileMode {
	return os.FileMode(uint32(mode & 0o777))
}

// rewriteShebang rewrites the first line of a script whose shebang points
// into the builder store root. Files not starting with "#!"+builder prefix
// are left untouched.
func rewriteShebang(p string, info os.FileInfo, builderPrefix, consumerPrefix string) error {
	file, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("open %q for shebang check: %w", p, err)
	}
	reader := bufio.NewReader(file)
	head, _ := reader.Peek(2 + len(builderPrefix))
	if !bytes.HasPrefix(head, append([]byte("#!"), builderPrefix...)) {
		_ = file.Close()
		return nil
	}
	data, err := io.ReadAll(reader)
	_ = file.Close()
	if err != nil {
		return fmt.Errorf("read %q for shebang rewrite: %w", p, err)
	}

	line, rest, _ := bytes.Cut(data, []byte("\n"))
	newLine := bytes.Replace(line, []byte(builderPrefix), []byte(consumerPrefix), 1)
	rewritten := append(append(newLine, '\n'), rest...)
	if err := os.WriteFile(p, rewritten, info.Mode().Perm()); err != nil {
		return fmt.Errorf("rewrite shebang of %q: %w", p, err)
	}
	return nil
}
