// Package constants holds shared size limits and thresholds used across the
// binmanager, runtimemanager and devtools packing code.
package constants

// Decompressed-size limits for inline (embedded) archives: MaxInlineArchiveSize
// is the hard cap rejected during extraction/packing, WarnInlineArchiveSize is
// the threshold above which a size warning is emitted.
const (
	MaxInlineArchiveSize  int64 = 50 << 20 // 50 MiB decompressed
	WarnInlineArchiveSize int64 = 10 << 20 // 10 MiB decompressed
)
