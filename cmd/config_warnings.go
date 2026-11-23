package cmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/configcache"
	"github.com/datamitsu/datamitsu/internal/logger"
	"go.uber.org/zap"
)

func configVersionSourceLabel(sourcePath string) string {
	clean := filepath.Clean(sourcePath)
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for i, part := range parts {
		if part != "node_modules" || i+1 >= len(parts) || parts[i+1] == ".pnpm" {
			continue
		}
		packageEnd := i + 2
		if strings.HasPrefix(parts[i+1], "@") && packageEnd < len(parts) {
			packageEnd++
		}
		return "package " + strings.Join(parts[i+1:packageEnd], "/") + " (" + filepath.Base(clean) + ")"
	}
	return "project config " + filepath.Base(clean)
}

func publishConfigVersionNotices(notices []configcache.VersionNotice, current string) {
	if len(notices) == 0 {
		return
	}
	unique := make(map[configcache.VersionNotice]struct{}, len(notices))
	for _, notice := range notices {
		unique[notice] = struct{}{}
	}
	ordered := make([]configcache.VersionNotice, 0, len(unique))
	for notice := range unique {
		ordered = append(ordered, notice)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Source != ordered[j].Source {
			return ordered[i].Source < ordered[j].Source
		}
		if ordered[i].Required != ordered[j].Required {
			return ordered[i].Required < ordered[j].Required
		}
		return ordered[i].Path < ordered[j].Path
	})
	var message strings.Builder
	fmt.Fprintf(&message, "Unstable build: compatibility checks were skipped\n  Current: %s\n  Configuration requirements:", current)
	for _, notice := range ordered {
		fmt.Fprintf(&message, "\n    %s: >= %s", notice.Source, notice.Required)
		logger.Logger.Debug("config minimum-version check skipped for unstable build",
			zap.String("path", notice.Path),
			zap.String("currentVersion", current),
			zap.String("requiredVersion", notice.Required))
	}
	message.WriteString("\n  Use a stable release to verify compatibility.")
	logger.Logger.Warn(message.String())
}
