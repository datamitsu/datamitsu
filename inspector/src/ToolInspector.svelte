<script lang="ts">
  import type { Manifest, Route, Tool } from "./model";

  import { select } from "./selection";
  let {
    copy,
    data,
    route = $bindable(),
    showApp,
    tool,
  }: {
    copy: (text: string) => void;
    data: Manifest;
    route: Route;
    showApp: (name: string) => void;
    tool: Tool | undefined;
  } = $props();
  // The managed files the current selection keeps, not every file in the snapshot.
  const selection = $derived(select(data, route));
</script>

<aside class="detail-panel" aria-label="Tool inspector">
  {#if tool}
    <p class="eyebrow">TOOL INSPECTOR</p>
    <h2>{tool.id}</h2>
    <p class="muted">{tool.name}</p>
    <span class:skipped={tool.skipped} class:enabled={!tool.skipped} class="badge"
      >{tool.skipped ? "Skipped" : "Enabled"} in this snapshot</span
    >
    <nav class="detail-tabs" aria-label="Tool detail sections">
      <button
        aria-pressed={route.section === "overview"}
        onclick={() => (route.section = "overview")}>Overview</button
      >
      <button
        aria-pressed={route.section === "metadata"}
        onclick={() => (route.section = "metadata")}>{"{ }"} Metadata</button
      >
    </nav>
    {#if route.section === "metadata"}
      <p class="muted">Display metadata from the resolved config.</p>
      <button onclick={() => copy(JSON.stringify(tool, null, 2))}>Copy JSON</button>
      <pre class="metadata">{JSON.stringify(tool, null, 2)}</pre>
    {:else}
      {#if tool.skipped}<p class="skip-reason">
          {tool.skipReason || "Disabled in the generating environment."}
        </p>{/if}
      <h3>APPLIES TO</h3>
      <div class="chips">
        {#each tool.projectTypes.length ? tool.projectTypes : ["All project types"] as project (project)}<span
            class="badge">{project}</span
          >{/each}
      </div>
      <h3>OPERATIONS · {tool.operations.length}</h3>
      {#each tool.operations as op, index (index)}
        <article class="operation-card">
          <div class="row">
            <span class="badge {op.kind}">{op.kind}</span><span class="mono muted"
              >Priority {op.priority}</span
            >
          </div>
          <p class="operation-chain">
            <button class="text-button" onclick={() => showApp(op.app)}>{op.app}</button><span
              >→</span
            >{op.scope}
          </p>
          <details open>
            <summary>File patterns · {op.globs.length || "all files"}</summary>
            <div class="chips">
              {#each op.globs as glob (glob)}<code>{glob}</code>{:else}<span class="muted"
                  >All discovered files</span
                >{/each}
            </div>
          </details>
          {#if op.excludeGlobs.length}<details>
              <summary>Excluded patterns · {op.excludeGlobs.length}</summary>
              <div class="chips">
                {#each op.excludeGlobs as glob (glob)}<code>{glob}</code>{/each}
              </div>
            </details>{/if}
        </article>
      {/each}
      {@const files = selection.managedConfigs.filter((file) => file.tools.includes(tool.id))}
      <h3>MANAGED FILES · {files.length}</h3>
      {#each files as file (file.name)}<div class="managed-file">
          <code>{file.name}</code>{#if file.deleteOnly}<span class="badge skipped">Delete only</span
            >{/if}
        </div>{:else}<p class="muted">No managed file declares ownership by this tool.</p>{/each}
      {#each [...new Set(tool.operations.map((op) => op.app))] as app (app)}<button
          class="reveal-app"
          onclick={() => showApp(app)}>Find {app} in Universe ↗</button
        >{/each}
    {/if}
  {:else}<p class="eyebrow">TOOL INSPECTOR</p>
    <h2>No matching tools.</h2>
    <p class="muted">Try another filter or reset your selection.</p>{/if}
</aside>
