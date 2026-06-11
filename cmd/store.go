package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ocibundle"
	"github.com/datamitsu/datamitsu/internal/ocidigest"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/ui"

	"github.com/spf13/cobra"
)

var storeCmd = &cobra.Command{
	Use:   "store",
	Short: "Manage the global binary and runtime store",
	Long:  `Manage the global binary and runtime store (binaries, runtimes, apps, remote configs).`,
}

var storePathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print store directory path",
	Long:  `Print the absolute path to the global store directory.`,
	RunE:  runStorePath,
}

var storeClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear the entire store",
	Long:  `Remove the entire global store directory including all binaries, runtimes, apps, and remote configs.`,
	RunE:  runStoreClear,
}

var (
	storeSeedApps       []string
	storeSeedResolveTag bool
	storeStatusJSON     bool
	storeImportDigest   string
)

var storeSeedCmd = &cobra.Command{
	Use:   "seed [<ref>@<digest>]",
	Short: "Seed the store from the declared OCI bundle",
	Long: `Pull the OCI bundle into the global store without docker/podman.

Without arguments the bundle is taken from the effective config (the top-level
"oci" key). An explicit "<host>/<owner>/<repo>@sha256:<digest>" argument
overrides the declaration. A bare ":<tag>" reference is refused unless
--resolve-tag is passed (the resolved digest is printed so it can be pinned).

By default the WHOLE bundle is pulled (airgap seeding). With --apps only the
layers of the named tools plus their runtime dependencies are pulled.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStoreSeed(commandContext(cmd), args)
	},
}

var storeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show OCI bundle contents and store coverage",
	Long: `Show what the declared OCI bundle contains for this platform and which of
the configured apps it covers (vs which require the network).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStoreStatus(commandContext(cmd))
	},
}

