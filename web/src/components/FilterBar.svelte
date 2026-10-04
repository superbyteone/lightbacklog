<script lang="ts">
  import { untrack } from 'svelte';
  import { writeFilters, type Filters } from '../lib/filters';
  import { setParams } from '../lib/router.svelte';
  import { app } from '../lib/store.svelte';
  import { currentMode, layout, setMode } from '../lib/layout.svelte';
  import ColumnsMenu from './ColumnsMenu.svelte';
  import Combobox from './Combobox.svelte';
  import Popover from './Popover.svelte';

  let { filters, lockedProject = false, hideStatus = false, showColumns = false, filtered, total }: { filters: Filters; lockedProject?: boolean; hideStatus?: boolean; showColumns?: boolean; filtered: boolean; total: number | null } = $props();
  const mode = $derived((layout.viewport, layout.modes, currentMode()));
  let columnsOpen = $state(false);
  let columnsBtn = $state<HTMLButtonElement>();

  type Key = 'project' | 'status' | 'priority' | 'type' | 'label';
  let openKey = $state<Key | null>(null);
  let anchors = $state<Record<string, HTMLButtonElement | undefined>>({});
  let search = $state(untrack(() => filters.q));
  let timer: ReturnType<typeof setTimeout>;

  $effect(() => {
    // keep the box in sync when the URL changes (back/forward, clear)
    if (filters.q !== search && document.activeElement?.id !== 'filter-q') search = filters.q;
  });

  function setQ(v: string) {
    search = v;
    clearTimeout(timer);
    timer = setTimeout(() => setParams((p) => writeFilters(p, { q: v.trim() })), 220);
  }

  const defs = $derived([
    ...(lockedProject ? [] : [{ key: 'project' as Key, label: 'Project', items: app.projects.map((p) => ({ value: p.key, label: p.name, color: p.color, hint: p.key })) }]),
    ...(hideStatus ? [] : [{ key: 'status' as Key, label: 'Status', items: app.statuses.map((s) => ({ value: s.slug, label: s.name, color: s.color })) }]),
    { key: 'priority' as Key, label: 'Priority', items: app.priorities.map((s) => ({ value: s.slug, label: s.name, color: s.color })) },
    { key: 'type' as Key, label: 'Type', items: [...app.types.map((s) => ({ value: s.slug, label: s.name, color: s.color })), { value: 'none', label: 'No type' }] },
    { key: 'label' as Key, label: 'Label', items: app.labels.map((l) => ({ value: l.name, label: l.name, color: l.color })) },
  ]);

  function toggle(key: Key, value: string) {
    const cur = filters[key];
    const next = cur.includes(value) ? cur.filter((v) => v !== value) : [...cur, value];
    setParams((p) => writeFilters(p, { [key]: next }));
  }

  function summary(key: Key, items: { value: string; label: string }[]) {
    const sel = filters[key];
    if (sel.length === 0) return '';
    if (sel.length === 1) return items.find((i) => i.value.toLowerCase() === sel[0].toLowerCase())?.label ?? sel[0];
    return `${sel.length} selected`;
  }

  function clear() {
    search = '';
    setParams((p) => writeFilters(p, { project: lockedProject ? undefined : [], status: [], priority: [], type: [], label: [], q: '', done: false }));
  }
</script>

<div class="bar" role="search">
  <input id="filter-q" class="input q" type="search" placeholder="Filter tasks…  (press /)" value={search} oninput={(e) => setQ(e.currentTarget.value)} aria-label="Search tasks" />
  {#each defs as d (d.key)}
    {@const s = summary(d.key, d.items)}
    <button class="btn filter" class:on={s} bind:this={anchors[d.key]} aria-haspopup="listbox" aria-expanded={openKey === d.key} onclick={() => (openKey = openKey === d.key ? null : d.key)}>
      {d.label}{#if s}<b>: {s}</b>{/if}
    </button>
    {#if openKey === d.key && anchors[d.key]}
      <Popover anchor={anchors[d.key]!} onclose={() => (openKey = null)}>
        <Combobox items={d.items} selected={filters[d.key].map((v) => d.items.find((i) => i.value.toLowerCase() === v.toLowerCase())?.value ?? v)} multiple placeholder="Filter by {d.label.toLowerCase()}…"
          onclose={() => (openKey = null)} onpick={(v) => toggle(d.key, v)} />
      </Popover>
    {/if}
  {/each}
  {#if !hideStatus}<label class="done"><input type="checkbox" checked={filters.done} onchange={(e) => setParams((p) => writeFilters(p, { done: e.currentTarget.checked }))} /> Show done</label>{/if}
  {#if filtered}<button class="btn ghost" onclick={clear}>Clear</button>{/if}
  <span class="spacer"></span>
  {#if showColumns}
    <div class="seg" role="radiogroup" aria-label="Display">
      <button role="radio" aria-checked={mode === 'list'} class:on={mode === 'list'} title="Table" onclick={() => setMode('list')}>☰ List</button>
      <button role="radio" aria-checked={mode === 'cards'} class:on={mode === 'cards'} title="Cards" onclick={() => setMode('cards')}>▦ Cards</button>
    </div>
    <!-- Both labels are laid out in the same grid cell so the button is always as wide as the longer one and the List/Cards toggle beside it never shifts. -->
    <button class="btn cols" bind:this={columnsBtn} aria-haspopup="dialog" aria-expanded={columnsOpen} onclick={() => (columnsOpen = !columnsOpen)}>
      <span class:hid={mode !== 'list'}>Columns</span><span class:hid={mode !== 'cards'}>Fields</span>
    </button>
    {#if columnsOpen && columnsBtn}
      <Popover anchor={columnsBtn} width={250} maxHeight={560} onclose={() => (columnsOpen = false)}><ColumnsMenu cards={mode === 'cards'} {lockedProject} /></Popover>
    {/if}
  {/if}
  {#if total !== null}<span class="muted count">{total.toLocaleString()} {total === 1 ? 'task' : 'tasks'}</span>{/if}
</div>

<style>
  .cols { display: inline-grid; justify-items: center; }
  .cols > span { grid-area: 1 / 1; }
  .cols > .hid { visibility: hidden; }
  .bar { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding: 10px 16px; border-bottom: 1px solid var(--line); }
  .q { width: 220px; }
  .seg { display: inline-flex; padding: 2px; border: 1px solid var(--line-strong); border-radius: 8px; background: var(--bg-soft); }
  .seg button { height: 26px; padding: 0 10px; border: 0; border-radius: 6px; background: transparent; color: var(--muted); font-weight: 600; white-space: nowrap; }
  .seg button.on { background: var(--bg); color: var(--fg); box-shadow: 0 1px 2px rgba(16, 24, 40, 0.12); }
  .filter.on { background: var(--accent-soft); border-color: var(--accent); }
  .filter b { font-weight: 600; max-width: 140px; overflow: hidden; text-overflow: ellipsis; }
  .done { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); font-size: 13px; user-select: none; }
  .count { font-size: 13px; }
</style>
