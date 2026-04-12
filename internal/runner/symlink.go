package runner

import (
	"io/fs"
	"os"
)

func filterSymlinkPaths(paths []string) []string {
	var result []string
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			result = append(result, p)
			continue
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		result = append(result, p)
	}
	return result
}
