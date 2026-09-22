<script lang="ts">
  import type { Manifest } from "./model";
  let { choose, data }: { choose: (kind: string, value: string) => void; data: Manifest } =
    $props();
  let dialog = $state<HTMLDialogElement>()!;
  let input = $state<HTMLInputElement>()!;
  let results = $state<HTMLDivElement>()!;
  let query = $state("");
  const entries = $derived([
    ...data.tools.map((t) => ({ description: t.name, kind: "tool", label: t.id })),
    ...data.apps.map((a) => ({ description: a.runtime, kind: "app", label: a.name })),
    ...data.projectTypes.map((p) => ({ description: p.description, kind: "project", label: p.id })),
  ]);
  const matches = $derived(
    entries
      .filter((entry) =>
        `${entry.label} ${entry.description}`.toLowerCase().includes(query.trim().toLowerCase()),
      )
      .slice(0, 30),
  );
  function open() {
    query = "";
    dialog.showModal();
    input.focus();
  }
  function keyboard(event: KeyboardEvent) {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
      event.preventDefault();
      if (dialog.open) {
        dialog.close();
      } else {
        open();
      }
    }
    if (
      event.key === "/" &&
      !dialog.open &&
      !document.activeElement?.matches("input,textarea,select,[contenteditable]")
    ) {
      event.preventDefault();
      open();
    }
  }
  function navigate(event: KeyboardEvent) {
    if (event.key === "Escape") {
      event.preventDefault();
      dialog.close();
      return;
    }
    const buttons = [...results.querySelectorAll("button")];
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    if (event.key === "ArrowDown") {
      event.preventDefault();
      buttons[Math.min(index + 1, buttons.length - 1)]?.focus();
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      if (index <= 0) {
        input.focus();
      } else {
        buttons[index - 1]?.focus();
      }
    }
    if (event.key === "Enter" && document.activeElement === input) {
      event.preventDefault();
      buttons[0]?.click();
    }
  }
</script>

<svelte:window onkeydown={keyboard} />
<button class="quick-trigger" aria-label="Quick find (Command K or Control K)" onclick={open}
  >⌕ <span>Quick find</span><kbd>⌘ K</kbd></button
>
<dialog
  bind:this={dialog}
  class="quick-dialog"
  aria-labelledby="quick-heading"
  onkeydown={navigate}
>
  <div class="row">
    <h2 id="quick-heading">Jump into the config</h2>
    <button aria-label="Close search" onclick={() => dialog.close()}>Esc</button>
  </div>
  <input
    bind:this={input}
    bind:value={query}
    type="search"
    placeholder="Search apps, tools, project types…"
    aria-label="Search configuration"
  />
  <div class="quick-results" bind:this={results}>
    {#each matches as entry (entry.kind + "/" + entry.label)}<button
        class="quick-result"
        onclick={() => {
          dialog.close();
          choose(entry.kind, entry.label);
        }}
        ><span class="quick-icon"
          >{entry.kind === "tool" ? "⌘" : entry.kind === "app" ? "◉" : "◇"}</span
        ><span class="quick-title"
          ><strong>{entry.label}</strong><span>{entry.description}</span></span
        ><span class="mono muted">{entry.kind}</span></button
      >{:else}<p class="empty">No matches. Try another search.</p>{/each}
  </div>
  <div class="quick-footer">
    <span>Showing {matches.length} results</span><span>↑ ↓ Navigate · ↵ Open · Esc Close</span>
  </div>
</dialog>
