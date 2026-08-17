package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"

	"github.com/spf13/cobra"
)

var (
	storeRefsJSON    bool
	storeRefsOCIOnly bool
)

var storeRefsCmd = &cobra.Command{
	Use:   "refs",
	Short: "List every external artifact the effective config pins",
	Long: `List every artifact this config fetches from outside the machine: OCI
references (the bundle and any registry-sourced parser modules) and, unless
--oci-only is passed, the hash-pinned https downloads.

It answers "what do I have to mirror?" without touching the network, so it runs
inside a firewall. Output is deterministic and sorted, which makes it usable as
input to a mirroring loop:

  datamitsu store refs --oci-only | xargs -n1 -I{} crane copy {} harbor.corp/dm/{}

Runtime archives (node, uv, the JDK) are resolved from a generated manifest at
install time rather than declared in the config, so they are not listed here.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStoreRefs(commandContext(cmd))
	},
}

// storeRefEntry is one external artifact. An OCI entry carries ref+digest, an
// https entry url+hash; the JSON shape keeps them in separate arrays so a
// consumer never has to sniff which kind it is holding.
type storeRefEntry struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Ref      string `json:"ref,omitempty"`
	Digest   string `json:"digest,omitempty"`
	URL      string `json:"url,omitempty"`
	Hash     string `json:"hash,omitempty"`
	Platform string `json:"platform,omitempty"`
}

type storeRefs struct {
	OCI   []storeRefEntry `json:"oci"`
	HTTPS []storeRefEntry `json:"https,omitempty"`
}

func runStoreRefs(ctx context.Context) error {
	cfg, err := loadConfigForStore(ctx)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	refs := collectStoreRefs(cfg)
	if storeRefsOCIOnly {
		refs.HTTPS = nil
	}

	if storeRefsJSON {
		data, err := json.MarshalIndent(refs, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal refs: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	for _, e := range refs.OCI {
		fmt.Printf("%s@%s\n", e.Ref, e.Digest)
	}
	for _, e := range refs.HTTPS {
		fmt.Println(e.URL)
	}
	return nil
}

// collectStoreRefs walks the effective config for everything fetched from
// outside the machine. Every https entry carries its mandatory hash, because
// the list exists to answer "what is still outside my registry, and how would I
// verify it" — a URL on its own answers only half of that.
func collectStoreRefs(cfg *config.Config) storeRefs {
	var refs storeRefs

	if cfg.OCI != nil {
		refs.OCI = append(refs.OCI, storeRefEntry{
			Kind: "bundle", Name: "oci", Ref: cfg.OCI.Ref, Digest: cfg.OCI.Digest,
		})
	}

	for _, name := range sortedKeys(cfg.Parsers) {
		p := cfg.Parsers[name]
		switch {
		case p.OCI != nil:
			refs.OCI = append(refs.OCI, storeRefEntry{
				Kind: "parser", Name: name, Ref: p.OCI.Ref, Digest: p.OCI.Digest,
			})
		case p.URL != "":
			refs.HTTPS = append(refs.HTTPS, storeRefEntry{
				Kind: "parser", Name: name, URL: p.URL, Hash: p.Hash,
			})
		}
	}

	for _, name := range sortedKeys(cfg.Apps) {
		app := cfg.Apps[name]
		if app.Binary != nil {
			refs.HTTPS = append(refs.HTTPS, binaryEntries("app-binary", name, app.Binary.Binaries)...)
		}
		refs.HTTPS = append(refs.HTTPS, archiveEntries("app-archive", name, app.Archives)...)
	}

	for _, name := range sortedKeys(cfg.Bundles) {
		if b := cfg.Bundles[name]; b != nil {
			refs.HTTPS = append(refs.HTTPS, archiveEntries("bundle-archive", name, b.Archives)...)
		}
	}

	for _, name := range sortedKeys(cfg.Runtimes) {
		if rt := cfg.Runtimes[name]; rt.Managed != nil {
			refs.HTTPS = append(refs.HTTPS, binaryEntries("runtime-binary", name, rt.Managed.Binaries)...)
		}
	}

	sortRefEntries(refs.OCI)
	sortRefEntries(refs.HTTPS)
	return refs
}

// binaryEntries flattens a per-platform binary map. The platform is reported as
// os/arch[/libc] so a mirror operator can see which entries matter for their
// target hosts instead of mirroring all of them blindly.
func binaryEntries(kind, name string, binaries binmanager.MapOfBinaries) []storeRefEntry {
	var out []storeRefEntry
	for _, osName := range sortedKeys(binaries) {
		byArch := binaries[osName]
		for _, arch := range sortedKeys(byArch) {
			byLibc := byArch[arch]
			for _, libc := range sortedKeys(byLibc) {
				info := byLibc[libc]
				if info.URL == "" {
					continue
				}
				platform := fmt.Sprintf("%s/%s", osName, arch)
				if libc != "" {
					platform += "/" + libc
				}
				out = append(out, storeRefEntry{
					Kind: kind, Name: name, URL: info.URL, Hash: info.Hash, Platform: platform,
				})
			}
		}
	}
	return out
}

// archiveEntries lists the externally-fetched archives of an app or bundle.
// Inline archives are embedded in the config itself and need no mirror.
func archiveEntries(kind, name string, archives map[string]*binmanager.ArchiveSpec) []storeRefEntry {
	var out []storeRefEntry
	for _, archiveName := range sortedKeys(archives) {
		spec := archives[archiveName]
		if spec == nil || spec.URL == "" {
			continue
		}
		out = append(out, storeRefEntry{
			Kind: kind, Name: name + ":" + archiveName, URL: spec.URL, Hash: spec.Hash,
		})
	}
	return out
}

// sortRefEntries orders entries by their address first, so identical artifacts
// declared by several entities land next to each other, then by kind and name
// to break ties deterministically.
func sortRefEntries(entries []storeRefEntry) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		switch {
		case a.Ref != b.Ref:
			return a.Ref < b.Ref
		case a.URL != b.URL:
			return a.URL < b.URL
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		case a.Name != b.Name:
			return a.Name < b.Name
		default:
			return a.Platform < b.Platform
		}
	})
}

// sortedKeys returns a map's keys in sorted order, for deterministic output.
func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
