<script lang="ts">
  import type { Snippet } from 'svelte';
  import Combobox, { type Item } from './Combobox.svelte';
  import Popover from './Popover.svelte';

  let {
    value, items, onchange, readonly = false, label, placeholder = 'Search…', empty = '', invalid = false, open = $bindable(false), children,
  }: {
    value: string;
    items: Item[];
    onchange: (v: string) => void;
    readonly?: boolean;
    label: string;
    placeholder?: string;
    /** Text shown while nothing is chosen (for required fields with no default). */
    empty?: string;
    invalid?: boolean;
    open?: boolean;
    children?: Snippet<[Item | undefined]>;
  } = $props();

  let btn = $state<HTMLButtonElement>();
  const current = $derived(items.find((i) => i.value === value));
</script>

<button class="cell-btn" bind:this={btn} disabled={readonly} aria-label="{label}: {current?.label ?? (empty ? 'not chosen' : value)}" aria-haspopup="listbox" aria-expanded={open}
  onclick={(e) => { e.stopPropagation(); open = !open; }}>
  {#if children}{@render children(current)}{:else}
    <span class="pill" class:unset={!current && !!empty} class:invalid>{#if current?.color}<span class="dot" style="--dot:{current.color}"></span>{/if}{current?.label ?? (empty || value)}</span>
  {/if}
</button>

{#if open && btn}
  <Popover anchor={btn} onclose={() => (open = false)}>
    <Combobox {items} selected={[value]} {placeholder} onclose={() => (open = false)}
      onpick={(v) => { open = false; btn?.focus(); if (v !== value) onchange(v); }} />
  </Popover>
{/if}

<style>
  .cell-btn { display: flex; align-items: center; max-width: 100%; height: 100%; padding: 0; border: 0; background: transparent; text-align: left; overflow: hidden; }
  .cell-btn:not(:disabled):hover .pill { background: var(--bg-hover); box-shadow: inset 0 0 0 1px var(--line-strong); }
</style>
