<script lang="ts">
  import { app } from '../lib/store.svelte';
  import Combobox from './Combobox.svelte';
  import Popover from './Popover.svelte';

  let { value, onchange, readonly = false, always = false, pill = false }: { value: string | null; onchange: (v: string | null) => void; readonly?: boolean; always?: boolean; pill?: boolean } = $props();

  let open = $state(false);
  let btn = $state<HTMLButtonElement>();
  const current = $derived(app.types.find((t) => t.slug === value));
  // "No type" is only offered when there is a type to clear.
  const items = $derived([...app.types.map((t) => ({ value: t.slug, label: t.name, color: t.color })), ...(value ? [{ value: '', label: 'No type' }] : [])]);
</script>

<button class="cell-btn" bind:this={btn} disabled={readonly} aria-label="Type: {current?.name ?? 'none'}" aria-haspopup="listbox" aria-expanded={open}
  onclick={(e) => { e.stopPropagation(); open = !open; }}>
  {#if current}
    <span class="pill"><span class="dot" style="--dot:{current.color}"></span>{current.name}</span>
  {:else if pill}
    <span class="pill unset">Set type</span>
  {:else}
    <span class="muted add" class:always>{readonly ? (always ? 'None' : '') : always ? 'Set type' : '+ type'}</span>
  {/if}
</button>

{#if open && btn}
  <Popover anchor={btn} onclose={() => (open = false)}>
    <Combobox {items} selected={[value ?? '']} placeholder="Improvement, feature or bug…" typeFirst={false} onclose={() => (open = false)}
      onpick={(v) => { open = false; btn?.focus(); const next = v === '' ? null : v; if (next !== value) onchange(next); }} />
  </Popover>
{/if}

<style>
  .cell-btn { display: flex; align-items: center; width: 100%; max-width: 100%; height: 100%; padding: 0; border: 0; background: transparent; text-align: left; overflow: hidden; }
  .cell-btn:not(:disabled):hover .pill { background: var(--bg-hover); box-shadow: inset 0 0 0 1px var(--line-strong); }
  .cell-btn:not(:disabled):hover:has(.add) { background: var(--bg-hover); border-radius: 6px; }
  .add { opacity: 0; font-size: 12px; padding: 2px 4px; white-space: nowrap; }
  .cell-btn:hover .add, .cell-btn:focus-visible .add { opacity: 1; }
  .add.always { opacity: 1; font-size: 13px; padding: 0; }
</style>
