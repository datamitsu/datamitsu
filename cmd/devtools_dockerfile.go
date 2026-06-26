package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/dockerfile"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/ocidigest"
	"github.com/datamitsu/datamitsu/internal/target"
	"github.com/datamitsu/datamitsu/internal/utils"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

// defaultBaseRepo is the GHCR repository of the datamitsu base image that
// generated wrapper Dockerfiles build FROM.
const defaultBaseRepo = "datamitsu/datamitsu"

var (
	dockerfileOutput       string
	dockerfileAlpine       bool
	dockerfileOffline      bool
	dockerfileNoVerify     bool
	dockerfileConfigJS     string
	dockerfileRepo         string
	dockerfileLabels       []string
	dockerfileArgs         []string
	dockerfileBuildArgs    []string
	dockerfileEnv          []string
	dockerfileForceInclude []string
	dockerfileEmitOCIMap   string
)

// digestResolver is the seam over ocidigest.Resolver so the command can be
// tested without network access.
type digestResolver interface {
	ResolveCached(ctx context.Context, repo, tag string) (string, error)
}

// newDigestResolver builds the production resolver; overridden in tests.
var newDigestResolver = func() digestResolver { return ocidigest.NewResolver() }

var dockerfileCmd = &cobra.Command{
	Use:   "dockerfile",
	Short: "Generate an optimized, digest-pinned multi-stage Dockerfile from the config",
	Long: `Generate a multi-stage Dockerfile that pre-installs every app in the loaded
config, one cacheable layer per binary, per runtime, and per runtime-managed app.

The base image is ` + defaultBaseRepo + ` at the version of the running datamitsu
binary, pinned by digest when it can be resolved (best-effort: --offline, an
unreachable registry, or a non-release build leave the FROM line unpinned with a
warning). The output file is fully overwritten on each run — it is a generated
artifact you own and may hand-edit, but hand-edits are lost on regeneration.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runDockerfile(commandContext(cmd), cmd)
	},
}

func init() {
	dockerfileCmd.Flags().StringVarP(&dockerfileOutput, "output", "o", "", "Output Dockerfile path including filename (required, overwritten)")
	dockerfileCmd.Flags().BoolVar(&dockerfileAlpine, "alpine", false, "Target the Alpine (musl) base image variant")
	dockerfileCmd.Flags().BoolVar(&dockerfileOffline, "offline", false, "Skip base-image digest resolution (emit an unpinned FROM with a warning)")
	dockerfileCmd.Flags().BoolVar(&dockerfileNoVerify, "no-verify", false, "Do not run apps' version checks in their build stages (verification is on by default)")
	dockerfileCmd.Flags().StringVar(&dockerfileConfigJS, "config-js", "datamitsu.config.js", "Pre-built config file COPYed into the image")
	dockerfileCmd.Flags().StringVar(&dockerfileRepo, "repo", "", "Override the base image repository (default: the repo this datamitsu build was published under)")
	dockerfileCmd.Flags().StringArrayVar(&dockerfileLabels, "label", nil, "OCI label key=value for the final image (repeatable)")
	dockerfileCmd.Flags().StringArrayVar(&dockerfileArgs, "arg", nil, "Build ARG (name or name=default) declared before ENV in the final stage (repeatable)")
	dockerfileCmd.Flags().StringArrayVar(&dockerfileBuildArgs, "build-arg", nil, "Build-time ARG promoted to ENV so install stages inherit it, e.g. DATAMITSU_INSTALL_TIMEOUT=1200; not baked into the final image (repeatable)")
	dockerfileCmd.Flags().StringArrayVar(&dockerfileEnv, "env", nil, "ENV key=value baked into the final image (repeatable)")
	dockerfileCmd.Flags().StringSliceVar(&dockerfileForceInclude, "force-include", nil, "Binary app names to include even if they lack a binary for the target libc (comma-separated, repeatable)")
	dockerfileCmd.Flags().StringVar(&dockerfileEmitOCIMap, "emit-oci-map", "", "Also write the layer→subtree map JSON (for the OCI bundle annotation post-process) to this path")
	_ = dockerfileCmd.MarkFlagRequired("output")
	devtoolsCmd.AddCommand(dockerfileCmd)
}

func runDockerfile(ctx context.Context, cmd *cobra.Command) error {
	cfg, _, _, err := loadConfigWithPaths(ctx, BeforeConfigPaths, NoAutoConfig, ConfigPaths)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	labels, err := parseLabels(dockerfileLabels)
	if err != nil {
		return err
	}

	finalArgs, err := parseArgs("arg", dockerfileArgs)
	if err != nil {
		return err
	}

	buildArgs, err := parseArgs("build-arg", dockerfileBuildArgs)
	if err != nil {
		return err
	}

	envVars, err := parseEnv(dockerfileEnv)
	if err != nil {
		return err
	}

	// The image targets musl on Alpine and glibc otherwise; binary apps without a
	// binary for that libc are dropped (a glibc binary can't exec on musl), unless
	// named in --force-include (for statically-linked tools the registry
	// under-declares).
	targetLibc := string(target.LibcGlibc)
	if dockerfileAlpine {
		targetLibc = string(target.LibcMusl)
	}
	forceInclude := make(map[string]bool, len(dockerfileForceInclude))
	for _, name := range dockerfileForceInclude {
		forceInclude[name] = true
		if _, ok := cfg.Apps[name]; !ok {
			fmt.Fprintf(os.Stderr, "Warning: --force-include %q is not a known app\n", name)
		}
	}

	plan := dockerfile.BuildPlan(cfg.Apps, cfg.Runtimes, dockerfile.PlanOptions{
		TargetLibc:   targetLibc,
		ForceInclude: forceInclude,
		Parsers:      cfg.Parsers,
	})
	if len(plan.LibcExcluded) > 0 {
		fmt.Fprintf(os.Stderr, "Warning: excluded %d app(s) with no %s binary (add via --force-include if universal): %s\n",
			len(plan.LibcExcluded), targetLibc, strings.Join(plan.LibcExcluded, ", "))
	}

	// The repository and tag of the datamitsu base image are baked into this
	// binary at release time (they differ between the stable and unstable
	// channels), so the generated FROM points at the image this build actually
	// came from rather than guessing it from the version string. --repo overrides
	// the repo for mirrors/forks; the registry host comes from DATAMITSU_OCI_REGISTRY.
	repo := resolveImageRepo(dockerfileRepo, ldflags.ImageRepo)
	tag := resolveImageTag(ldflags.ImageTag, ldflags.Version, dockerfileAlpine)
	registry := env.GetOCIRegistry()
	baseImage := fmt.Sprintf("%s/%s:%s", registry, repo, tag)

	digest, unpinnedReason := resolveBaseDigest(ctx, newDigestResolver(), dockerfileOffline, ldflags.Version, repo, tag)
	if digest == "" {
		fmt.Fprintf(os.Stderr, "Warning: %s will be UNPINNED (%s)\n", baseImage, unpinnedReason)
	}

	opts := dockerfile.DefaultRenderOptions()
	opts.BaseImage = baseImage
	opts.Digest = digest
	opts.UnpinnedReason = unpinnedReason
	opts.ConfigSource = dockerfileConfigJS
	opts.Labels = labels
	opts.Args = finalArgs
	opts.BuildArgs = buildArgs
	opts.Env = envVars
	opts.NoVerify = dockerfileNoVerify

	text := dockerfile.Render(plan, opts)
	if err := writeFileAtomic(dockerfileOutput, []byte(text)); err != nil {
		return fmt.Errorf("failed to write %s: %w", dockerfileOutput, err)
	}

	if dockerfileEmitOCIMap != "" {
		mapData, err := dockerfile.MarshalOCIMap(dockerfile.BuildOCIMap(plan, opts))
		if err != nil {
			return fmt.Errorf("failed to marshal oci map: %w", err)
		}
		if err := writeFileAtomic(dockerfileEmitOCIMap, mapData); err != nil {
			return fmt.Errorf("failed to write %s: %w", dockerfileEmitOCIMap, err)
		}
	}

	printDockerfileSummary(cmd, plan, digest, unpinnedReason)
	return nil
}

// resolveBaseDigest returns the base-image digest, or an empty digest plus a
// human-readable reason. It never errors: pinning is best-effort, so every
// non-success path degrades to an unpinned FROM.
func resolveBaseDigest(ctx context.Context, resolver digestResolver, offline bool, version, repo, tag string) (digest, unpinnedReason string) {
	if offline {
		return "", "offline mode (--offline)"
	}
	if !isPinnableVersion(version) {
		return "", fmt.Sprintf("datamitsu version %q is not a release tag", version)
	}
	d, err := resolver.ResolveCached(ctx, repo, tag)
	if err != nil {
		return "", fmt.Sprintf("digest resolution failed: %v", err)
	}
	return d, ""
}

// isPinnableVersion reports whether version is a concrete release tag (immutable,
// worth pinning). Dev builds and prerelease/unstable versions use mutable image
// tags, so they are left unpinned.
func isPinnableVersion(version string) bool {
	normalized := version
	if !strings.HasPrefix(normalized, "v") {
		normalized = "v" + normalized
	}
	return semver.IsValid(normalized) && semver.Prerelease(normalized) == ""
}

// resolveImageRepo picks the base image repository: an explicit --repo flag wins,
// then the repo baked into the binary at release time, then the stable default
// (covering local builds that set neither).
func resolveImageRepo(repoFlag, ldflagsRepo string) string {
	if repoFlag != "" {
		return repoFlag
	}
	if ldflagsRepo != "" {
		return ldflagsRepo
	}
	return defaultBaseRepo
}

// resolveImageTag picks the base image tag: the exact tag baked in at release
// time, falling back to the version for local builds (where ImageTag is empty),
// with the Alpine suffix appended for the musl variant.
func resolveImageTag(ldflagsTag, version string, alpine bool) string {
	tag := ldflagsTag
	if tag == "" {
		tag = version
	}
	if alpine {
		tag += "-alpine"
	}
	return tag
}

// parseLabels turns repeated key=value flags into a map (empty when no flags).
func parseLabels(pairs []string) (map[string]string, error) {
	return parseKeyValues("label", pairs)
}

// parseEnv turns repeated key=value flags into a map (empty when no flags).
func parseEnv(pairs []string) (map[string]string, error) {
	return parseKeyValues("env", pairs)
}

// parseArgs turns repeated --arg/--build-arg flags into a map. Unlike labels/env,
// a bare name (no '=') is allowed: it declares an ARG whose default comes from
// `docker build --build-arg` at build time (value stored as ""). flag names the
// originating flag for error messages.
func parseArgs(flag string, pairs []string) (map[string]string, error) {
	m := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, _ := strings.Cut(pair, "=")
		if key == "" {
			return nil, fmt.Errorf("invalid --%s %q: want name or name=value", flag, pair)
		}
		m[key] = value
	}
	return m, nil
}

// parseKeyValues parses repeated --<flag> key=value pairs into a map; the value
// may itself contain '=' (only the first is the separator).
func parseKeyValues(flag string, pairs []string) (map[string]string, error) {
	m := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid --%s %q: want key=value", flag, pair)
		}
		m[key] = value
	}
	return m, nil
}

// writeFileAtomic writes data to path via a temp file and rename, fully
// overwriting any existing file (regeneration semantics).
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	tmpFile, err := os.CreateTemp(dir, ".dockerfile-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := utils.RenameReplace(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

func printDockerfileSummary(cmd *cobra.Command, plan dockerfile.Plan, digest, unpinnedReason string) {
	out := cmd.OutOrStdout()
	stages := len(plan.RuntimeStages) + len(plan.RuntimeAppStages) + len(plan.BinaryStages)
	pinned := "pinned by digest"
	if digest == "" {
		pinned = "UNPINNED (" + unpinnedReason + ")"
	}
	_, _ = fmt.Fprintf(out, "Generated %s: %d build stages, base image %s\n", dockerfileOutput, stages, pinned)
	if len(plan.Skipped) > 0 {
		_, _ = fmt.Fprintf(out, "Skipped (no install footprint): %s\n", strings.Join(plan.Skipped, ", "))
	}
}
