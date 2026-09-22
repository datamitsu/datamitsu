<script lang="ts">
 import {onMount,tick,untrack} from "svelte";
 import brandIcon from "../../website/static/img/icon.png?inline";
 import Operations from "./Operations.svelte";
 import Universe from "./Universe.svelte";
 import Blueprints from "./Blueprints.svelte";
 import QuickFind from "./QuickFind.svelte";
 import {download,routeHash,type Manifest} from "./model";
 import EmbeddedView from "./EmbeddedView.svelte";
 import {embedCode,embedMode,embedPreset,embedViews,readLocation,readThemeMessage,themeMessage,themePreference,viewURL,type EmbedPreset} from "./embedding";
 let {data}:{data:Manifest}=$props();
 let route=$state(untrack(()=>readLocation(location.search,location.hash,data)));
 const embedded=Boolean(embedMode(location.search));
 const preset=embedPreset(location.search);
 let embedDialog=$state<HTMLDialogElement>()!;
 let embedSection=$state<keyof typeof embedViews>("universe");
 let embedDetail=$state<EmbedPreset>("");
 const embedRoute=$derived({...route,view:embedViews[embedSection].view});
 let preference=$state(themePreference(location.search));
 const snippet=$derived(embedCode(location.href,embedRoute,preference,embedSection==="universe"?embedDetail:""));
 let systemDark=$state(matchMedia("(prefers-color-scheme: dark)").matches);
 const theme=$derived(preference==="auto"?(systemDark?"dark":"light"):preference);
 let message=$state("");let copyValue=$state("");let copyDialog=$state<HTMLDialogElement>()!;let copyField=$state<HTMLTextAreaElement>()!;let notificationTimer:ReturnType<typeof setTimeout>;
 onMount(()=>{
  const media=matchMedia("(prefers-color-scheme: dark)");
  const change=()=>{systemDark=media.matches;};media.addEventListener("change",change);change();
  return ()=>{clearTimeout(notificationTimer);media.removeEventListener("change",change);};
 });
 $effect(()=>{document.documentElement.dataset.theme=theme;});
 function writeHistory(push=false){
  if(embedded){return;}
  const url=new URL(location.href);url.searchParams.set("theme",preference);url.hash=routeHash(route);
  try{if(push){history.pushState(null,"",url.href);}else{history.replaceState(null,"",url.href);}}
  catch{/* Opaque sandbox origins and some file viewers prohibit history changes. */}
 }
 $effect(()=>{writeHistory();});
 function navigate(view:string){route.view=view;writeHistory(true);}
 function restore(){route=readLocation(location.search,location.hash,data);preference=themePreference(location.search);}
 function reset(){route.q="";route.project="";route.runtime="";route.status="all";route.operation="all";}
 function choose(kind:string,value:string){reset();if(kind==="app"){route.app=value;navigate("universe");}else{if(kind==="tool"){route.tool=value;}else {route.project=value;}navigate("operations");}}
 // Any origin may set the mode: it changes colors and nothing else, and a
 // sandboxed frame has no origin of its own to check the host against.
 function acceptTheme(event:MessageEvent){
  const value=readThemeMessage(event.data);
  if(!value){return;}
  preference=value;
  (event.source as WindowProxy|null)?.postMessage({type:themeMessage.ack,value},"*");
 }
 async function copy(text:string){
  try{await navigator.clipboard.writeText(text);message="Copied to clipboard";clearTimeout(notificationTimer);notificationTimer=setTimeout(()=>{message="";},3000);}
  catch{copyValue=text;await tick();copyDialog.showModal();copyField.focus();copyField.select();}
 }
