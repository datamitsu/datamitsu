/// <reference path="./.datamitsu/datamitsu.config.d.ts" />

const getBeforeConfigs = () => {
  return [{ path: "./node_modules/@shibanet0/datamitsu-config/datamitsu.config.oci-ghcr.js" }];
};
globalThis.getBeforeConfigs = getBeforeConfigs;

// --- BENCH: datamitsu overhead probe ---
// Injects a trivial shell app + a tool matching every file so we can measure
// (a) per-launch `exec` overhead and (b) `lint`/`fix` planner+discovery overhead
// with ~zero real tool work. Gated on DATAMITSU_BENCH=1, so normal lint/fix/CI
// runs are untouched. Driven by scripts/bench-overhead.sh.
const benchScript =
  'ts=${EPOCHREALTIME:-$(date +%s.%N)}; printf "%s\\n" "$ts"; ' +
  'if [ -n "${DATAMITSU_BENCH_LOG:-}" ]; then printf "%s\\t%s\\n" "$ts" "${1:-}" >>"$DATAMITSU_BENCH_LOG"; fi';

const applyBench = (config: config.Config): config.Config => {
  config.apps ??= {};
  config.apps.tsbench = {
    description: "bench: prints a high-res timestamp, ~zero work (overhead probe)",
    shell: { args: ["-c", benchScript, "datamitsu-tsbench"], name: "bash" },
    versionCheck: { disabled: true },
  };

  config.tools ??= {};
  config.tools.tsbench = {
    name: "tsbench (overhead probe)",
    operations: {
      fix: { app: "tsbench", args: ["{file}"], globs: ["**/*"], scope: "per-file" },
      lint: { app: "tsbench", args: ["{file}"], globs: ["**/*"], scope: "per-file" },
    },
  };

  return config;
};

const getConfig = (config: config.Config) => {
  config.ejectConfigs ??= [];
  for (const tool of ["editorconfig-checker", "gitleaks", "trufflehog"]) {
    if (!config.ejectConfigs.includes(tool)) {
      config.ejectConfigs.push(tool);
    }
  }

  config.apps ??= {};
  config.apps["inspector-types"] = {
    shell: {
      args: [
        "--filter",
        "@datamitsu/inspector",
        "exec",
        "svelte-check",
        "--tsconfig",
        "./tsconfig.json",
        "--fail-on-warnings",
      ],
      name: "pnpm",
    },
    versionCheck: { disabled: true },
  };
  config.tools ??= {};
  config.tools["inspector-types"] = {
    name: "Svelte inspector type and accessibility checks",
    operations: {
      lint: {
        app: "inspector-types",
        args: [],
        globs: ["inspector/**/*.{ts,js,svelte,json}"],
        scope: "repository",
      },
    },
  };
  const upstreamApps = config.apps ?? {};
  const upstreamGolangciLint = upstreamApps["golangci-lint"];
  if (upstreamGolangciLint?.binary) {
    config.apps = {
      ...upstreamApps,
      "golangci-lint": {
        ...upstreamGolangciLint,
        binary: {
          ...upstreamGolangciLint.binary,
          binaries: {
            darwin: {
              amd64: {
                unknown: {
                  binaryPath: "golangci-lint-2.15.0/golangci-lint",
                  contentType: "tar.gz",
                  hash: "sha256:767b899a28285e88ded2823d5dcc4bc5ef681173eb2d80f029a7b17ee4ece3b8",
                  url: "https://github.com/golangci/golangci-lint/releases/download/v2.15.0/golangci-lint-2.15.0-darwin-amd64.tar.gz",
                },
              },
              arm64: {
                unknown: {
                  binaryPath: "golangci-lint-2.15.0/golangci-lint",
                  contentType: "tar.gz",
                  hash: "sha256:d1e409481a8ca846c4bc97e781eb5cb1bd5c23353a0b3977a1cbda1b1b6447d5",
                  url: "https://github.com/golangci/golangci-lint/releases/download/v2.15.0/golangci-lint-2.15.0-darwin-arm64.tar.gz",
                },
              },
            },
            linux: {
              amd64: {
                glibc: {
                  binaryPath: "golangci-lint-2.15.0/golangci-lint",
                  contentType: "tar.gz",
                  hash: "sha256:9b7fe6a6bf6334b98decf8d86b11415ea7889afdabdd1959fdce23af2a9ebcea",
                  url: "https://github.com/golangci/golangci-lint/releases/download/v2.15.0/golangci-lint-2.15.0-linux-amd64.tar.gz",
                },
              },
              arm64: {
                glibc: {
                  binaryPath: "golangci-lint-2.15.0/golangci-lint",
                  contentType: "tar.gz",
                  hash: "sha256:b4d8f17068b35ee6060cc2f69a16fb48740d706fbba81661c318649c114a6998",
                  url: "https://github.com/golangci/golangci-lint/releases/download/v2.15.0/golangci-lint-2.15.0-linux-arm64.tar.gz",
                },
              },
            },
            windows: {
              amd64: {
                unknown: {
                  binaryPath: "golangci-lint-2.15.0/golangci-lint.exe",
                  contentType: "zip",
                  hash: "sha256:cd7ea6063c95de245e17e5246e42bdc14bb221792c429dbd239ab3a6ade6ca6a",
                  url: "https://github.com/golangci/golangci-lint/releases/download/v2.15.0/golangci-lint-2.15.0-windows-amd64.zip",
                },
              },
              arm64: {
                unknown: {
                  binaryPath: "golangci-lint-2.15.0/golangci-lint.exe",
                  contentType: "zip",
                  hash: "sha256:4fb8988abf186158e30af1b8a0fb3671cf2cb02bc91d7e6ce6996f4c1e922e9a",
                  url: "https://github.com/golangci/golangci-lint/releases/download/v2.15.0/golangci-lint-2.15.0-windows-arm64.zip",
                },
              },
            },
          },
          version: "v2.15.0",
        },
      },
    };
  }

  if (facts().env.DATAMITSU_BENCH === "1") {
    return applyBench(config);
  }
  return config;
};
globalThis.getConfig = getConfig;

const getMinVersion = () => "0.0.0";

globalThis.getMinVersion = getMinVersion;
