package ui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestBannerLayout(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	tests := []struct {
		name   string
		config string
		width  int
		rows   int
	}{
		{"adjacent", "sample", 80, 3},
		{"exact fit", "sample", 38, 3},
		{"stacked", "sample", 37, 6},
		{"long name", strings.Repeat("x", 80), 30, 6},
		{"wide name", "日本語設定", 30, 6},
		{"tiny terminal", "sample", 4, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := bannerLines("datamitsu", "0.5.2", tt.config, "1.0.0", tt.width)
			if len(lines) != tt.rows {
				t.Fatalf("rows = %d, want %d: %q", len(lines), tt.rows, lines)
			}
			if tt.width >= 12 && !strings.Contains(strings.Join(lines, "\n"), "1.0.0") {
				t.Errorf("configuration version lost when fitting banner: %q", lines)
			}
			for _, line := range lines {
				if got := runewidth.StringWidth(line); got > tt.width {
					t.Errorf("line occupies %d columns, limit %d: %q", got, tt.width, line)
				}
			}
			if tt.width >= 5 {
				for start := 0; start < len(lines); start += 3 {
					want := runewidth.StringWidth(lines[start])
					for _, line := range lines[start+1 : start+3] {
						if got := runewidth.StringWidth(line); got != want {
							t.Errorf("box row width = %d, want %d: %q", got, want, line)
						}
					}
				}
			}
		})
	}
}
