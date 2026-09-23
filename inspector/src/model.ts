export const defaultView = "universe";
export interface AppDefinition {
  dependsOn: string[];
  description: string;
  name: string;
  /**
   * Where a reader learns about this app: the configuration's own link, or one derived from it.
   */
  officialUrl?: string;
  /**
   * True when the core worked the link out from a download URL or a package name.
   */
  officialUrlDerived?: boolean;
  runtime: string;
  version: string;
}
export interface Manifest {
  apps: AppDefinition[];
  capture?: { capturedAt: string; package?: string; version?: string };
  managedConfigs: {
    deleteOnly: boolean;
    /**
     * Present only for an entry that may live in .datamitsu/configs/ until a project ejects it.
     */
    ejectable?: boolean;
    name: string;
    /**
     * Where an ejectable entry lives in the captured configuration.
     */
    placement?: "internal" | "repo";
    projectTypes: string[];
    scope: string;
    tools: string[];
  }[];
  name: string;
  projectTypes: { description: string; id: string; markers: string[] }[];
  schemaVersion: number;
  tools: Tool[];
  version: string;
}
export interface Operation {
  app: string;
  excludeGlobs: string[];
  globs: string[];
  kind: string;
  priority: number;
  scope: string;
}
export interface Route {
  app: string;
  diagram: string;
  layout: string;
  list: string;
  operation: string;
  project: string;
  q: string;
  runtime: string;
  section: string;
  status: string;
  tool: string;
  view: string;
}
export interface Tool {
  id: string;
  name: string;
  operations: Operation[];
  projectTypes: string[];
  skipped: boolean;
  skipReason: string;
}
export const runtimeNames: Record<string, string> = {
  binary: "Native binary",
  bun: "Bun",
  go: "Go",
  jvm: "JVM",
  node: "Node.js",
  python: "Python",
  shell: "Shell",
  unknown: "Other",
};
export function download(text: string, name: string, type: string) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export function isToolMatching(tool: Tool, route: Route): boolean {
  return (
    (!route.project ||
      tool.projectTypes.length === 0 ||
      tool.projectTypes.includes(route.project)) &&
    [tool.id, tool.name, ...tool.operations.map((op) => op.app)]
      .join(" ")
      .toLowerCase()
      .includes(route.q.trim().toLowerCase()) &&
    (route.status === "all" ||
      (route.status === "skipped" && tool.skipped) ||
      (route.status === "enabled" && !tool.skipped)) &&
    (route.operation === "all" || tool.operations.some((op) => op.kind === route.operation))
  );
}
/**
 * What to call an app's link, by what the link actually is. A page the configuration chose is
 * documentation; one derived from a download URL or a package name is the package or the
 * repository, and says so rather than promising more than it is.
 */
export function officialUrlLabel(app: AppDefinition): string {
  if (!app.officialUrl) {
    return "";
  }
  if (!app.officialUrlDerived) {
    return "Documentation";
  }
  const host = hostOf(app.officialUrl);
  if (packageHosts.has(host)) {
    return "Package";
  }
  return forgeHosts.has(host) ? "Repository" : "Homepage";
}
export function readRoute(hash: string, data: Manifest): Route {
  const [path, query] = hash.replace(/^#\/?/, "").split("?", 2);
  const p = new URLSearchParams(query);
  const choice = (name: string, values: string[], fallback: string) =>
    values.includes(p.get(name) ?? "") ? p.get(name)! : fallback;
  return {
    app: choice(
      "app",
      data.apps.map((v) => v.name),
      "",
    ),
    diagram: choice(
      "diagram",
      ["configuration", "runtimes"],
      p.get("runtime") || p.get("project") ? "runtimes" : "configuration",
    ),
    layout: choice("layout", ["sphere", "helix", "clusters"], "sphere"),
    list: choice("list", ["table", "cards"], "table"),
    operation: choice("operation", ["all", "fix", "lint"], "all"),
    project: choice(
      "project",
      data.projectTypes.map((v) => v.id),
      "",
    ),
    q: p.get("q") ?? "",
    runtime: choice(
      "runtime",
      data.apps.map((v) => v.runtime),
      "",
    ),
    section: choice("section", ["overview", "metadata"], "overview"),
    status: choice("status", ["all", "enabled", "skipped"], "all"),
    tool: choice(
      "tool",
      data.tools.map((v) => v.id),
      "",
    ),
    view: ["blueprints", "operations", "universe"].includes(path ?? "") ? path! : defaultView,
  };
}
export function routeHash(route: Route): string {
  const parameters = new URLSearchParams();
  for (const [key, value] of Object.entries(route)) {
    if (key !== "view" && value) {
      parameters.set(key, value);
    }
  }
  return `#/${route.view}?${parameters}`;
}

const forgeHosts = new Set(["codeberg.org", "gitea.com", "github.com", "gitlab.com"]);
const packageHosts = new Set(["central.sonatype.com", "pkg.go.dev", "pypi.org", "www.npmjs.com"]);

export function runtimeColor(runtime: string) {
  return `var(--runtime-${Object.hasOwn(runtimeNames, runtime) ? runtime : "unknown"})`;
}

function hostOf(value: string): string {
  try {
    return new URL(value).host;
  } catch {
    return "";
  }
}
