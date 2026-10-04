<script lang="ts">
  import { flushSync, onMount } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
  import { isFiltered, parseFilters, toApiParams, writeFilters } from '../lib/filters';
  import { loc, setParams } from '../lib/router.svelte';
  import { app, cache, errorText, onTasksChanged, setProjectFavorite, TaskList, toast } from '../lib/store.svelte';
  import { openCreate, ui } from '../lib/ui.svelte';
  import Board from './Board.svelte';
  import BulkBar from './BulkBar.svelte';
  import CompactList from './CompactList.svelte';
  import FilterBar from './FilterBar.svelte';
  import FilterSheet from './FilterSheet.svelte';
  import MobileHeader from './MobileHeader.svelte';
  import TaskCards from './TaskCards.svelte';
  import TaskTable from './TaskTable.svelte';
  import { currentMode, layout } from '../lib/layout.svelte';

  let { projectKey }: { projectKey?: string } = $props();

  const params = $derived(new URLSearchParams(loc.search));
  const filters = $derived(parseFilters(params));
  const filtered = $derived(isFiltered(filters, !!projectKey));
  const project = $derived(projectKey ? app.projects.find((p) => p.key.toLowerCase() === projectKey.toLowerCase()) : undefined);
  const view = $derived(projectKey && params.get('view') === 'board' ? 'board' : 'list');
  // The board shows every status (grouped into columns) in manual order; the list applies status filters.
  const apiParams = $derived(view === 'board' ? toApiParams({ ...filters, status: [], done: true, sort: 'position' }, app.statuses, projectKey) : toApiParams(filters, app.statuses, projectKey));
  const activeRef = $derived(params.get('task') ?? undefined);

  const list = new TaskList(200);
  const mode = $derived((layout.viewport, layout.modes, currentMode()));
  /** Phones and tablets get the redesigned list (docs/design/mobile-redesign); desktop keeps the table layout. */
  const compact = $derived(layout.viewport <= 1100);
  const overlay = $derived(compact && view === 'list');
  const canCreate = $derived(!project || (project.role !== 'viewer' && !project.archived));

  // --- selection for bulk actions (list view) ---
  const selection = new SvelteSet<string>();
  let lastClicked: string | null = null;
  const selectedTasks = $derived([...selection].map((id) => cache[id]).filter((t) => !!t));

  function select(id: string, shift: boolean) {
    if (shift && lastClicked && list.ids.includes(lastClicked)) {
      const a = list.ids.indexOf(lastClicked), b = list.ids.indexOf(id);
      for (const x of list.ids.slice(Math.min(a, b), Math.max(a, b) + 1)) selection.add(x);
    } else if (selection.has(id)) selection.delete(id);
    else selection.add(id);
    lastClicked = id;
  }
  function selectAll() {
    if (list.ids.length > 0 && list.ids.every((id) => selection.has(id))) selection.clear();
    else for (const id of list.ids) selection.add(id);
  }
  // Selection only makes sense for what is on screen: drop ids that left the list, and clear when the filters change.
  $effect(() => {
    const ids = new Set(list.ids);
    for (const id of [...selection]) if (!ids.has(id)) selection.delete(id);
  });
  $effect(() => {
    apiParams;
    selection.clear();
    lastClicked = null;
  });

  // --- compact: select mode, filter sheet ---
  let selectMode = $state(false);
  let sheetOpen = $state(false);
  const allSelected = $derived(list.ids.length > 0 && list.ids.every((id) => selection.has(id)));
  const barVisible = $derived(compact && view === 'list' && selectMode && selection.size > 0);
  $effect(() => {
    if (!compact || view !== 'list') {
      selectMode = false;
      selection.clear();
    }
  });
  /** Long press on a task (phones and tablets): enter select mode with that task selected. */
  function startSelecting(id: string) {
    if (view !== 'list' || selectMode) return;
    setSelectMode(true);
    selection.add(id);
  }
  function setSelectMode(on: boolean) {
    selectMode = on;
    if (!on) selection.clear();
  }
  const activeFilters = $derived(filters.priority.length + filters.type.length + filters.label.length + (view === 'board' ? 0 : filters.status.length) + (projectKey ? 0 : filters.project.length));

  // Reload when filters/sort change — but not when only the open-task panel changes.
  $effect(() => {
    const p = apiParams;
    if (app.statuses.length === 0) return;
    void list.reset(p).then(async () => {
      if (view === 'board') for (let i = 0; i < 25 && list.hasMore; i++) await list.more(); // the board needs the whole project
    });
  });

  // --- compact header that scrolls away with the list ---
  // List and cards: the header is laid over the top of the list, which reserves room for it (--fold-h). Scrolling
  // down moves only its menu bar (hamburger + "LightBacklog") up together with the rows until it is gone; the title
  // row (project name, count, Create button) and the Filters / Sort / Select row stay pinned, and the rows carry on
  // scrolling under them (LB-59). Scrolling up brings the menu bar straight back over the list. Only a transform on
  // the header changes while scrolling, so nothing is laid out or re-rendered. The board (which scrolls per column)
  // folds its whole header away in one step instead.
  let collapsed = $state(false);
  let quietUntil = 0;
  let foldEl = $state<HTMLDivElement>();
  let topEl = $state<HTMLElement>();
  let bodyEl = $state<HTMLDivElement>();
  let viewEl = $state<HTMLElement>();
  let foldH = $state(0); // the header's height in px, reserved as the list's top padding (--fold-h)
  let hdrY = 0; // header offset in px: 0 fully shown … -height of the top part fully hidden
  function setCollapsed(v: boolean) {
    if (collapsed === v) return;
    const before = foldEl?.offsetHeight ?? 0;
    collapsed = v;
    quietUntil = Date.now() + 300; // resizing the scroller nudges its scrollTop; don't read that as a gesture
    if (!foldEl || !bodyEl) return;
    // The layout changes once, instantly (animating the height would re-lay-out and re-render the whole
    // virtualised list on every frame). The list then slides from where it was to where it is with a transform.
    flushSync();
    const dy = before - foldEl.offsetHeight;
    if (dy !== 0 && !window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      bodyEl.animate([{ transform: `translateY(${dy}px)` }, { transform: 'translateY(0)' }], { duration: 160, easing: 'ease-out' });
    }
  }
  function setHeader(y: number, scrolled: number) {
    if (!foldEl) return;
    const h = topEl?.offsetHeight ?? 0;
    hdrY = Math.min(0, Math.max(-h, y));
    foldEl.style.transform = hdrY === 0 ? '' : `translateY(${hdrY}px)`;
    if (topEl) topEl.inert = h > 0 && hdrY <= 1 - h;
    foldEl.classList.toggle('lifted', scrolled > 0 && hdrY > -h);
  }
  // Keep the room reserved at the top of the list equal to the header's height (it changes with the mode and
  // wrapping). foldH is plain Svelte state fed into the section's own `style` attribute below (with --tail)
  // rather than written straight to the DOM: that attribute is fully re-rendered on every change, so a second,
  // imperative writer of a custom property on the same element would get silently overwritten whenever only the
  // other one changed (e.g. entering/leaving selection without the header's height itself changing).
  $effect(() => {
    if (!overlay || !foldEl) return;
    const f = foldEl;
    const set = () => (foldH = f.offsetHeight);
    set();
    const ro = new ResizeObserver(set);
    ro.observe(f);
    return () => { ro.disconnect(); foldH = 0; setHeader(0, 0); };
  });
  onMount(() => {
    const last = new WeakMap<EventTarget, number>();
    let travelled = 0; // signed distance since the direction last changed
    const onscroll = (e: Event) => {
      const el = e.target;
      if (!(el instanceof HTMLElement) || el.closest('.popover') || !el.closest('.view')) return;
      const y = el.scrollTop;
      const prev = last.get(el) ?? 0;
      last.set(el, y);
      if (!compact) return;
      if (overlay) {
        if (el.classList.contains('scroll')) setHeader(Math.max(-y, hdrY - (y - prev)), y);
        return;
      }
      if (Date.now() < quietUntil) return;
      if (y <= 8) { travelled = 0; return setCollapsed(false); }
      const d = y - prev;
      travelled = Math.sign(d) === Math.sign(travelled) ? travelled + d : d;
      if (travelled > 24 && y > 80) setCollapsed(true);
      else if (travelled < -12) setCollapsed(false);
    };
    document.addEventListener('scroll', onscroll, true);
    return () => document.removeEventListener('scroll', onscroll, true);
  });
  $effect(() => {
    apiParams; // a new filter/sort/project starts at the top of a fresh list
    collapsed = false;
    setHeader(0, 0);
  });
  $effect(() => {
    selectMode; // the select bar replaces the header: start it from the top
    setHeader(0, 0);
  });
  $effect(() => {
    if (!compact) collapsed = false;
  });
  onMount(() => {
    // Esc clears the selection (unless something else, like a popup or the task panel, should get the key).
    const esc = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      if (document.querySelector('.popover, [role=dialog], aside.panel')) return;
      if (e.target instanceof HTMLElement && e.target.closest('textarea, select, input:not([type=checkbox])')) return; // typing: Esc belongs to the field
      if (selection.size > 0) selection.clear();
      else if (selectMode) selectMode = false;
    };
    document.addEventListener('keydown', esc);
    const off = onTasksChanged(() => void list.refresh().then(async () => { if (view === 'board') for (let i = 0; i < 25 && list.hasMore; i++) await list.more(); }));
    return () => {
      document.removeEventListener('keydown', esc);
      off();
      list.destroy();
    };
  });

  function onsort(field: string) {
    const cur = filters.sort;
    const next = cur === field ? '-' + field : cur === '-' + field ? field : field === 'updated_at' || field === 'completed_at' ? '-' + field : field; // dates: newest first
    setParams((p) => writeFilters(p, { sort: next }));
  }

  function open(ref: string) {
    setParams((p) => p.set('task', ref), { replace: false });
  }

  function clear() {
    setParams((p) => writeFilters(p, { project: projectKey ? undefined : [], status: [], priority: [], type: [], label: [], q: '', done: false }));
  }

  function applySheet(next: { status: string[]; priority: string[]; type: string[]; label: string[]; project: string[]; done: boolean }) {
    setParams((p) => writeFilters(p, { ...next, status: view === 'board' ? undefined : next.status, done: view === 'board' ? undefined : next.done, project: projectKey ? undefined : next.project }));
  }
  async function toggleFavorite() {
    if (!project) return;
    try {
      await setProjectFavorite(project, !project.favorite);
    } catch (e) {
      toast(`Couldn't update favorite: ${errorText(e)}`, 'error');
    }
  }
  const emptyText = $derived(project ? 'This project has no tasks yet. Tap + to add one.' : 'No tasks yet. Tap + to create your first task.');
