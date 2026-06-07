package dockerfile

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func samplePlan() Plan {
	return Plan{
		RuntimeStages: []RuntimeStage{
			{Name: "node", Kind: config.RuntimeKindNode},
			{Name: "uv", Kind: config.RuntimeKindUV},
			{Name: "go", Kind: config.RuntimeKindGo},
		},
		RuntimeAppStages: []RuntimeAppStage{
			{App: "prettier", Kind: config.RuntimeKindNode, Runtime: "node"},
			{App: "ruff", Kind: config.RuntimeKindUV, Runtime: "uv"},
			{App: "golangci", Kind: config.RuntimeKindGo, Runtime: "go"},
		},
		BinaryStages: []BinaryStage{{App: "shellcheck"}},
	}
}

func pinnedOpts() RenderOptions {
	opts := DefaultRenderOptions()
	opts.BaseImage = "ghcr.io/datamitsu/datamitsu:0.0.19"
	opts.Digest = "sha256:" + strings.Repeat("a", 64)
	return opts
}

func TestRender_PinnedStructure(t *testing.T) {
	out := Render(samplePlan(), pinnedOpts())

	mustContain(t, out, "# syntax=docker/dockerfile:1")
	mustContain(t, out, "FROM ghcr.io/datamitsu/datamitsu:0.0.19@sha256:"+strings.Repeat("a", 64)+" AS dm-base")
	mustContain(t, out, "ENV DATAMITSU_CACHE_DIR=/dm")

	// Hierarchical stages.
	mustContain(t, out, "FROM dm-base AS rt-node")
	mustContain(t, out, "FROM rt-node AS app-prettier")
	mustContain(t, out, "FROM rt-uv AS app-ruff")
	mustContain(t, out, "FROM dm-base AS app-shellcheck")
	mustContain(t, out, "datamitsu --config /opt/datamitsu-config/datamitsu.config.js install --runtime node")
	mustContain(t, out, "datamitsu --config /opt/datamitsu-config/datamitsu.config.js install prettier")

	// Final COPY --link assembly.
	mustContain(t, out, "COPY --link --from=rt-node /dm/store/.runtimes/node /dm/store/.runtimes/node")
	mustContain(t, out, "COPY --link --from=app-prettier /dm/store/.apps/node/prettier /dm/store/.apps/node/prettier")
	mustContain(t, out, "COPY --link --from=app-ruff /dm/store/.apps/uv/ruff /dm/store/.apps/uv/ruff")
	mustContain(t, out, "COPY --link --from=app-ruff /dm/store/.uv/python /dm/store/.uv/python")
	mustContain(t, out, "COPY --link --from=app-shellcheck /dm/store/.bin/shellcheck /dm/store/.bin/shellcheck")

	// Go SDK is build-only: a runtime stage exists, but no runtime COPY into final.
	mustContain(t, out, "FROM dm-base AS rt-go")
	if strings.Contains(out, "COPY --link --from=rt-go") {
		t.Error("go runtime subtree must NOT be copied to the final image")
	}

	mustContain(t, out, `ENTRYPOINT ["/usr/local/bin/datamitsu","--before-config","/opt/datamitsu-config/datamitsu.config.js"]`)
	mustContain(t, out, `CMD ["--help"]`)
}

