import { defineConfig } from "tsdown";

// The published action has no node_modules at runtime — GitHub runs the bundle
// directly — so the @actions/* toolkit and its transitive deps are inlined
// (tsdown bundles everything not declared in `dependencies` by default).
export default defineConfig({
  clean: true,
  dts: false,
  entry: { index: "src/main.ts" },
  format: "esm",
  outDir: "dist",
  platform: "node",
});