</script>

{#if compact}
<section class="view compact" class:overlay class:collapsed bind:this={viewEl} style="--tail:{barVisible ? 96 : 24}px; --fold-h:{foldH}px">
  <div class="fold" bind:this={foldEl}>
    <div class="fold-in" inert={collapsed}>
      <MobileHeader {filters} {project} {canCreate} total={list.total} {view} mode={mode} {activeFilters} {selectMode} selectedCount={selection.size} {allSelected}
        onfilters={() => (sheetOpen = true)} {onsort} onselectmode={setSelectMode} onselectall={selectAll} bind:top={topEl} sortable={view === 'list'} />
    </div>
  </div>
  <div class="lists" bind:this={bodyEl}>
    {#if view === 'board' && projectKey}
      <Board {list} {projectKey} onopen={open} {activeRef} />
    {:else}
      <CompactList {list} {mode} showProject={!projectKey} onopen={open} {activeRef} {selectMode} selected={selection} onselect={(id) => select(id, false)} onlongpress={startSelecting} {emptyText} {filtered} onclear={clear} />
    {/if}
  </div>
  {#if barVisible}<BulkBar compact tasks={selectedTasks} onclear={() => selection.clear()} />{/if}
  {#if sheetOpen}<FilterSheet {filters} lockedProject={!!projectKey} hideStatus={view === 'board'} onapply={applySheet} onclose={() => (sheetOpen = false)} />{/if}
</section>
{:else}
<section class="view" class:has-bar={view === 'list' && selection.size > 0}>
  <header class:has-project={!!project}>
    <h1>{#if project}<span class="dot lg" style="--dot:{project.color}"></span>{project.name}<span class="muted key">{project.key}</span>{:else}All Tasks{/if}</h1>
    {#if project}
      <div class="tabs" role="tablist" aria-label="View">
        <button role="tab" aria-selected={view === 'list'} class:on={view === 'list'} onclick={() => setParams((p) => p.delete('view'))}>List</button>
        <button role="tab" aria-selected={view === 'board'} class:on={view === 'board'} onclick={() => setParams((p) => p.set('view', 'board'))}>Board</button>
      </div>
      <button class="gear star" class:on={project.favorite} title={project.favorite ? 'Remove from favourites' : 'Add to favourites'} aria-label={project.favorite ? 'Remove from favourites' : 'Add to favourites'} aria-pressed={project.favorite} onclick={toggleFavorite}>
        <svg viewBox="0 0 24 24" width="18" height="18" fill={project.favorite ? 'currentColor' : 'none'} stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" aria-hidden="true"><path d="M12 2.5l2.9 5.88 6.49.94-4.7 4.58 1.11 6.47L12 17.27l-5.8 3.1 1.11-6.47-4.7-4.58 6.49-.94z" /></svg>
      </button>
      <button class="gear" title="Project settings and members" aria-label="Project settings" onclick={() => (ui.projectSettings = project.key)}>
        <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></svg>
      </button>
    {/if}
    <span class="spacer"></span>
    {#if !project || (project.role !== 'viewer' && !project.archived)}
      <button class="btn primary" onclick={() => openCreate()}>+ New task <kbd>C</kbd></button>
    {/if}
  </header>
  <FilterBar {filters} lockedProject={!!projectKey} hideStatus={view === 'board'} showColumns={view === 'list'} filtered={filtered && (view === 'list' || filters.priority.length + filters.label.length > 0 || filters.q !== '')} total={list.total} />
  {#if view === 'board' && projectKey}
    <Board {list} {projectKey} onopen={open} {activeRef} />
  {:else if mode === 'cards'}
    <TaskCards {list} showProject={!projectKey} sort={filters.sort} {onsort} onopen={open} {activeRef} selected={selection} onselect={select} onselectall={selectAll} {filtered} onclear={clear}
      emptyText={project ? 'This project has no tasks yet. Press C to add one.' : 'No tasks yet. Press C to create your first task.'} />
  {:else}
    <TaskTable {list} showProject={!projectKey} sort={filters.sort} {onsort} onopen={open} {activeRef} selected={selection} onselect={select} onselectall={selectAll} {filtered} onclear={clear}
      emptyText={project ? 'This project has no tasks yet. Press C to add one.' : 'No tasks yet. Press C to create your first task.'} />
  {/if}
  {#if view === 'list' && selection.size > 0}<BulkBar tasks={selectedTasks} onclear={() => selection.clear()} />{/if}
</section>
{/if}

<style>
  .view { position: relative; display: flex; flex-direction: column; height: 100%; min-width: 0; }
  .view.has-bar :global(.table .inner) { margin-bottom: 76px; } /* keep the last rows reachable above the action bar */
  header { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; padding: 12px 16px 0; }
  /* Match the sidebar's "+ New task" button: the C hint has an outline only, no filled background. */
  header .btn.primary kbd { background: transparent; border-color: color-mix(in srgb, var(--accent-fg) 40%, transparent); color: var(--accent-fg); }
  h1 { display: flex; align-items: center; gap: 10px; min-width: 0; margin: 0; font-size: 20px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
  .lg { width: 12px; height: 12px; }
  /* a clear settings button: a bordered square with a proper gear, exactly as tall as the List / Board switch beside it (32px) */
  .gear { display: inline-grid; place-items: center; width: 32px; height: 32px; box-sizing: border-box; border: 1px solid var(--line-strong); border-radius: 8px; background: var(--bg); color: var(--fg); }
  .gear:hover { background: var(--bg-hover); border-color: var(--accent); color: var(--accent); }
  .gear.star.on { color: #ca8a04; border-color: var(--line-strong); }
  .gear.star.on:hover { color: #a16207; border-color: var(--line-strong); }
  .key { font: 12px ui-monospace, SFMono-Regular, Menlo, monospace; font-weight: 400; }
  .tabs { display: inline-flex; margin-left: 8px; padding: 2px; border-radius: 8px; background: var(--bg-soft); border: 1px solid var(--line); }
  .tabs button { height: 26px; padding: 0 12px; border: 0; border-radius: 6px; background: transparent; color: var(--muted); font-weight: 600; }
  .tabs button.on { background: var(--bg); color: var(--fg); box-shadow: 0 1px 2px rgba(16, 24, 40, 0.12); }

  /* --- phone and tablet (docs/design/mobile-redesign) --- */
  .view.compact { background: var(--m-page); }
  .fold { flex: none; }
  .fold-in { min-height: 0; }
  .lists { display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; }
  /* Board: the whole header folds away in one step while scrolling down. */
  .view.compact:not(.overlay) .fold { display: grid; grid-template-rows: 1fr; }
  .view.compact:not(.overlay) .fold-in { overflow: hidden; }
  .view.collapsed .fold { grid-template-rows: 0fr; }
  /* List and cards: the header is laid over the list, which reserves its height as top padding (--fold-h). Its
     top part slides away with the scroll (a transform only); the Filters / Sort row stays. */
  .view.overlay { overflow: hidden; }
  .view.overlay .fold { position: absolute; top: 0; left: 0; right: 0; z-index: 5; will-change: transform; }
  .view.overlay .fold:global(.lifted) { box-shadow: 0 2px 8px rgba(16, 24, 40, 0.12); }
</style>
