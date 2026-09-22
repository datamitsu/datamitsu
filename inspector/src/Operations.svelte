<script lang="ts">
 import {select} from "./selection";
 import DetailDrawer from "./DetailDrawer.svelte";
 import ToolInspector from "./ToolInspector.svelte";
 import {runtimeColor,type Manifest,type Route} from "./model";
 let {data,route=$bindable(),showApp,copy}:{data:Manifest;route:Route;showApp:(name:string)=>void;copy:(text:string)=>void}=$props();
 let drawer=$state<DetailDrawer>();
 function selectTool(id:string){route.tool=id;drawer?.open();}
 // The same selection every other view reads. See selection.ts.
 const selection=$derived(select(data,route));
 const tools=$derived(selection.tools);
 const selected=$derived(tools.find(tool=>tool.id===route.tool)??tools.find(tool=>tool.id==="eslint")??tools[0]);
 const enabled=$derived(tools.filter(tool=>!tool.skipped).length);
 const appRuntime=(app:string)=>data.apps.find(a=>a.name===app)?.runtime??"unknown";
</script>
<section class="view" aria-label="Operations">
 <div class="intro"><div><p class="eyebrow">THE CONFIG INSPECTOR</p><h1>Every tool.<br/><em>Nothing hidden.</em></h1></div>
  <div class="overview"><div class="metrics"><div><strong>{tools.length}</strong><span>matching tools</span></div><div class="enabled"><strong>{enabled}</strong><span>enabled</span></div><div class="skipped"><strong>{tools.length-enabled}</strong><span>skipped</span></div></div>
   <div class="status-meter" style:--enabled-share={`${tools.length?enabled/tools.length*100:0}%`}></div><p>Select a tool to explore its operations, patterns, and managed files.</p>
  </div>
 </div>
 <div class="inspector-filters">
  <div class="segmented" role="group" aria-label="Tool status">{#each ["all","enabled","skipped"] as status}<button aria-pressed={route.status===status} onclick={()=>route.status=status}>{status==="all"?"All tools":status}</button>{/each}</div>
  <div class="segmented" role="group" aria-label="Operation kind">{#each ["all","fix","lint"] as op}<button aria-pressed={route.operation===op} onclick={()=>route.operation=op}>{op==="all"?"Any operation":op}</button>{/each}</div>
  <div class="segmented list-mode" role="group" aria-label="Tool display">{#each ["table","cards"] as mode}<button aria-pressed={route.list===mode} onclick={()=>route.list=mode}>{mode==="table"?"☷ Table":"⊞ Cards"}</button>{/each}</div>
 </div>
 <div class="inspector-grid"><div class="tool-list">
  {#if !tools.length}<div class="empty"><h2>No matches.</h2><p>Try a different search or reset the filters.</p></div>
  {:else if route.list==="table"}
   <div class="table-scroll"><table><caption>Configured tools · F: fix · L: lint</caption><thead><tr><th>Tool</th><th>Fix</th><th>Lint</th><th>Scope</th><th>Snapshot status</th></tr></thead>
    <tbody>{#each tools as tool}<tr class:selected={selected?.id===tool.id}>
     <td><span class="runtime-dot" style:--runtime-color={runtimeColor(appRuntime(tool.operations[0]?.app??""))}></span><button class="tool-name" aria-pressed={selected?.id===tool.id} onclick={()=>selectTool(tool.id)}>{tool.id}</button></td>
     {#each ["fix","lint"] as kind}<td><span class="badge {tool.operations.some(op=>op.kind===kind)?kind:'absent'}" aria-label={`${kind} ${tool.operations.some(op=>op.kind===kind)?'declared':'not declared'}`}>{tool.operations.some(op=>op.kind===kind)?"✓":"—"}</span></td>{/each}
     <td class="muted">{[...new Set(tool.operations.map(op=>op.scope))].join(", ")}</td><td><span class="badge {tool.skipped?'skipped':'enabled'}">{tool.skipped?"Skipped":"Enabled"}</span></td>
    </tr>{/each}</tbody></table></div>
  {:else}<div class="tool-cards">{#each tools as tool,index}<article class="tool-card" class:selected={selected?.id===tool.id}>
   <div class="row"><div><span class="runtime-dot" style:--runtime-color={runtimeColor(appRuntime(tool.operations[0]?.app??""))}></span><button class="tool-name" aria-pressed={selected?.id===tool.id} onclick={()=>selectTool(tool.id)}>{tool.id}</button></div><span class="card-index">{String(index+1).padStart(2,"0")}</span></div>
   <p class="muted">{tool.name}</p><div class="chips">{#each tool.operations as op}<span class="badge {op.kind}">{op.kind}</span>{/each}<span class="badge {tool.skipped?'skipped':'enabled'}">{tool.skipped?"Skipped":"Enabled"}</span></div>
   <p class="mono muted">{tool.projectTypes.join(" · ")||"All project types"}</p>
  </article>{/each}</div>{/if}
 </div><DetailDrawer bind:this={drawer} selection={route.tool} title={selected?.id??"tool"}><ToolInspector {data} tool={selected} bind:route {showApp} {copy}/></DetailDrawer></div>
 <p class="footnote" role="status">{tools.length} of {selection.totals.tools} tools. Project filters show declared applicability; file patterns still decide which files match.</p>
</section>
