package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"sort"
	"testing"
)

func TestCollectBundleEntries(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"agent-skills": {Version: "1.0", Files: map[string]string{"agents.md": "content"}},
		"docs":         {Version: "2.0", Files: map[string]string{"readme.md": "content"}},
	}

	entries := collectBundleEntries(bundles)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("entries not sorted: %v", names)
	}
}

func TestCollectBundleEntriesEmpty(t *testing.T) {
	entries := collectBundleEntries(binmanager.MapOfBundles{})
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestCollectBundleEntriesVersions(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"with-version":    {Version: "3.0", Files: map[string]string{"a.txt": "a"}},
		"without-version": {Files: map[string]string{"b.txt": "b"}},
	}

	entries := collectBundleEntries(bundles)

	versions := map[string]string{}
	for _, e := range entries {
		versions[e.name] = e.version
	}
	if versions["with-version"] != "3.0" {
		t.Errorf("expected version '3.0', got %q", versions["with-version"])
	}
	if versions["without-version"] != "" {
		t.Errorf("expected empty version, got %q", versions["without-version"])
	}
}

func TestCollectBundleEntriesNilBundle(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"nil-bundle": nil,
	}

	entries := collectBundleEntries(bundles)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].version != "" {
		t.Errorf("expected empty version for nil bundle, got %q", entries[0].version)
	}
}

func TestBundlesListOutput(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"agent-skills": {Version: "1.0", Files: map[string]string{"agents.md": "content"}},
		"docs":         {Version: "2.0", Files: map[string]string{"readme.md": "content"}},
		"no-version":   {Files: map[string]string{"a.txt": "a"}},
	}

	entries := collectBundleEntries(bundles)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	versions := map[string]string{}
	for _, e := range entries {
		versions[e.name] = e.version
	}
	if versions["agent-skills"] != "1.0" {
		t.Errorf("expected agent-skills version '1.0', got %q", versions["agent-skills"])
	}
	if versions["docs"] != "2.0" {
		t.Errorf("expected docs version '2.0', got %q", versions["docs"])
	}
	if versions["no-version"] != "" {
		t.Errorf("expected no-version to be empty, got %q", versions["no-version"])
	}
}

func TestBundlesListSorting(t *testing.T) {
	bundles := binmanager.MapOfBundles{
		"zzz-bundle": {Version: "1.0", Files: map[string]string{"a.txt": "a"}},
		"aaa-bundle": {Version: "2.0", Files: map[string]string{"b.txt": "b"}},
		"mmm-bundle": {Version: "3.0", Files: map[string]string{"c.txt": "c"}},
	}

	entries := collectBundleEntries(bundles)
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}

	if !sort.StringsAreSorted(names) {
		t.Errorf("entries not sorted: %v", names)
	}
}
