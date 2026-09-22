<script lang="ts">
  import { onMount, tick, type Snippet } from "svelte";

  let { children, selection, title }: { children: Snippet; selection: string; title: string } = $props();
  let isCompact = $state(false);
  let dialog = $state<HTMLDialogElement>();

  export async function open() {
    await tick();
    if (dialog && !dialog.open) {
      dialog.showModal();
    }
  }

  onMount(() => {
    const media = matchMedia("(max-width: 1000px)");
    const update = () => { isCompact = media.matches; };
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  });

  $effect(() => {
    if (selection && isCompact) {
      open();
    }
  });
</script>

<div class="detail-container">
  {#if isCompact}
    <button class="detail-trigger" onclick={open}><span>Inspect <strong>{title}</strong></span><span aria-hidden="true">↗</span></button>
    <dialog bind:this={dialog} class="inspector-drawer" aria-label={`${title} details`}>
      <div class="drawer-heading"><span class="eyebrow">INSPECTOR / DETAILS</span><button onclick={() => dialog?.close()} aria-label="Close inspector">✕ <span>Close</span></button></div>
      {@render children()}
    </dialog>
  {:else}
    {@render children()}
  {/if}
</div>