func TestRender_ConfigSplitLayering(t *testing.T) {
	out := Render(samplePlan(), pinnedOpts())

	// The base stage must be config-free: nothing FROM it should be invalidated
	// by a config edit. The config enters only config-split and final. It also
	// must not `git init` — slice stages use --no-auto-config, so a git root is
	// never needed (and COPY --link would make the workdir root-owned, which git
	// rejects).
	baseBlock := out[strings.Index(out, "AS dm-base"):strings.Index(out, "AS config-split")]
	if strings.Contains(baseBlock, "datamitsu.config.js") {
		t.Errorf("base stage must not COPY the config (it would bust every stage on edit):\n%s", baseBlock)
	}
	if strings.Contains(baseBlock, "git init") {
		t.Errorf("base stage must not git init (COPY --link makes the workdir root-owned, which git rejects):\n%s", baseBlock)
	}

	// The config-split stage reads the full config (which may read facts), so it
	// gets its own git init; its owner-correct workdir keeps git happy.
	mustContain(t, out, "FROM dm-base AS config-split")
	mustContain(t, out, "COPY --chown=datamitsu:datamitsu datamitsu.config.js ./")
	splitBlock := out[strings.Index(out, "AS config-split"):strings.Index(out, "AS rt-")]
	mustContain(t, splitBlock, "RUN git init -q .")
	mustContain(t, out, "RUN datamitsu --config /opt/datamitsu-config/datamitsu.config.js devtools split-config --output /slices")

	// Every builder stage COPYs only its own slice from config-split, landing it
	// at the path install reads.
	mustContain(t, out, "COPY --link --from=config-split /slices/rt-node.js /opt/datamitsu-config/datamitsu.config.js")
	mustContain(t, out, "COPY --link --from=config-split /slices/app-prettier.js /opt/datamitsu-config/datamitsu.config.js")
	mustContain(t, out, "COPY --link --from=config-split /slices/app-shellcheck.js /opt/datamitsu-config/datamitsu.config.js")

	// The final image carries the full config for the entrypoint.
	finalIdx := strings.Index(out, "AS final")
	if finalIdx < 0 {
		t.Fatal("no final stage in output")
	}
	mustContain(t, out[finalIdx:], "COPY --chown=datamitsu:datamitsu datamitsu.config.js ./")
}

func TestRender_UnpinnedEmitsWarning(t *testing.T) {
	opts := DefaultRenderOptions()
	opts.BaseImage = "ghcr.io/datamitsu/datamitsu:dev"
	opts.Digest = ""
	opts.UnpinnedReason = "offline mode"

	out := Render(samplePlan(), opts)
	mustContain(t, out, "# WARNING: base image ghcr.io/datamitsu/datamitsu:dev is NOT pinned by digest (offline mode).")
	mustContain(t, out, "FROM ghcr.io/datamitsu/datamitsu:dev AS dm-base")
	if strings.Contains(out, "@sha256:") {
		t.Error("unpinned render must not contain a digest")
	}
}

func TestRender_VerifiesByDefault(t *testing.T) {
	// `install` verifies by default, so the default render emits plain install
	// commands (no --verify, no --no-verify).
	out := Render(samplePlan(), pinnedOpts())
	mustContain(t, out, "install prettier\n")
	mustContain(t, out, "install shellcheck\n")
	if strings.Contains(out, "--no-verify") {
		t.Error("default render must not include --no-verify")
	}
}

func TestRender_NoVerifyAppendsFlag(t *testing.T) {
	opts := pinnedOpts()
	opts.NoVerify = true
	out := Render(samplePlan(), opts)

	// App and binary install commands opt out ...
	mustContain(t, out, "install prettier --no-verify")
	mustContain(t, out, "install shellcheck --no-verify")
	// ... but runtime stages are unaffected (a runtime has no app version check).
	if strings.Contains(out, "install --runtime node --no-verify") {
		t.Error("runtime install must not get --no-verify")
	}
}

func TestRender_Deterministic(t *testing.T) {
	a := Render(samplePlan(), pinnedOpts())
	b := Render(samplePlan(), pinnedOpts())
	if a != b {
		t.Error("Render is not deterministic")
	}
}

func TestRender_Labels(t *testing.T) {
	opts := pinnedOpts()
	opts.Labels = map[string]string{
		"org.opencontainers.image.title":  "datamitsu-config",
		"org.opencontainers.image.source": "https://github.com/shibanet0/datamitsu-config",
	}
	out := Render(samplePlan(), opts)
	mustContain(t, out, `LABEL org.opencontainers.image.source="https://github.com/shibanet0/datamitsu-config"`)
	mustContain(t, out, `LABEL org.opencontainers.image.title="datamitsu-config"`)
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("output missing expected line:\n  %s\n--- full output ---\n%s", needle, haystack)
	}
}
