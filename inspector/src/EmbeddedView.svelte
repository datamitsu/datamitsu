<script lang="ts">
 import {onMount,untrack} from "svelte";
 import {themePreference,viewURL} from "./embedding";
 import {select} from "./selection";
 import {runtimeColor,runtimeNames,type Manifest,type Route} from "./model";
 import {createScene} from "./scene.js";
 let {data,route,theme,preset=""}:{data:Manifest;route:Route;theme:string;preset?:string}=$props();
 const minimal=$derived(preset==="minimal"&&route.view==="universe");
 let runtime=$state(untrack(()=>route.runtime));
 const filtered=$derived(minimal?{...route,runtime}:route);
 // One selection here too: the strip, the legend and the list are the same
 // answer the full inspector shows for this route. See selection.ts.
 const selection=$derived(select(data,filtered));
 const groups=$derived(selection.families.filter(family=>family.count>0));
 const apps=$derived(selection.apps);
 const tools=$derived(selection.tools.filter(tool=>!route.tool||tool.id===route.tool));
 let selected=$state("");
 let hovered=$state("");let tip=$state({x:0,y:0});
 const hoveredApp=$derived(hovered?data.apps.find(item=>item.name===hovered):undefined);
 let canvas=$state<HTMLCanvasElement>()!;let labels=$state<HTMLDivElement>()!;
 let scene=$state<ReturnType<typeof createScene>>();
 function appURL(name:string){
  return viewURL(location.href,{...route,app:name,runtime:"",q:""},themePreference(location.search));
 }
 const fullURL=$derived(viewURL(location.href,{...route,app:"",runtime:"",q:""},themePreference(location.search)));
 // A new tab, or nothing: an embed that navigated itself would replace the host
 // page's section with the whole inspector.
 function openApp(name:string){
  const link=document.createElement("a");
  link.href=appURL(name);link.target="_blank";link.rel="noopener";
  link.click();
 }
 onMount(()=>{
  if(!canvas){return;}
  const instance=createScene(canvas,labels,(name:string)=>{if(minimal){openApp(name);}else{selected=name;}},()=>{},{
   autoRotate:minimal,
   centerX:minimal?0.5:0.58,
   onHover:(name:string,x:number,y:number)=>{hovered=name;tip={x,y};},
  });
  instance.setData(data.apps);instance.update(apps,route.app,true);instance.layout(route.layout);
  if(instance.isMoving()&&!minimal){instance.motion();}
  scene=instance;return ()=>instance.destroy();
 });
 $effect(()=>{if(theme){scene?.theme();}});
 $effect(()=>{scene?.update(apps,minimal?"":selected||route.app,true);});
