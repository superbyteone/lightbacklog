<script lang="ts">
  export interface Item {
    value: string;
    label: string;
    color?: string;
    hint?: string;
  }

  let {
    items,
    selected = [],
    multiple = false,
    placeholder = 'Search…',
    onpick,
    oncreate,
    onclose,
    typeFirst = true,
  }: {
    items: Item[];
    selected?: string[];
    multiple?: boolean;
    placeholder?: string;
    onpick: (value: string) => void;
    oncreate?: (text: string) => void;
    onclose?: () => void;
    /** On touch devices a long list still focuses the search box at once (raising the keyboard); pass false to leave it for the person to tap. */
    typeFirst?: boolean;
  } = $props();

  let text = $state('');
  let index = $state(0);
  let input = $state<HTMLInputElement>();
  let listEl = $state<HTMLDivElement>();

  const shown = $derived.by(() => {
    const q = text.trim().toLowerCase();
    if (!q) return items;
    const starts = items.filter((i) => i.label.toLowerCase().startsWith(q) || i.hint?.toLowerCase().startsWith(q));
    const rest = items.filter((i) => !starts.includes(i) && (i.label.toLowerCase().includes(q) || i.hint?.toLowerCase().includes(q)));
    return [...starts, ...rest];
  });
  const canCreate = $derived(!!oncreate && text.trim() !== '' && !items.some((i) => i.label.toLowerCase() === text.trim().toLowerCase()));
  const total = $derived(shown.length + (canCreate ? 1 : 0));

  // On touch devices focusing the search box raises the on-screen keyboard, which covers most of the
  // screen; a short list is quicker to tap than to search, so leave the box unfocused there (the keyboard
  // then appears only when the person taps the search box). Long lists focus it unless typeFirst is false.
  $effect(() => {
    const touch = typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches;
    if (!touch || (typeFirst && items.length > 8)) input?.focus();
  });
  $effect(() => {
    text;
    index = 0;
  });
  $effect(() => {
    listEl?.querySelector<HTMLElement>('[data-active="true"]')?.scrollIntoView({ block: 'nearest' });
    index;
  });

  function choose(i: number) {
    if (i < shown.length) onpick(shown[i].value);
    else if (canCreate) {
      oncreate!(text.trim());
      text = '';
    }
  }

  function key(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      index = total ? (index + 1) % total : 0;
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      index = total ? (index - 1 + total) % total : 0;
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (total) choose(index);
    } else if (e.key === 'Tab') {
      onclose?.();
    }
  }
</script>

<input class="cb-input" bind:this={input} bind:value={text} {placeholder} onkeydown={key} aria-label={placeholder} role="combobox" aria-expanded="true" aria-controls="cb-list" />
<div class="cb-list" id="cb-list" role="listbox" bind:this={listEl}>
  {#each shown as item, i (item.value)}
    <button
      type="button" role="option" class="cb-item" class:active={i === index} data-active={i === index}
      aria-selected={selected.includes(item.value)} onmouseenter={() => (index = i)} onclick={() => choose(i)}
    >
      {#if multiple}<span class="cb-check">{selected.includes(item.value) ? '✓' : ''}</span>{/if}
      {#if item.color}<span class="dot" style="--dot:{item.color}"></span>{/if}
      <span class="cb-label">{item.label}</span>
      {#if item.hint}<span class="muted cb-hint">{item.hint}</span>{/if}
      {#if !multiple && selected.includes(item.value)}<span class="cb-tick">✓</span>{/if}
    </button>
  {/each}
  {#if canCreate}
    <button type="button" role="option" class="cb-item" class:active={index === shown.length} data-active={index === shown.length} aria-selected="false"
      onmouseenter={() => (index = shown.length)} onclick={() => choose(shown.length)}>
      <span class="cb-check">+</span><span class="cb-label">Create “{text.trim()}”</span>
    </button>
  {/if}
  {#if total === 0}<div class="muted cb-empty">No matches</div>{/if}
</div>

<style>
  .cb-input { flex: none; width: 100%; height: 36px; padding: 0 12px; border: 0; border-bottom: 1px solid var(--line); background: transparent; outline: none; }
  .cb-list { overflow: auto; padding: 4px; min-height: 0; }
  .cb-item { display: flex; align-items: center; gap: 8px; width: 100%; min-height: 30px; padding: 4px 8px; border: 0; border-radius: 6px; background: transparent; text-align: left; }
  .cb-item.active { background: var(--bg-hover); }
  .cb-label { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .cb-hint { font-size: 12px; }
  .cb-check { width: 14px; color: var(--accent); font-weight: 700; text-align: center; }
  .cb-tick { color: var(--accent); }
  .cb-empty { padding: 10px; text-align: center; }
</style>
