import { defineConfig } from "tsdown";

// The VS Code extension host loads the extension via require(), so the bundle is
// CommonJS. The `vscode` module is provided by the host at runtime and must NOT
// be bundled; everything else (vscode-languageclient and its deps) is inlined so
// the published .vsix needs no node_modules.
export default defineConfig({
  clean: true,
  // Keep only the host-provided `vscode` module external; everything else
  // (vscode-languageclient and its deps, which live in devDependencies) is bundled
  // so the published .vsix needs no node_modules.
  deps: { neverBundle: ["vscode"] },
  dts: false,
  entry: { extension: "src/extension.ts" },
  format: "cjs",
  outDir: "dist",
  platform: "node",
});
