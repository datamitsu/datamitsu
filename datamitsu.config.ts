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
  if (facts().env.DATAMITSU_BENCH === "1") {
    return applyBench(config);
  }
  return config;
};
globalThis.getConfig = getConfig;

const getMinVersion = () => "0.0.0";

globalThis.getMinVersion = getMinVersion;
