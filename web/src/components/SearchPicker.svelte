<script lang="ts">
  export interface PickItem {
    value: string;
    label: string;
    color?: string;
    hint?: string;
  }

  /**
   * Inline "search and pick many" field used in the filter sheet for labels and projects: suggestions
   * appear while typing (capped, so hundreds of labels or projects stay fast) and what is chosen shows
   * as removable chips underneath.
   */
  let { label, placeholder, items, selected, ontoggle, max = 8 }: {
    label: string;
    placeholder: string;
    items: PickItem[];
    selected: string[];
    ontoggle: (value: string) => void;
    max?: number;
  } = $props();

  let text = $state('');
  let index = $state(0);
  let input = $state<HTMLInputElement>();
  let listEl = $state<HTMLDivElement>();
  const uid = $props.id();

  const q = $derived(text.trim().toLowerCase());
  const same = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();
  const isOn = (v: string) => selected.some((s) => same(s, v));
  const matches = $derived.by(() => {
    if (!q) return [];
    const starts: PickItem[] = [], rest: PickItem[] = [];
    for (const i of items) {
      const l = i.label.toLowerCase();
      if (l.startsWith(q) || i.hint?.toLowerCase().startsWith(q)) starts.push(i);
      else if (l.includes(q) || i.hint?.toLowerCase().includes(q)) rest.push(i);
      if (starts.length >= max) break;
    }
    return [...starts, ...rest].slice(0, max);
  });
  const chosen = $derived(selected.map((v) => items.find((i) => same(i.value, v)) ?? { value: v, label: v }));
  $effect(() => {
    q;
    index = 0;
  });
  // the suggestions float over the rest of the sheet: scroll them fully into view
  $effect(() => {
    if (listEl && matches.length) listEl.scrollIntoView({ block: 'nearest' });
  });

  function pick(i: PickItem) {
    ontoggle(i.value);
    text = '';
    input?.focus();
  }
  function key(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') { e.preventDefault(); index = matches.length ? (index + 1) % matches.length : 0; }
    else if (e.key === 'ArrowUp') { e.preventDefault(); index = matches.length ? (index - 1 + matches.length) % matches.length : 0; }
    else if (e.key === 'Enter') { e.preventDefault(); if (matches[index]) pick(matches[index]); }
    else if (e.key === 'Escape' && text) { e.preventDefault(); e.stopPropagation(); text = ''; }
  }
  // Match highlight: the typed part in bold.
  function parts(l: string): [string, string, string] {
    const at = l.toLowerCase().indexOf(q);
    return at < 0 ? [l, '', ''] : [l.slice(0, at), l.slice(at, at + q.length), l.slice(at + q.length)];
  }
</script>

<div class="picker">
  <div class="field" class:focus={false}>
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="6.5" /><path d="m20 20-4.2-4.2" /></svg>
    <input bind:this={input} bind:value={text} {placeholder} role="combobox" aria-expanded={matches.length > 0} aria-controls="{uid}-list" aria-autocomplete="list" aria-label={placeholder}
      autocomplete="off" autocapitalize="off" spellcheck="false" enterkeyhint="done" onkeydown={key} />
    {#if text}<button class="clear" aria-label="Clear {label.toLowerCase()} search" onclick={() => { text = ''; input?.focus(); }}>
      <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg></button>{/if}
  </div>
  {#if q}
    <div class="list" id="{uid}-list" role="listbox" aria-label="{label} suggestions" bind:this={listEl}>
      {#each matches as m, i (m.value)}
        {@const [a, b, c] = parts(m.label)}
        <button class="opt" class:active={i === index} role="option" aria-selected={isOn(m.value)} onclick={() => pick(m)} onpointerenter={() => (index = i)}>
          {#if m.color}<span class="dot" style="--dot:{m.color}"></span>{/if}
          <span class="lbl">{a}<b>{b}</b>{c}</span>
          {#if isOn(m.value)}<svg class="tick" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 12.5 4.5 4.5L19 7.5" /></svg>{/if}
        </button>
      {:else}
        <p class="none muted">No {label.toLowerCase()} matches “{text.trim()}”.</p>
      {/each}
    </div>
  {/if}
  {#if chosen.length}
    <div class="chips" role="list" aria-label="Selected {label.toLowerCase()}s">
      {#each chosen as c (c.value)}
        <span class="chip" role="listitem">
          {#if c.color}<span class="dot" style="--dot:{c.color}"></span>{/if}<span class="cl">{c.label}</span>
          <button aria-label="Remove {c.label}" onclick={() => ontoggle(c.value)}>
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg></button>
        </span>
      {/each}
    </div>
  {/if}
</div>

<style>
  .picker { position: relative; display: grid; gap: 8px; }
  .field { display: flex; align-items: center; gap: 8px; height: 36px; padding: 0 10px; border: 1px solid var(--m-line-strong); border-radius: 8px; background: var(--m-card); color: var(--muted); }
  .field:focus-within { border-color: var(--m-accent); box-shadow: 0 0 0 2px var(--m-accent-soft); }
  /* text inputs stay at 16px: smaller and iOS zooms the page when the field is focused */
  input { flex: 1; min-width: 0; height: 100%; border: 0; background: transparent; color: var(--fg); font-size: 16px; outline: none; }
  .clear { position: relative; display: grid; place-items: center; width: 28px; height: 28px; margin-right: -6px; border: 0; background: transparent; color: var(--muted); }
  .clear::after, .chip button::after { content: ''; position: absolute; inset: -8px; }
  .list { position: absolute; z-index: 5; top: 40px; left: 0; right: 0; max-height: 240px; overflow-y: auto; border: 1px solid var(--m-line-strong); border-radius: 8px; background: var(--m-card); box-shadow: var(--shadow); }
  .opt { display: flex; align-items: center; gap: 8px; width: 100%; min-height: 38px; padding: 0 12px; border: 0; border-bottom: 1px solid var(--m-line); background: transparent; text-align: left; font-size: 14px; }
  .opt:last-child { border-bottom: 0; }
  .opt.active { background: var(--m-accent-soft); }
  .opt .lbl { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .opt b { font-weight: 700; }
  .tick { color: var(--m-accent); flex: none; }
  .none { margin: 0; padding: 10px 12px; font-size: 14px; }
  .chips { display: flex; flex-wrap: wrap; gap: 8px; }
  .chip { display: inline-flex; align-items: center; gap: 6px; height: 32px; padding: 0 4px 0 12px; border: 1px solid var(--m-accent); border-radius: 16px; background: var(--m-accent-soft); color: var(--m-accent); font-size: 14px; max-width: 100%; }
  .chip .cl { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .chip button { position: relative; display: grid; place-items: center; width: 24px; height: 24px; border: 0; border-radius: 50%; background: transparent; color: inherit; }
  .dot { flex: none; width: 8px; height: 8px; }
</style>
