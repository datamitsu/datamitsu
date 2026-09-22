<script lang="ts">
  import { onMount } from "svelte";

  import DetailDrawer from "./DetailDrawer.svelte";
  import { type Manifest, officialUrlLabel, type Route, runtimeColor, runtimeNames } from "./model";
  import { createScene } from "./scene.js";
  import { select } from "./selection";
  let {
    data,
    route = $bindable(),
    showTool,
    theme,
  }: { data: Manifest; route: Route; showTool: (name: string) => void; theme: string } = $props();
  let drawer = $state<DetailDrawer>();
  function selectApp(name: string) {
    route.app = name;
    drawer?.open();
  }
  let canvas = $state<HTMLCanvasElement>()!;
  let labels = $state<HTMLDivElement>()!;
  let scene = $state<ReturnType<typeof createScene>>();
  let moving = $state(false);
  // Everything on screen — the lit points, the counters, the strip, the legend
  // and the directory — is this one selection. See selection.ts.
  const selection = $derived(select(data, route));
  const apps = $derived(selection.apps);
  const app = $derived(
    apps.find((item) => item.name === route.app) ??
      apps.find((item) => item.name === "eslint") ??
      apps[0],
  );
  const tools = $derived(
    selection.tools.filter((tool) => tool.operations.some((op) => op.app === app?.name)),
  );
  const files = $derived(
    selection.managedConfigs.filter((file) => tools.some((tool) => file.tools.includes(tool.id))),
  );
  const counters = $derived([
    [apps.length, selection.totals.apps, "managed apps"],
    [selection.tools.length, selection.totals.tools, "tool definitions"],
    [selection.projectTypes.length, selection.totals.projectTypes, "project types"],
    [selection.managedConfigs.length, selection.totals.managedConfigs, "managed files"],
  ] as const);
  onMount(() => {
    const instance = createScene(canvas, labels, selectApp, (value: boolean) => {
      moving = value;
    });
    instance.setData(data.apps);
    scene = instance;
    moving = instance.isMoving();
    return () => instance.destroy();
  });
  $effect(() => {
    scene?.update(apps, app?.name ?? "", true);
  });
  $effect(() => {
    scene?.layout(route.layout);
  });
  $effect(() => {
    if (theme) {
      scene?.theme();
    }
  });
</script>

