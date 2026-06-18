package cmd

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func chainEntries() []config.ChainHashEntry {
	return []config.ChainHashEntry{
		{FileName: ".gitignore", Hash: "xxh3:1111111111111111aaaaaaaaaaaaaaaa"},
		{FileName: "eslint.config.mjs", Hash: "xxh3:2222222222222222bbbbbbbbbbbbbbbb"},
	}
}

func TestSelectChainHashes(t *testing.T) {
	all := chainEntries()

	t.Run("no args returns everything", func(t *testing.T) {
		got, err := selectChainHashes(all, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("filters to requested files in arg order", func(t *testing.T) {
		got, err := selectChainHashes(all, []string{"eslint.config.mjs", ".gitignore"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0].FileName != "eslint.config.mjs" || got[1].FileName != ".gitignore" {
			t.Fatalf("wrong selection/order: %+v", got)
		}
	})

	t.Run("unknown file errors and lists the unknowns", func(t *testing.T) {
		_, err := selectChainHashes(all, []string{".gitignore", "nope.json"})
		if err == nil {
			t.Fatal("expected an error for unknown file")
		}
		if !strings.Contains(err.Error(), "nope.json") {
			t.Errorf("error should name the unknown file, got %q", err)
		}
	})
}

func TestFormatChainHashTable(t *testing.T) {
	out := formatChainHashTable(chainEntries())

	// Both files and hashes appear, one row each, hashes column-aligned (the
	// shorter ".gitignore" is padded to the width of "eslint.config.mjs").
	for _, want := range []string{
		".gitignore",
		"eslint.config.mjs",
		"xxh3:1111111111111111aaaaaaaaaaaaaaaa",
		"xxh3:2222222222222222bbbbbbbbbbbbbbbb",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n%s", want, out)
		}
	}
	if lines := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; lines != 2 {
		t.Errorf("expected 2 rows, got %d:\n%s", lines, out)
	}
	if !strings.Contains(out, ".gitignore         xxh3:") {
		t.Errorf("expected .gitignore padded to alignment, got:\n%s", out)
	}
}
