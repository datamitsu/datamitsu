import { svelte } from "@sveltejs/vite-plugin-svelte";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";

import protocol from "../internal/inspector/protocol.json" with { type: "json" };
import defaultTheme from "../internal/inspector/theme.json" with { type: "json" };
import { themeCSS } from "./src/theme.ts";

function singleHTML(): Plugin {
  return {
    enforce: "post",
    generateBundle(this, _, bundle) {
      const files = Object.values(bundle);
      const scripts = files.filter((file) => file.type === "chunk");
      if (
        scripts.length !== 1 ||
        scripts.some((file) => file.imports.length > 0 || file.dynamicImports.length > 0)
      ) {
        throw new Error("Inspector must be a single script without external imports");
      }
      const css = files
        .flatMap((file) =>
          file.type === "asset" && file.fileName.endsWith(".css") ? [String(file.source)] : [],
        )
        .join("");
      if (files.some((file) => file.type === "asset" && !file.fileName.endsWith(".css"))) {
        throw new Error("Inspector assets must be inlined");
      }
      if (!css) {
        throw new Error("Inspector stylesheet is missing");
      }
      const script = scripts[0]!.code.replaceAll(/<\/script/gi, "<\\/script");
      const template = readFileSync(new URL("index.html", import.meta.url), "utf8");
      const html = template
        .replace("</head>", () => `<style>${css}</style></head>`)
        .replace(
          '<script type="module" src="/src/main.ts"></script>',
          () => `<script>${script}</script>`,
        )
        .trim();
      if (html.split(protocol.placeholder).length !== 2) {
        throw new Error("Manifest placeholder must occur exactly once");
      }
      if (html.split(protocol.themePlaceholder).length !== 2) {
        throw new Error("Theme placeholder must occur exactly once");
      }
      for (const key of Object.keys(bundle)) {
        delete bundle[key];
      }
      this.emitFile({ fileName: "inspector.html", source: `${html}\n`, type: "asset" });
    },
    name: "inspector-single-html",
    transformIndexHtml(html, context) {
      if (!context.server) {
        return html;
      }
      return html
        .replace(protocol.placeholder, () =>
          JSON.stringify({
            apps: [],
            managedConfigs: [],
            name: "datamitsu.config",
            projectTypes: [],
            schemaVersion: protocol.schemaVersion,
            tools: [],
            version: "development",
          }),
        )
        .replace(protocol.themePlaceholder, () => themeCSS(defaultTheme));
    },
  };
}

export default defineConfig({
  build: {
    cssCodeSplit: false,
    cssMinify: true,
    emptyOutDir: false,
    lib: { entry: "src/main.ts", formats: ["iife"], name: "DatamitsuInspector" },
    minify: true,
    outDir: fileURLToPath(new URL("../internal/inspector", import.meta.url)),
    sourcemap: false,
  },
  plugins: [svelte(), singleHTML()],
});
