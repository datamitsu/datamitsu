import { defineConfig } from "tsdown";

export default defineConfig({
  banner: {
    js: ["// @ts-nocheck", "// prettier-ignore", "/* eslint-disable */"].join("\n"),
  },
  entry: ["src/main.ts"],
  format: "esm",
  outDir: "dist",
});