<section class="view" aria-label="Universe">
  <div class="universe-grid">
    <div class="map-panel">
      <canvas
        bind:this={canvas}
        aria-label="Spatial map of apps. Use the app directory below for keyboard selection."
      ></canvas>
      <div bind:this={labels} class="scene-labels"></div>
      <div class="scene-copy">
        <p class="eyebrow"><span class="status-dot"></span> THE DATAMITSU UNIVERSE</p>
        <h1>Your stack.<br /><em>In orbit.</em></h1>
        <p class="muted">A whole world of tools.<br />One place to bring them together.</p>
        <p class="scene-annotation">
          Every point is a real app.<br />Find yours. Follow its connections.
        </p>
      </div>
      <div class="scene-controls segmented" role="group" aria-label="Scene layout">
        {#each ["sphere", "helix", "clusters"] as layout (layout)}<button
            aria-pressed={route.layout === layout}
            onclick={() => (route.layout = layout)}>{layout}</button
          >{/each}
      </div>
      <div class="camera-controls">
        <button
          aria-label="Automatic rotation"
          aria-pressed={moving}
          onclick={() => (moving = scene?.motion() ?? false)}>↻ Auto</button
        ><button aria-label="Zoom out" onclick={() => scene?.zoom(-0.15)}>−</button><button
          aria-label="Zoom in"
          onclick={() => scene?.zoom(0.15)}>+</button
        ><button aria-label="Reset camera" onclick={() => scene?.reset()}>⤾</button>
      </div>
      <div class="stats">
        {#each counters as [shown, total, label] (label)}<div>
            <strong>{shown}</strong><span>{shown === total ? label : `of ${total} ${label}`}</span>
          </div>{/each}
      </div>
      <p class="scene-hint">DRAG TO ROTATE ↔ SELECT TO EXPLORE</p>
    </div>
    <DetailDrawer bind:this={drawer} selection={route.app} title={app?.name ?? "app"}
      ><aside class="detail-panel app-detail" aria-label="App inspector">
        {#if app}<p class="eyebrow">APP / INSPECTOR</p>
          <h2>{app.name}</h2>
          <span class="badge" style:color={runtimeColor(app.runtime)}
            >{runtimeNames[app.runtime] ?? app.runtime}</span
          >{#if app.version}<span class="badge">{app.version}</span>{/if}
          <p class="muted">{app.description}</p>
          {#if app.officialUrl}<p class="app-link">
              <a href={app.officialUrl} target="_blank" rel="noopener noreferrer"
                >{officialUrlLabel(app)} ↗</a
              >
            </p>{/if}
          <h3>TOOL DEFINITIONS · {tools.length}</h3>
          {#each tools as tool (tool.id)}<button class="app-tool" onclick={() => showTool(tool.id)}
              ><strong>{tool.id} ↗</strong><span
                >{tool.operations.map((op) => `${op.kind} · ${op.scope}`).join(" / ")}</span
              ></button
            >{:else}<p class="muted">
              Available on demand. No automatic operation references this app.
            </p>{/each}
          {#if app.dependsOn.length}<h3>APP DEPENDENCIES</h3>
            <div class="chips">
              {#each app.dependsOn as dependency (dependency)}<button
                  onclick={() => {
                    route.q = "";
                    route.project = "";
                    route.runtime = "";
                    route.app = dependency;
                  }}>{dependency}</button
                >{/each}
            </div>{/if}
          <h3>MANAGED FILES · {files.length}</h3>
          {#each files as file (file.name)}<code class="managed-file">{file.name}</code>{:else}<p
              class="muted"
            >
              No managed file declares ownership by these tools.
            </p>{/each}
          <h3>RUN ON DEMAND</h3>
          <code class="command">datamitsu exec {app.name} -- --help</code>
        {:else}<h2>No matching apps.</h2>
          <p class="muted">Try another search or reset the filters.</p>{/if}
      </aside></DetailDrawer
    >
  </div>
  <div class="runtime-strip">
    <span class="eyebrow">FOLLOW A RUNTIME</span>
    <div class="legend">
      <button aria-pressed={!route.runtime} onclick={() => (route.runtime = "")}
        >All <span>{selection.families.reduce((total, family) => total + family.count, 0)}</span
        ></button
      >{#each selection.families as family (family.runtime)}<button
          class="runtime-filter"
          style:--runtime-color={runtimeColor(family.runtime)}
          aria-pressed={route.runtime === family.runtime}
          onclick={() => (route.runtime = family.runtime)}
          ><span class="runtime-dot" style:--runtime-color={runtimeColor(family.runtime)}
          ></span>{family.label} <span>{family.count}</span></button
        >{/each}
    </div>
  </div>
  <div class="runtime-distribution" role="group" aria-label="Runtime distribution">
    {#each selection.families as family (family.runtime)}<button
        title={`${family.label}: ${family.count} apps`}
        style:flex-grow={family.count}
        style:background={runtimeColor(family.runtime)}
        aria-label={`${family.label}: ${family.count} apps`}
        aria-pressed={route.runtime === family.runtime}
        onclick={() => (route.runtime = family.runtime)}
      ></button>{/each}
  </div>
  <details class="app-directory">
    <summary>Browse matching apps · {apps.length} of {selection.totals.apps}</summary>
    <div class="chips">
      {#each apps as item (item.name)}<button
          aria-pressed={app?.name === item.name}
          onclick={() => selectApp(item.name)}>{item.name}</button
        >{/each}
    </div>
  </details>
  <p class="footnote">
    Colors and spokes show runtime membership. Positions and motion are illustrative.
  </p>
</section>
