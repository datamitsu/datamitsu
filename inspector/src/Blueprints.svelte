<script lang="ts">
 import {select} from "./selection";
 import {download,runtimeColor,runtimeNames,type Manifest,type Route} from "./model";
 let {data,route=$bindable()}:{data:Manifest;route:Route}=$props();
 let viewport=$state<HTMLDivElement>();
 let diagram=$state<SVGSVGElement>()!;
 // Both diagrams draw the current selection, not the whole snapshot: a chart that
 // ignored a filter would contradict the table beside it. See selection.ts.
 const selection=$derived(select(data,route));
 const apps=$derived(selection.apps);
 const runtimes=$derived([...new Set(apps.map(app=>app.runtime))]);
 const groups=$derived([{label:"PROJECT TYPES",count:selection.projectTypes.length,detail:"Marker files detect project shape"},{label:"TOOL DEFINITIONS",count:selection.tools.length,detail:"Fix and lint operations declare scope"},{label:"MANAGED APPS",count:apps.length,detail:"Executables grouped by runtime"},{label:"MANAGED FILES",count:selection.managedConfigs.length,detail:"Files explicitly associated with tools"}]);
 const filtered=$derived(Boolean(route.project||route.runtime||route.q.trim()));
 function exportSVG(){
  const copy=diagram.cloneNode(true) as SVGSVGElement;
  const source=[diagram,...diagram.querySelectorAll("*")];const target=[copy,...copy.querySelectorAll("*")];
  source.forEach((node,i)=>{const computed=getComputedStyle(node);for(const key of ["fill","stroke","font-family","font-size","font-weight","letter-spacing"]){target[i]?.setAttribute(key,computed.getPropertyValue(key));}});
  copy.setAttribute("xmlns","http://www.w3.org/2000/svg");download(new XMLSerializer().serializeToString(copy),`${route.diagram}.svg`,"image/svg+xml");
 }
</script>
<section class="view" aria-label="Blueprints">
 <div class="intro"><div><p class="eyebrow">THE CONFIG, DRAWN OUT</p><h1>See the structure.<br/><em>Follow the connections.</em></h1></div><p class="muted intro-note">From project shape to configured tools.<br/>A portable map of this configuration.</p></div>
 <div class="row blueprint-toolbar"><div class="segmented" role="group" aria-label="Blueprint diagram">{#each ["configuration","runtimes"] as name}<button aria-pressed={route.diagram===name} onclick={()=>route.diagram=name}>{name}</button>{/each}</div><button onclick={exportSVG}>↓ Download SVG</button></div>
 <div class="blueprint-panel"><div class="blueprint-heading"><p class="eyebrow">{route.diagram==="configuration"?"01 / CONFIGURATION":"02 / RUNTIME LANDSCAPE"}</p><h2>{route.diagram==="configuration"?"One config. A connected toolchain.":"Every app has a home."}</h2></div>
  <div bind:this={viewport} class="blueprint-viewport" role="region" aria-label="Scrollable configuration diagram">
   <svg bind:this={diagram} viewBox={route.diagram==="configuration"?"0 0 1160 510":`0 0 1160 ${Math.max(360,runtimes.length*100+70)}`} role="img" aria-label={`${route.diagram} diagram`}>
    <rect width="100%" height="100%" fill="var(--background)"/>
    {#if route.diagram==="configuration"}
     <path d="M580 122 V185 M145 185 H1015 M145 185 V235 M435 185 V235 M725 185 V235 M1015 185 V235" fill="none" stroke="var(--accent)" stroke-width="1.5"/>
     <rect x="410" y="35" width="340" height="88" rx="16" fill="var(--raised)" stroke="var(--accent)"/>
     <text x="580" y="74" text-anchor="middle" class="diagram-title">datamitsu config</text><text x="580" y="101" text-anchor="middle" class="diagram-caption">RESOLVED SNAPSHOT</text>
     {#each groups as group,index}<g transform={`translate(${index*290+20},235)`}><rect width="250" height="158" rx="14" fill="var(--panel)" stroke="var(--line)"/><text x="22" y="36" class="diagram-caption">{group.label}</text><text x="22" y="103" class="diagram-number">{group.count}</text></g>{/each}
     <text x="580" y="456" text-anchor="middle" class="diagram-caption">Project types → tools → apps · Managed files declare tool ownership</text>
    {:else}
     {#each runtimes as runtime,index}{@const count=apps.filter(app=>app.runtime===runtime).length}<g transform={`translate(40,${30+index*100})`}>
      <rect width="1080" height="80" rx="12" fill="var(--panel)" stroke="var(--line)"/><circle cx="30" cy="40" r="5" fill={runtimeColor(runtime)}/><text x="48" y="46" class="diagram-title">{runtimeNames[runtime]??runtime}</text>
      <rect x="240" y="29" width={Math.max(4,count/Math.max(1,apps.length)*690)} height="22" rx="4" fill={runtimeColor(runtime)}/><text x="1020" y="47" text-anchor="end" class="diagram-title">{count}</text>
     </g>{/each}
    {/if}
   </svg>
  </div><div class="diagram-pan"><span>Swipe to explore the diagram</span><div><button aria-label="Pan diagram left" onclick={()=>viewport?.scrollBy({left:-280,behavior:"smooth"})}>←</button><button aria-label="Pan diagram right" onclick={()=>viewport?.scrollBy({left:280,behavior:"smooth"})}>→</button></div></div><div class="blueprint-caption"><span>{route.diagram==="configuration"?(filtered?"Counts are what the current filters select.":"Counts come directly from the resolved configuration."):"Bar lengths show each runtime’s share of managed apps."}</span><span>Static SVG · No external assets</span></div>
 </div>
 {#if route.diagram==="runtimes"}<div class="runtime-cards">{#each runtimes as runtime}<article class="tool-card"><h3 style:color={runtimeColor(runtime)}>{runtimeNames[runtime]??runtime}</h3><p class="muted">{apps.filter(app=>app.runtime===runtime).map(app=>app.name).join(" · ")}</p></article>{/each}</div>{/if}
</section>