var storeImportCmd = &cobra.Command{
	Use:   "import <oci-layout-dir>",
	Short: "Seed the store from a local OCI image layout directory",
	Long: `Seed the store from a standard OCI image layout directory (as produced by
"crane pull --format=oci", "oras copy --to-oci-layout", or "skopeo copy") —
fully offline bundle transfer. The bundle digest is taken from the effective
config or from --digest; every blob is verified against the digest chain
exactly like a registry pull.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStoreImport(commandContext(cmd), args[0])
	},
}

func init() {
	storeSeedCmd.Flags().StringSliceVar(&storeSeedApps, "apps", nil,
		"Seed only the layers of these apps and their runtime dependencies (repeatable)")
	storeSeedCmd.Flags().BoolVar(&storeSeedResolveTag, "resolve-tag", false,
		"Allow a tag reference: resolve it to a digest first and print the digest")
	storeStatusCmd.Flags().BoolVar(&storeStatusJSON, "json", false,
		"Emit the status as JSON")
	storeImportCmd.Flags().StringVar(&storeImportDigest, "digest", "",
		"Bundle digest to import (sha256:<64 hex>); defaults to the config's oci.digest")

	storeCmd.AddCommand(storePathCmd)
	storeCmd.AddCommand(storeClearCmd)
	storeCmd.AddCommand(storeSeedCmd)
	storeCmd.AddCommand(storeStatusCmd)
	storeCmd.AddCommand(storeImportCmd)
	rootCmd.AddCommand(storeCmd)
}

// resolveSeedRef determines the bundle reference for store seed: the explicit
// CLI argument (digest-pinned, or tag with --resolve-tag) or the config's oci
// declaration.
func resolveSeedRef(ctx context.Context, cfg *config.Config, args []string) (*config.OCIRef, error) {
	if len(args) == 0 {
		if cfg.OCI == nil {
			return nil, errors.New("no oci bundle declared in the effective config; pass <ref>@<digest> explicitly or set the top-level `oci` key")
		}
		return cfg.OCI, nil
	}

	arg := args[0]
	if ref, digest, ok := strings.Cut(arg, "@"); ok {
		return &config.OCIRef{Ref: ref, Digest: digest}, nil
	}

	ref, tag, ok := strings.Cut(arg, ":")
	if !ok || strings.Contains(tag, "/") {
		return nil, fmt.Errorf("reference %q must be pinned as <ref>@sha256:<digest> (or <ref>:<tag> with --resolve-tag)", arg)
	}
	if !storeSeedResolveTag {
		return nil, fmt.Errorf("a tag reference does not pin content; pass <ref>@sha256:<digest>, or use --resolve-tag to resolve %q and print the digest", arg)
	}
	host, repo, ok := strings.Cut(ref, "/")
	if !ok {
		return nil, fmt.Errorf("reference %q has no repository path", ref)
	}
	digest, err := ocidigest.NewResolverForHost(host).Resolve(ctx, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("resolve tag %q: %w", arg, err)
	}
	fmt.Printf("Resolved %s -> %s\nPin it in the config as oci: { ref: %q, digest: %q }\n", arg, digest, ref, digest)
	return &config.OCIRef{Ref: ref, Digest: digest}, nil
}

func runStoreSeed(ctx context.Context, args []string) error {
	cfg, err := loadConfigForStore(ctx)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	ref, err := resolveSeedRef(ctx, cfg, args)
	if err != nil {
		return err
	}

	opts := ocibundle.Options{}
	if len(storeSeedApps) > 0 {
		opts.Needed = storeSeedApps
	}

	d := ui.New(term.DetectMode())
	restore := ui.Activate(d)
	defer func() {
		d.Close()
		restore()
	}()

	if err := ocibundle.SeedBundle(ctx, cfg, ref, opts); err != nil {
		return err
	}
	fmt.Printf("Seeded store from %s@%s\n", ref.Ref, ref.Digest)
	return nil
}

func runStoreStatus(ctx context.Context) error {
	cfg, err := loadConfigForStore(ctx)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	status, err := ocibundle.BundleStatus(ctx, cfg)
	if err != nil {
		return err
	}

	if storeStatusJSON {
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal status: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("Bundle:  %s@%s\n", status.Ref, status.Digest)
	if status.Signed {
		fmt.Println("Signer:  pinned (verification not yet supported by this build)")
	} else {
		fmt.Println("Signer:  not set (byte integrity via the digest chain only)")
	}
	if len(status.Platforms) > 0 {
		fmt.Printf("Platforms: %s\n", strings.Join(status.Platforms, ", "))
	}
	if status.Selected != "" {
		fmt.Printf("Selected:  %s\n", status.Selected)
	} else if len(status.Platforms) > 0 {
		fmt.Println("Selected:  none (no entry matches this host)")
	}
	if status.Seeded {
		fmt.Println("Fully seeded: yes")
	}
	if len(status.Layers) > 0 {
		fmt.Printf("\nLayers (%d):\n", len(status.Layers))
		for _, layer := range status.Layers {
			mark := " "
			if layer.Present {
				mark = "*"
			}
			label := layer.Subtree
			if layer.App != "" {
				label += " (" + layer.App + ")"
			}
			fmt.Printf("  [%s] %s\n", mark, label)
		}
		fmt.Println("  [*] = already in the store")
	}
	if len(status.Apps) > 0 {
		var uncovered []string
		for _, app := range status.Apps {
			if !app.Covered {
				uncovered = append(uncovered, app.App)
			}
		}
		fmt.Printf("\nApps covered by the bundle: %d/%d\n", len(status.Apps)-len(uncovered), len(status.Apps))
		if len(uncovered) > 0 {
			fmt.Printf("Require the network: %s\n", strings.Join(uncovered, ", "))
		}
	}
	return nil
}

func runStoreImport(ctx context.Context, dir string) error {
	cfg, err := loadConfigForStore(ctx)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	digest := storeImportDigest
	var signer *config.OCISigner
	if digest == "" {
		if cfg.OCI == nil {
			return errors.New("no oci bundle declared in the effective config; pass --digest sha256:<64 hex>")
		}
		digest = cfg.OCI.Digest
		signer = cfg.OCI.Signer
	}

	d := ui.New(term.DetectMode())
	restore := ui.Activate(d)
	defer func() {
		d.Close()
		restore()
	}()

	if err := ocibundle.SeedFromLayout(ctx, cfg, dir, digest, signer, ocibundle.Options{}); err != nil {
		return err
	}
	fmt.Printf("Imported store seed from %s (%s)\n", dir, digest)
	return nil
}

func runStorePath(cmd *cobra.Command, args []string) error {
	fmt.Println(env.GetStorePath())
	return nil
}

func runStoreClear(cmd *cobra.Command, args []string) error {
	storePath := filepath.Clean(env.GetStorePath())
	home, _ := os.UserHomeDir()
	if home != "" {
		home = filepath.Clean(home)
	}
	volume := filepath.VolumeName(storePath)
	sep := string(filepath.Separator)
	if storePath == "" || storePath == "." || storePath == "/" ||
		strings.EqualFold(storePath, home) ||
		!filepath.IsAbs(storePath) ||
		strings.EqualFold(storePath, volume+sep) ||
		(volume != "" && strings.EqualFold(storePath, volume)) ||
		(home != "" && strings.HasPrefix(strings.ToLower(home), strings.ToLower(storePath+sep))) {
		return fmt.Errorf("refusing to clear dangerous path: %s", storePath)
	}
	// Go module cache populates the store with read-only dirs (0555); a plain
	// os.RemoveAll then fails with EACCES. ForceRemoveAll restores write bits first.
	if err := runtimemanager.ForceRemoveAll(storePath); err != nil {
		return fmt.Errorf("failed to clear store: %w", err)
	}
	fmt.Printf("Cleared store: %s\n", storePath)
	return nil
}
