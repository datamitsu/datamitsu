/**
 * What the current route selects out of a snapshot: one answer, read by every view and every number
 * on screen. Search text, project type and the runtime filter combine here and nowhere else — a
 * view that filtered for itself would disagree with the counter beside it the first time someone
 * touched a filter.
 */

import {
  type AppDefinition,
  isToolMatching,
  type Manifest,
  type Route,
  runtimeNames,
  type Tool,
} from "./model.ts";

export interface RuntimeFamily {
  count: number;
  label: string;
  runtime: string;
}

export interface Selection {
  /**
   * The apps the filters leave: lit in the orbit, listed in the directory.
   */
  apps: AppDefinition[];
  /**
   * Every runtime family the snapshot has, counted under the search and project filters but not
   * under the runtime filter itself — the strip keeps its shape, and a reader can see what
   * following another runtime would give.
   */
  families: RuntimeFamily[];
  managedConfigs: Manifest["managedConfigs"];
  projectTypes: Manifest["projectTypes"];
  tools: Tool[];
  /**
   * What the snapshot holds in total, for the "N of M" a filtered view shows.
   */
  totals: { apps: number; managedConfigs: number; projectTypes: number; tools: number };
}

export function matchingApps(data: Manifest, route: Route): AppDefinition[] {
  const tools = matchingTools(data, { ...route, q: "" });
  return searchApps(data, route.q).filter(
    (app) =>
      (!route.runtime || app.runtime === route.runtime) &&
      (!route.project || tools.some((tool) => tool.operations.some((op) => op.app === app.name))),
  );
}

export function matchingTools(data: Manifest, route: Route): Tool[] {
  // A tool is named by its own id as well as by the apps it runs, so a search
  // that lights an app in the orbit keeps the tool that drives it in the table.
  // Without that, "lint" would select ruff the app and drop ruff the tool.
  const searched = new Set(searchApps(data, route.q).map((app) => app.name));
  return data.tools.filter(
    (tool) =>
      (isToolMatching(tool, route) ||
        (isToolMatching(tool, { ...route, q: "" }) &&
          tool.operations.some((op) => searched.has(op.app)))) &&
      (!route.runtime ||
        tool.operations.some((op) =>
          data.apps.some((app) => app.name === op.app && app.runtime === route.runtime),
        )),
  );
}

/**
 * The runtime families present in what the route selects, in the order the snapshot lists them. The
 * embed's runtime distribution and its text fallback read this.
 */
export function runtimeCounts(data: Manifest, route: Route): RuntimeFamily[] {
  const apps = matchingApps(data, route);
  return [...new Set(apps.map((app) => app.runtime))].map((runtime) => ({
    count: apps.filter((app) => app.runtime === runtime).length,
    label: runtimeNames[runtime] ?? runtime,
    runtime,
  }));
}

export function select(data: Manifest, route: Route): Selection {
  const apps = matchingApps(data, route);
  const tools = matchingTools(data, route);
  const selectedTools = new Set(tools.map((tool) => tool.id));
  const withoutRuntime = matchingApps(data, { ...route, runtime: "" });
  return {
    apps,
    families: [...new Set(data.apps.map((app) => app.runtime))].map((runtime) => ({
      count: withoutRuntime.filter((app) => app.runtime === runtime).length,
      label: runtimeNames[runtime] ?? runtime,
      runtime,
    })),
    // A file with no declared tool belongs to the configuration itself, not to a
    // selection of it, so only a project filter can take it out of view.
    managedConfigs: data.managedConfigs.filter(
      (file) =>
        (!route.project ||
          file.projectTypes.length === 0 ||
          file.projectTypes.includes(route.project)) &&
        (file.tools.length === 0 || file.tools.some((id) => selectedTools.has(id))),
    ),
    projectTypes: route.project
      ? data.projectTypes.filter((project) => project.id === route.project)
      : data.projectTypes,
    tools,
    totals: {
      apps: data.apps.length,
      managedConfigs: data.managedConfigs.length,
      projectTypes: data.projectTypes.length,
      tools: data.tools.length,
    },
  };
}

/**
 * The apps a search text names. Text is the one filter both kinds share, so it is applied in one
 * place and read by the app and the tool matcher alike.
 */
function searchApps(data: Manifest, q: string): AppDefinition[] {
  const needle = q.toLowerCase().trim();
  return data.apps.filter((app) =>
    `${app.name} ${app.description} ${app.runtime}`.toLowerCase().includes(needle),
  );
}
