import { defaultView, type Manifest, readRoute, type Route, routeHash } from "./model.ts";
import { matchingTools, runtimeCounts } from "./selection.ts";

export const embedSizes = {
  blueprints: { minHeight: 180, ratio: "6 / 1" },
  operations: { minHeight: 420, ratio: "4 / 3" },
  universe: { minHeight: 420, ratio: "4 / 3" },
};
export type EmbedPreset = "" | "minimal";
export type ThemePreference = "auto" | "dark" | "light";

// A host page with its own theme toggle keeps the frame in step by posting the
// request; the frame answers with the ack so the host knows it need not reload.
export const themeMessage = {
  ack: "datamitsu:theme-ack",
  request: "datamitsu:theme",
} as const;

// The snippet is the frame and nothing else: numbers printed beside it would be
// a second copy of what the frame shows, and would disagree with it the first
// time the configuration changes.
export function embedCode(
  href: string,
  route: Route,
  theme: ThemePreference,
  preset: EmbedPreset = "",
): string {
  const size = embedSizes[route.view as keyof typeof embedSizes] ?? embedSizes.operations;
  const isMinimal = preset === "minimal" && route.view === "universe";
  const sandbox = isMinimal
    ? "allow-scripts allow-popups allow-popups-to-escape-sandbox"
    : "allow-scripts";
  return `<iframe src="${escapeHTML(viewURL(href, route, theme, true, preset))}"\n  loading="lazy" sandbox="${sandbox}" referrerpolicy="no-referrer"\n  title="datamitsu ${route.view} — configuration snapshot"\n  style="width:100%;aspect-ratio:${size.ratio};min-height:${size.minHeight}px;border:0;color-scheme:inherit"></iframe>`;
}

export function embedPreset(search: string): EmbedPreset {
  return new URLSearchParams(search).get("preset") === "minimal" ? "minimal" : "";
}

export function fallbackText(data: Manifest, route: Route): string {
  if (route.view === "operations") {
    return matchingTools(data, route)
      .filter((tool) => !route.tool || tool.id === route.tool)
      .map(
        (tool) =>
          `${tool.id}: ${tool.skipped ? `skipped${tool.skipReason ? ` (${tool.skipReason})` : ""}` : "enabled"}; ${tool.operations.map((op) => `${op.kind} via ${op.app}, ${op.scope}, priority ${op.priority}, patterns ${op.globs.join(", ") || "all"}${op.excludeGlobs.length > 0 ? `, excluding ${op.excludeGlobs.join(", ")}` : ""}`).join("; ")}`,
      )
      .join(". ");
  }
  return runtimeCounts(data, route)
    .map((group) => `${group.label}: ${group.count}`)
    .join(" · ");
}

const themeValues: ThemePreference[] = ["auto", "dark", "light"];

export function readThemeMessage(data: unknown): "" | ThemePreference {
  const message = data as null | { type?: unknown; value?: unknown };
  if (!message || message.type !== themeMessage.request) {
    return "";
  }
  return themeValues.find((value) => value === message.value) ?? "";
}

export const embedViews = {
  operations: { label: "Tool definitions", view: "operations" },
  runtimes: { label: "Runtime distribution", view: "blueprints" },
  universe: { label: "Universe · app orbit", view: "universe" },
} as const;

export function embedMode(search: string): "" | keyof typeof embedViews {
  const query = new URLSearchParams(search);
  const mode = query.get("embed");
  if (mode && Object.hasOwn(embedViews, mode)) {
    return mode as keyof typeof embedViews;
  }
  if (mode === "1") {
    const view = query.get("view") ?? defaultView;
    return view === "blueprints" ? "runtimes" : view === "operations" ? "operations" : "universe";
  }
  return "";
}

export function readLocation(search: string, hash: string, data: Manifest): Route {
  const mode = embedMode(search);
  if (!mode && hash.startsWith("#/")) {
    return readRoute(hash, data);
  }
  const query = new URLSearchParams(search);
  const projectType = query.get("projectType");
  if (projectType !== null) {
    query.set("project", projectType);
  }
  return readRoute(
    `#/${mode ? embedViews[mode].view : (query.get("view") ?? defaultView)}?${query}`,
    data,
  );
}

export function themePreference(search: string): ThemePreference {
  const value = new URLSearchParams(search).get("theme");
  return value === "dark" || value === "light" ? value : "auto";
}

export function viewURL(
  href: string,
  route: Route,
  theme: ThemePreference,
  isEmbedded = false,
  preset: EmbedPreset = "",
): string {
  const url = new URL(href);
  const query = new URLSearchParams(routeHash(route).split("?", 2)[1]);
  if (query.has("project")) {
    query.set("projectType", query.get("project")!);
    query.delete("project");
  }
  query.set("view", route.view);
  query.set("theme", theme);
  if (isEmbedded) {
    query.set("embed", route.view === "blueprints" ? "runtimes" : route.view);
    if (preset) {
      query.set("preset", preset);
    }
  }
  url.search = query.toString();
  url.hash = "";
  return url.href;
}

function escapeHTML(value: string) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}