</script>
<svelte:window onhashchange={restore} onpopstate={restore} onmessage={acceptTheme}/>
<svelte:head><link rel="icon" type="image/png" href={brandIcon}/></svelte:head>
{#if embedded}<EmbeddedView {data} {route} {theme} {preset}/>{:else}
<main class="atlas">
 <header class="masthead"><a class="brand" href="#/universe" aria-label="datamitsu Config Inspector"><img class="brand-mark" src={brandIcon} alt="" width="40" height="40"/><span class="brand-name">datamitsu</span><span class="edition">CONFIG INSPECTOR</span></a><div class="header-actions"><QuickFind {data} {choose}/><label class="theme-picker"><span class="sr-only">Color theme</span><span class="select"><select aria-label="Color theme" bind:value={preference}><option value="auto">Auto theme</option><option value="light">Light</option><option value="dark">Dark</option></select><i aria-hidden="true">▾</i></span></label><button aria-label="Copy link to current view" onclick={()=>copy(viewURL(location.href,route,preference))}>↗ <span>Share view</span></button><button aria-label="Copy embed code" onclick={()=>{embedSection=route.view==="blueprints"?"runtimes":route.view==="operations"?"operations":"universe";embedDialog.showModal();}}>&lt;/&gt; <span>Embed</span></button><button class="dataset-button" aria-label="Download display metadata as JSON" onclick={()=>download(JSON.stringify(data,null,2),"datamitsu-inspector-manifest.json","application/json")}>↓ <span>Dataset</span></button></div></header>
 <p class="snapshot-caption"><strong>{data.name}</strong>{#if data.capture?.package}<span>{data.capture.package}{data.capture.version?` @ ${data.capture.version}`:""}</span>{/if}{#if data.capture}<span>Captured {data.capture.capturedAt.slice(0,10)}</span>{/if}</p>
 <nav class="views" aria-label="Inspector views">{#each [["universe","Universe"],["operations","Operations"],["blueprints","Blueprints"]] as [id,label],index}<a href={`#/${id}`} aria-current={route.view===id?"page":undefined} onclick={(event)=>{event.preventDefault();navigate(id!);}}><span>0{index+1}</span>{label}</a>{/each}</nav>
 {#if route.view!=="blueprints"}<div class="toolbar"><label class="search"><span>Search</span><input type="search" bind:value={route.q} placeholder="Try eslint, ruff, prettier…"/></label><label><span>Project type</span><span class="select"><select bind:value={route.project}><option value="">All project types</option>{#each data.projectTypes as project}<option value={project.id}>{project.id}</option>{/each}</select><i aria-hidden="true">▾</i></span></label><button onclick={reset}>Reset</button></div>{/if}
 {#if route.view==="operations"}<Operations {data} bind:route showApp={(name)=>choose("app",name)} {copy}/>
 {:else if route.view==="universe"}<Universe {data} bind:route {theme} showTool={(name)=>choose("tool",name)}/>
 {:else}<Blueprints {data} bind:route/>{/if}
 <footer><span>DATAMITSU <span class="mono">{data.version}</span></span><span>Resolved config snapshot · Works offline · Environment-dependent settings may differ in CI</span></footer>
</main>
{/if}
{#if message}<p class="notification" role="status">{message}</p>{/if}
<dialog bind:this={copyDialog} class="copy-dialog" aria-labelledby="copy-heading"><h2 id="copy-heading">Copy this text</h2><p class="muted">Select and copy using your keyboard.</p><textarea bind:this={copyField} value={copyValue} readonly aria-label="Text to copy"></textarea><form method="dialog"><button>Done</button></form></dialog>

{#if !embedded}
<dialog bind:this={embedDialog} class="copy-dialog embed-dialog" aria-labelledby="embed-heading">
 <h2 id="embed-heading">Embed a section</h2>
 <p class="muted">Only the selected content. No navigation, header, or inspector controls.</p>
 <label>Section <span class="select"><select bind:value={embedSection}>{#each Object.entries(embedViews) as [id,section]}<option value={id}>{section.label}</option>{/each}</select><i aria-hidden="true">▾</i></span></label>
 {#if embedSection==="universe"}<label>Detail <span class="select"><select bind:value={embedDetail}><option value="">Full section</option><option value="minimal">Minimal orbit</option></select><i aria-hidden="true">▾</i></span></label>
 <p class="footnote">The minimal orbit keeps the sphere and the runtime strip. Hovering a point names its app, runtime and pinned version; selecting one opens this inspector in a new tab.</p>{/if}
 <p class="footnote">Publish this HTML at a stable URL. Replace it when your config changes; every embed will show the new snapshot on its next load.</p>
 <textarea readonly value={snippet} aria-label="Section embed HTML"></textarea>
 <div class="row"><button onclick={()=>copy(snippet)}>Copy HTML</button><form method="dialog"><button>Done</button></form></div>
</dialog>

{/if}