</script>
<main class="embedded" class:embedded-strip={route.view==="blueprints"} class:embedded-minimal={minimal} aria-label={`datamitsu ${route.view} snapshot`}>
 {#if route.view==="blueprints"}
  <div class="embed-runtime-heading"><strong>{apps.length} managed apps</strong><span class="muted">{groups.length} runtime families</span></div>
  <div class="embed-distribution" role="img" aria-label={groups.map(group=>`${group.label}: ${group.count}`).join(", ")}>{#each groups as group}<span style:flex-grow={group.count} style:background={runtimeColor(group.runtime)}></span>{/each}</div>
  <ul class="embed-legend">{#each groups as group}<li><i style:background={runtimeColor(group.runtime)}></i><span>{group.label}</span><strong>{group.count}</strong></li>{/each}</ul>
  {#if !apps.length}<p>No apps match these filters.</p>{/if}
 {:else if route.view==="operations"}
  <div class="embed-runtime-heading"><strong>{tools.length} tool definitions</strong><span class="muted">Resolved configuration</span></div>
  {#each tools as tool}<details class="embed-tool" open={tool.id===route.tool}>
   <summary><strong>{tool.id}</strong><span class:skipped={tool.skipped} class="badge">{tool.skipped?"Skipped":"Enabled"}</span><span class="muted">{[...new Set(tool.operations.map(op=>op.scope))].join(" · ")}</span></summary>
   <div class="embed-tool-body"><h1>{tool.name}</h1>{#if tool.skipReason}<p>{tool.skipReason}</p>{/if}
    {#each tool.operations as op}<article><h2>{op.kind} <span class="muted">via {op.app}</span></h2><p>{op.scope} · Priority {op.priority}</p><p>Patterns: <code>{op.globs.join(", ")||"All files"}</code></p>{#if op.excludeGlobs.length}<p>Excluded: <code>{op.excludeGlobs.join(", ")}</code></p>{/if}</article>{/each}
   </div>
  </details>{:else}<p>No tools match these filters.</p>{/each}
  <p class="footnote">Status reflects configuration, not an execution plan.</p>
 {:else if minimal}
  <div class="map-panel embed-map"><canvas bind:this={canvas} aria-label="App orbit, grouped by runtime. An accessible app directory follows."></canvas><div bind:this={labels} class="scene-labels"></div>
   {#if hoveredApp}<p class="orbit-tip" aria-hidden="true" style:left={`${tip.x}px`} style:top={`${tip.y}px`}><strong>{hoveredApp.name}</strong><span>{runtimeNames[hoveredApp.runtime]??hoveredApp.runtime}{hoveredApp.version?` · ${hoveredApp.version}`:""}</span></p>{/if}
  </div>
  <div class="minimal-rail">
   <div class="runtime-strip"><div class="legend"><button aria-pressed={!runtime} onclick={()=>runtime=""}>All <span>{selection.families.reduce((total,family)=>total+family.count,0)}</span></button>{#each groups as group}<button class="runtime-filter" style:--runtime-color={runtimeColor(group.runtime)} aria-pressed={runtime===group.runtime} onclick={()=>runtime=runtime===group.runtime?"":group.runtime}><span class="runtime-dot" style:--runtime-color={runtimeColor(group.runtime)}></span>{group.label} <span>{group.count}</span></button>{/each}</div></div>
   <div class="runtime-distribution" role="group" aria-label="Runtime distribution">{#each groups as group}<button title={`${group.label}: ${group.count} apps`} style:flex-grow={group.count} style:background={runtimeColor(group.runtime)} aria-label={`${group.label}: ${group.count} apps`} aria-pressed={runtime===group.runtime} onclick={()=>runtime=runtime===group.runtime?"":group.runtime}></button>{/each}</div>
   <!-- The minimal preset shows no chrome, but a pointer is not the only way in:
        the directory stays for a screen reader and for the keyboard. -->
   <ul class="sr-only">{#each apps as app}<li><a href={appURL(app.name)} target="_blank" rel="noopener">{app.name} {app.version}</a></li>{/each}</ul>
   <p class="minimal-footer"><strong>{data.name}</strong><a href={fullURL} target="_blank" rel="noopener">Open the full inspector ↗</a></p>
  </div>
 {:else}
  <div class="map-panel embed-map"><canvas bind:this={canvas} aria-label="App orbit, grouped by runtime. An accessible app directory follows."></canvas><div bind:this={labels} class="scene-labels"></div><div class="embed-scene-title"><strong>{apps.length} managed apps</strong><span>Positions are illustrative</span></div></div>
  <ul class="embed-legend">{#each groups as group}<li><i style:background={runtimeColor(group.runtime)}></i><span>{group.label}</span><strong>{group.count}</strong></li>{/each}</ul>
  <details class="app-directory"><summary>App directory · {apps.length}</summary><div class="chips">{#each apps as app}<button aria-pressed={selected===app.name} onclick={()=>selected=app.name}>{app.name}</button>{/each}</div></details>
  {#if selected}{@const app=apps.find(item=>item.name===selected)}{#if app}<p class="embed-selection" role="status"><strong>{app.name}</strong> · {app.runtime} {app.version}<br/>{app.description}</p>{/if}{/if}
 {/if}
</main>
