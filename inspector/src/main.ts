import { mount } from "svelte";

import type { Manifest } from "./model";

import { schemaVersion } from "../../internal/inspector/protocol.json";
import App from "./App.svelte";
import "./style.css";

function readManifest(): Manifest {
  const data = JSON.parse(
    document.getElementById("inspector-manifest")?.textContent ?? "null",
  ) as Manifest;
  if (
    !data ||
    data.schemaVersion !== schemaVersion ||
    typeof data.name !== "string" ||
    [data.apps, data.tools, data.projectTypes, data.managedConfigs].some(
      (value) => !Array.isArray(value),
    )
  ) {
    throw new Error("This inspector contains an invalid or unsupported configuration snapshot.");
  }
  return data;
}

const target = document.getElementById("app")!;
try {
  mount(App, { props: { data: readManifest() }, target });
} catch (error) {
  target.textContent =
    error instanceof Error ? error.message : "The inspector could not be opened.";
}
