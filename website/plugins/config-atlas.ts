import type { LoadContext, Plugin } from "@docusaurus/types";

import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

import { themeCSS } from "../../inspector/src/theme";

export default function configAtlas(context: LoadContext): Plugin<string> {
  const templatePath = resolve(context.siteDir, "../internal/inspector/inspector.html");
  const snapshotPath = resolve(context.siteDir, "src/data/reference-config.json");
  const themePath = resolve(context.siteDir, "../internal/inspector/theme.json");
  return {
    getPathsToWatch: () => [templatePath, snapshotPath, themePath],
    // The same palette the inspector is built with, so the orbit in the frame,
    // the poster that precedes it, the runtime bars on the showcase and the
    // page they all sit on read one file. It goes in the head ahead of the
    // theme's own stylesheets, which is where a custom property a page can
    // override belongs.
    injectHtmlTags({ content }) {
      return {
        headTags: [{ innerHTML: content ?? "", tagName: "style" }],
      };
    },
    loadContent() {
      const { placeholder, schemaVersion, themePlaceholder } = JSON.parse(
        readFileSync(resolve(context.siteDir, "../internal/inspector/protocol.json"), "utf8"),
      );
      const template = readFileSync(templatePath, "utf8");
      if (template.split(placeholder).length !== 2) {
        throw new Error("Inspector placeholder must occur once");
      }
      if (template.split(themePlaceholder).length !== 2) {
        throw new Error("Inspector theme placeholder must occur once");
      }
      const capture = JSON.parse(readFileSync(snapshotPath, "utf8"));
      if (capture.manifest.schemaVersion !== schemaVersion) {
        throw new Error("Reference snapshot schema does not match the inspector");
      }
      const { manifest: data, ...provenance } = capture;
      const manifest = JSON.stringify({ ...data, capture: provenance })
        .replaceAll("<", "\\u003c")
        .replaceAll(">", "\\u003e")
        .replaceAll("&", "\\u0026");
      const theme = themeCSS(JSON.parse(readFileSync(themePath, "utf8")));
      writeFileSync(
        resolve(context.siteDir, "static/atlas.html"),
        template.replace(placeholder, () => manifest).replace(themePlaceholder, () => theme),
      );
      return theme;
    },
    name: "config-atlas",
  };
}
