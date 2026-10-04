<script lang="ts">
  import { app } from '../lib/store.svelte';
  import type { Label } from '../lib/types';
  import Combobox from './Combobox.svelte';
  import Popover from './Popover.svelte';

  let { labels, onchange, readonly = false, max = 3, always = false, plain = false, fit = false }: { labels: Label[]; onchange: (names: string[]) => void; readonly?: boolean; max?: number; always?: boolean; plain?: boolean; fit?: boolean } = $props();

  let open = $state(false);
  let btn = $state<HTMLButtonElement>();
  let probe = $state<HTMLDivElement>();
  // fit mode (table column): show as many labels as the cell width allows, then "+N" for the rest
  let fitCount = $state(Infinity);
  const shown = $derived(fit ? Math.min(fitCount, labels.length) : max);

  function measure() {
    if (!fit || !btn || !probe) return;
    const avail = btn.clientWidth;
    const chips = Array.from(probe.querySelectorAll<HTMLElement>('[data-chip]'));
    const more = probe.querySelector<HTMLElement>('[data-more]');
    const gap = 4;
    const moreW = (more?.offsetWidth ?? 0) + gap;
    let used = 0;
    let n = 0;
    for (let i = 0; i < chips.length; i++) {
      const w = chips[i].offsetWidth + (i > 0 ? gap : 0);
      const rest = chips.length - i - 1;
      // the last label needs no "+N"; earlier ones must leave room for it
      if (used + w + (rest > 0 ? moreW : 0) > avail) break;
      used += w;
      n = i + 1;
    }
    fitCount = Math.max(1, n);
  }

  $effect(() => {
    if (!fit || !btn) return;
    void labels.map((l) => l.name).join('\u0000');
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(btn);
    return () => ro.disconnect();
  });
  const names = $derived(labels.map((l) => l.name));
  const items = $derived([...app.labels].sort((a, b) => a.name.localeCompare(b.name)).map((l) => ({ value: l.name, label: l.name, color: l.color })));

  function toggle(name: string) {
    const has = names.some((n) => n.toLowerCase() === name.toLowerCase());
    onchange(has ? names.filter((n) => n.toLowerCase() !== name.toLowerCase()) : [...names, name]);
  }
</script>

<button class="cell-btn" class:plain bind:this={btn} disabled={readonly} aria-label="Labels: {names.join(', ') || 'none'}" aria-haspopup="listbox" aria-expanded={open}
  onclick={(e) => { e.stopPropagation(); open = !open; }}>
  {#if fit}
    <div class="probe" bind:this={probe} aria-hidden="true">
      {#each labels as l (l.id)}<span class="chip" data-chip>{l.name}</span>{/each}
      <span class="muted more" data-more>+{labels.length}</span>
    </div>
  {/if}
  {#each labels.slice(0, shown) as l (l.id)}
    {#if plain}<span class="ltext" style="--chip:{l.color}"><span class="hash" aria-hidden="true">#</span>{l.name}</span>{:else}<span class="chip" style="--chip:{l.color}">{l.name}</span>{/if}
  {/each}
  {#if labels.length > shown}<span class="muted more">+{labels.length - shown}</span>{/if}
  {#if labels.length === 0}<span class="muted add" class:always>{readonly ? (always ? 'None' : '') : '+ Add label'}</span>{/if}
</button>

{#if open && btn}
  <Popover anchor={btn} onclose={() => (open = false)}>
    <Combobox {items} selected={names.map((n) => items.find((i) => i.label.toLowerCase() === n.toLowerCase())?.value ?? n)} multiple placeholder="Find or create a label…" typeFirst={false}
      onclose={() => (open = false)} onpick={toggle} oncreate={(t) => toggle(t)} />
  </Popover>
{/if}

<style>
  .cell-btn { display: flex; align-items: center; gap: 4px; width: 100%; height: 100%; padding: 0; border: 0; background: transparent; text-align: left; overflow: hidden; }
  .cell-btn:not(:disabled):hover { background: var(--bg-hover); border-radius: 6px; }
  .add { opacity: 0; font-size: 12px; }
  .add.always { opacity: 1; font-size: 13px; }
  .cell-btn:hover .add, .cell-btn:focus-visible .add { opacity: 1; }
  .more { font-size: 12px; flex: none; }
  .cell-btn { position: relative; }
  .probe { position: absolute; visibility: hidden; pointer-events: none; display: flex; gap: 4px; white-space: nowrap; left: 0; top: 0; }
  /* Cards view: labels as plain coloured text (no pill); the empty "+ Add label" fades in only when the pointer is over its own spot */
  .plain { gap: 10px; width: auto; }
  .plain:not(:disabled):hover { background: transparent; }
  .ltext { flex: none; font-size: 12px; font-weight: 400; color: var(--chip, var(--muted)); white-space: nowrap; }
  .plain .more { color: #9aa0ab; }
  .plain .add { color: #9aa0ab; transition: opacity 120ms ease; }
</style>
