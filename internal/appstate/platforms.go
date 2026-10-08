package appstate

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

// SupportedPlatforms is the exact target matrix accepted by GitHub manifests.
func SupportedPlatforms() []string {
	return []string{
		"darwin/amd64", "darwin/arm64",
		"windows/amd64", "windows/arm64",
		"freebsd/amd64", "freebsd/arm64",
		"openbsd/amd64", "openbsd/arm64",
		"linux/amd64/glibc", "linux/amd64/musl",
		"linux/arm64/glibc", "linux/arm64/musl",
	}
}

// ValidatePlatforms rejects explicit empty selections and non-exact identifiers.
func ValidatePlatforms(platforms []string) error {
	if platforms == nil {
		return nil
	}
	if len(platforms) == 0 {
		return errors.New("platforms must not be empty")
	}
	supported := SupportedPlatforms()
	for _, platform := range platforms {
		if !slices.Contains(supported, platform) {
			slices.Sort(supported)
			return fmt.Errorf("unknown platform %q; supported platforms: %s", platform, strings.Join(supported, ", "))
		}
	}
	return nil
}

// FilterPlatforms removes unselected binary leaves and invalidates stale hashes.
// Replacing the maps preserves pre-filter history for binaryPath inference.
func (s *State) FilterPlatforms() bool {
	if s.Platforms == nil {
		return false
	}
	changed := false
	for name, entry := range s.Binaries {
		if entry == nil {
			continue
		}
		entryChanged := false
		filtered := make(binmanager.MapOfBinaries)
		for osType, arches := range entry.Binaries {
			for arch, libcs := range arches {
				for libc, info := range libcs {
					platform := string(osType) + "/" + string(arch)
					if osType == syslist.OsTypeLinux {
						platform += "/" + libc
					}
					if !slices.Contains(s.Platforms, platform) || (osType != syslist.OsTypeLinux && libc != "unknown") {
						entryChanged = true
						continue
					}
					if filtered[osType] == nil {
						filtered[osType] = make(map[syslist.ArchType]map[string]binmanager.BinaryOsArchInfo)
					}
					if filtered[osType][arch] == nil {
						filtered[osType][arch] = make(map[string]binmanager.BinaryOsArchInfo)
					}
					filtered[osType][arch][libc] = info
				}
				if len(libcs) == 0 {
					entryChanged = true
				}
			}
			if len(arches) == 0 {
				entryChanged = true
			}
		}
		entry.Binaries = filtered
		if entryChanged {
			changed = true
			if metadata := s.Apps[name]; metadata == nil || entry.ConfigHash != ComputeConfigHash(metadata, s.Platforms) {
				entry.ConfigHash = ""
			}
		}
	}
	return changed
}
