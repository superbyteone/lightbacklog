<script lang="ts">
  import { untrack } from 'svelte';
  import { writeFilters, type Filters } from '../lib/filters';
  import { navigate, setParams } from '../lib/router.svelte';
  import { setMode, type ViewMode } from '../lib/layout.svelte';
  import { app, errorText, setProjectFavorite, toast } from '../lib/store.svelte';
  import type { Project } from '../lib/types';
  import { openCreate, ui } from '../lib/ui.svelte';
  import Popover from './Popover.svelte';

  /**
   * The phone/tablet page header (docs/design/mobile-redesign). Three parts: the menu bar (`top`), which scrolls
   * away with the list on phones (LB-59: on tablets, where it's hidden anyway, nothing scrolls away); the title row
   * (project name, count, Create button); and the pinned row with Filters / Sort on the left and search / the gear
   * (list, cards and kanban view, settings) on the right. The title row and the pinned row stay visible while
   * scrolling. In select mode all three are replaced by the selection bar.
   */
  let {
    filters, project, canCreate, total, view, mode, activeFilters, selectMode, selectedCount, allSelected,
    onfilters, onsort, onselectmode, onselectall, top = $bindable(), sortable = true,
  }: {
    filters: Filters;
    project?: Project;
    canCreate: boolean;
    total: number | null;
    view: 'list' | 'board';
    mode: ViewMode;
    activeFilters: number;
    selectMode: boolean;
    selectedCount: number;
    allSelected: boolean;
    onfilters: () => void;
    onsort: (field: string) => void;
    onselectmode: (on: boolean) => void;
    onselectall: () => void;
    top?: HTMLElement;
    sortable?: boolean;
  } = $props();

  // --- search: the magnifier expands into a focused input ---
  let searching = $state(untrack(() => filters.q !== ''));
  let text = $state(untrack(() => filters.q));
  let input = $state<HTMLInputElement>();
  let timer: ReturnType<typeof setTimeout>;
  $effect(() => {
    if (filters.q !== text && document.activeElement !== input) {
      text = filters.q;
      if (filters.q) searching = true;
    }
  });
  function setQ(v: string) {
    text = v;
    clearTimeout(timer);
    timer = setTimeout(() => setParams((p) => writeFilters(p, { q: v.trim() })), 220);
  }
  function openSearch() {
    searching = true;
    queueMicrotask(() => input?.focus());
  }
  function closeSearch() {
    clearTimeout(timer);
    text = '';
    searching = false;
    if (filters.q) setParams((p) => writeFilters(p, { q: '' }));
  }

  // --- sort ---
  const SORTS = [
    ['updated_at', 'Updated', 'Updated'], ['created_at', 'Created', 'Newest'], ['completed_at', 'Completed', 'Completed'], ['title', 'Title', 'Title'], ['status', 'Status', 'Status'],
    ['priority', 'Priority', 'Priority'], ['sequence', 'Sequence', 'Sequence'], ['type', 'Type', 'Type'], ['due_date', 'Due', 'Due date'], ['project', 'Project', 'Project'],
  ] as const;
  const field = $derived(filters.sort.replace(/^-/, ''));
  const desc = $derived(filters.sort.startsWith('-'));
  const sortName = $derived(SORTS.find((s) => s[0] === field)?.[1] ?? 'Updated');
  let sortOpen = $state(false);
  let sortBtn = $state<HTMLButtonElement>();

  // --- the title: tap "All tasks" or the project name to jump to another project (LB-60) ---
  // Favorites are listed first, then the rest alphabetically (LB-33), matching the sidebar's grouping.
  const switchActive = $derived(app.projects.filter((p) => !p.archived));
  const switchFavorites = $derived(switchActive.filter((p) => p.favorite));
  const switchRest = $derived(switchActive.filter((p) => !p.favorite));
  let switchOpen = $state(false);
  let switchBtn = $state<HTMLButtonElement>();
  function pickProject(key: string | null) {
    switchOpen = false;
    navigate(key ? '/p/' + key : '/');
  }

  // --- the gear: how the tasks are shown, and the settings ---
  const VIEWS = [['list', 'List view'], ['cards', 'Card view'], ['board', 'Kanban view']] as const;
  type ViewId = (typeof VIEWS)[number][0];
  const viewId = $derived<ViewId>(view === 'board' ? 'board' : mode);
  let gearOpen = $state(false);
  let gearBtn = $state<HTMLButtonElement>();
  function pickView(id: ViewId) {
    gearOpen = false;
    if (id === 'board') return setParams((p) => p.set('view', 'board'));
    setParams((p) => p.delete('view'));
    setMode(id);
  }

  const scroller = (): HTMLElement | null => document.querySelector('.view .scroll');

  async function toggleFavorite() {
    if (!project) return;
    try {
      await setProjectFavorite(project, !project.favorite);
    } catch (e) {
      toast(`Couldn't update favorite: ${errorText(e)}`, 'error');
    }
  }
</script>

<div class="mhead" class:selecting={selectMode}>
  {#if selectMode}
    <div class="selbar">
      <button class="icon" aria-label="Exit select mode" onclick={() => onselectmode(false)}>
        <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg></button>
      <strong class="selcount" aria-live="polite">{selectedCount} selected</strong>
      <span class="spacer"></span>
      <button class="link" onclick={onselectall}>{allSelected ? 'Select none' : 'Select all'}</button>
    </div>
  {:else}
    <div class="bar" bind:this={top}>
      <button class="icon hamburger" aria-label="Open menu" onclick={() => (ui.sidebar = true)}>
        <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h16" /></svg></button>
      <span class="brand">LightBacklog</span>
    </div>
    <div class="titlerow">
      <h1>
        <button class="titlebtn" bind:this={switchBtn} aria-haspopup="listbox" aria-expanded={switchOpen} aria-label="Switch project" onclick={() => (switchOpen = !switchOpen)}>
          {#if project}<span class="pdot" style="--dot:{project.color}"></span><span class="pname">{project.name}</span>{:else}All tasks{/if}
          <svg class="chev" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
        </button>
      </h1>
      {#if total !== null}<span class="count" aria-label="{total} tasks">{total.toLocaleString()}</span>{/if}
      <span class="spacer"></span>
      {#if canCreate}
        <button class="create" onclick={() => openCreate()}>
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>Create</button>
      {/if}
      {#snippet switchRow(p: Project)}
        <button class="mi" role="option" aria-selected={project?.id === p.id} onclick={() => pickProject(p.key)}>
          <span class="row"><span class="pdot" style="--dot:{p.color}"></span><span class="pn">{p.name}</span>
            {#if p.favorite}<svg class="star" viewBox="0 0 24 24" width="14" height="14" fill="currentColor" aria-hidden="true"><path d="M12 2.5l2.9 5.88 6.49.94-4.7 4.58 1.11 6.47L12 17.27l-5.8 3.1 1.11-6.47-4.7-4.58 6.49-.94z" /></svg>{/if}</span>
          {#if project?.id === p.id}<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 12.5 4.5 4.5L19 7.5" /></svg>{/if}
        </button>
      {/snippet}
      {#if switchOpen && switchBtn}
        <Popover anchor={switchBtn} width={260} maxHeight={360} onclose={() => (switchOpen = false)}>
          <div class="menu" role="listbox" aria-label="Switch project">
            <div class="opts">
              <button class="mi" role="option" aria-selected={!project} onclick={() => pickProject(null)}>
                <span>All tasks</span>
                {#if !project}<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 12.5 4.5 4.5L19 7.5" /></svg>{/if}
              </button>
              {#if switchFavorites.length > 0}
                <div class="divider"><span>Favorites</span><span class="rule"></span></div>
                {#each switchFavorites as p (p.id)}{@render switchRow(p)}{/each}
                <div class="divider"><span>A&ndash;Z</span><span class="rule"></span></div>
              {/if}
              {#each switchRest as p (p.id)}{@render switchRow(p)}{/each}
            </div>
          </div>
        </Popover>
      {/if}
    </div>
    <div class="controls">
      {#if searching}
        <div class="searchbox" role="search">
          <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="6.5" /><path d="m20 20-4.2-4.2" /></svg>
          <input id="filter-q" type="search" placeholder="Search tasks" aria-label="Search tasks" value={text} oninput={(e) => setQ(e.currentTarget.value)}
            enterkeyhint="search" autocomplete="off" onkeydown={(e) => e.key === 'Escape' && closeSearch()} bind:this={input} />
        </div>
        <button class="link" onclick={closeSearch}>Cancel</button>
      {:else}
        <button class="pill" class:on={activeFilters > 0} aria-haspopup="dialog" aria-label={activeFilters ? `Filters, ${activeFilters} active` : 'Filters'} onclick={onfilters}>
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M4 7h9M17 7h3M4 17h3M11 17h9" /><circle cx="15" cy="7" r="2" /><circle cx="9" cy="17" r="2" /></svg>
          Filters{#if activeFilters > 0}<span class="badge" aria-hidden="true">{activeFilters}</span>{/if}
        </button>
      {#if sortable}
        <button class="pill" bind:this={sortBtn} aria-haspopup="listbox" aria-expanded={sortOpen} aria-label="Sort: {sortName}, {desc ? 'descending' : 'ascending'}" onclick={() => (sortOpen = !sortOpen)}>
          {sortName}
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">{#if desc}<path d="M12 5v14M6 13l6 6 6-6" />{:else}<path d="M12 19V5M6 11l6-6 6 6" />{/if}</svg>
        </button>
        {#if sortOpen && sortBtn}
          <Popover anchor={sortBtn} width={230} maxHeight={520} onclose={() => (sortOpen = false)}>
            <div class="menu" role="listbox" aria-label="Sort by">
              <div class="opts">
              {#each SORTS as [f, , long] (f)}
                <button class="mi" role="option" aria-selected={field === f} onclick={() => { onsort(f); if (field !== f) sortOpen = false; }}>
                  <span>{long}</span>
                  {#if field === f}<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">{#if desc}<path d="M12 5v14M6 13l6 6 6-6" />{:else}<path d="M12 19V5M6 11l6-6 6 6" />{/if}</svg>{/if}
                </button>
              {/each}
              </div>
              <p class="hint muted">Tap the current one again to reverse the order.</p>
            </div>
          </Popover>
        {/if}
      {/if}
        <span class="spacer"></span>
        <button class="icon" aria-label="Search tasks" onclick={openSearch}>
          <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="6.5" /><path d="m20 20-4.2-4.2" /></svg></button>
        <button class="icon" bind:this={gearBtn} aria-label="View and settings" aria-haspopup="menu" aria-expanded={gearOpen} onclick={() => (gearOpen = !gearOpen)}>
          <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></svg></button>
        {#if gearOpen && gearBtn}
          <Popover anchor={gearBtn} width={230} onclose={() => (gearOpen = false)}>
            <div class="menu" role="menu" aria-label="View and settings">
              {#each VIEWS as [id, label] (id)}
                {#if id !== 'board' || project}
                  <button class="mi" role="menuitemradio" aria-checked={viewId === id} onclick={() => pickView(id)}>
                    <span>{label}</span>
                    {#if viewId === id}<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 12.5 4.5 4.5L19 7.5" /></svg>{/if}
                  </button>
                {/if}
              {/each}
              {#if project}
                <div class="sep" role="separator"></div>
                <button class="mi" role="menuitemcheckbox" aria-checked={project.favorite} onclick={toggleFavorite}>
                  <span>Favourite</span>
                  {#if project.favorite}<svg class="star" viewBox="0 0 24 24" width="16" height="16" fill="currentColor" aria-hidden="true"><path d="M12 2.5l2.9 5.88 6.49.94-4.7 4.58 1.11 6.47L12 17.27l-5.8 3.1 1.11-6.47-4.7-4.58 6.49-.94z" /></svg>{/if}
                </button>
              {/if}
              <div class="sep" role="separator"></div>
              {#if project}<button class="mi" role="menuitem" onclick={() => { gearOpen = false; ui.projectSettings = project.key; }}>Project settings</button>{/if}
              <button class="mi" role="menuitem" onclick={() => { gearOpen = false; navigate('/settings'); }}>All Settings</button>
            </div>
          </Popover>
        {/if}
      {/if}
    </div>
  {/if}
</div>

<style>
  /* Sizes follow the sidebar: 14px text, 32px controls, 8px gaps. Small controls get a 44px touch area from ::after. */
  .mhead { background: var(--m-page); }
  .bar { display: flex; align-items: center; gap: 2px; height: 48px; padding: 0 8px 0 4px; background: var(--m-card); border-bottom: 1px solid var(--m-line); }
  .brand { margin-left: 4px; font-size: 16px; font-weight: 750; letter-spacing: -0.01em; }
  .icon { position: relative; display: inline-grid; place-items: center; flex: none; width: 40px; height: 40px; border: 0; border-radius: 6px; background: transparent; color: var(--fg); -webkit-tap-highlight-color: transparent; }
  .icon:active { background: var(--m-chip); }
  .create { position: relative; display: inline-flex; align-items: center; gap: 6px; flex: none; height: 32px; padding: 0 14px 0 10px; border: 0; border-radius: 16px; background: var(--m-accent); color: var(--accent-fg); font-size: 14px; font-weight: 600; -webkit-tap-highlight-color: transparent; }
  .create:active { filter: brightness(1.1); }
  .create::after, .pill::after, .link::after, .controls .icon::after { content: ''; position: absolute; inset: -6px; }
  /* The menu bar (menu button and app name) only exists where the sidebar is a drawer. */
  @media (min-width: 801px) { .bar { display: none; } }
  .searchbox { flex: 1; min-width: 0; display: flex; align-items: center; gap: 8px; height: 34px; padding: 0 10px; border: 1px solid var(--m-accent); border-radius: 6px; background: var(--m-card); color: var(--muted); }
  /* 16px on purpose: iOS zooms the page into focused inputs with smaller text */
  .searchbox input { flex: 1; min-width: 0; height: 100%; border: 0; background: transparent; color: var(--fg); font-size: 16px; outline: none; }
  .searchbox input::-webkit-search-cancel-button { display: none; }
  .link { position: relative; flex: none; height: 32px; padding: 0 8px; border: 0; background: transparent; color: var(--m-accent); font-size: 14px; font-weight: 600; }
  /* Extra bottom padding keeps Create's expanded touch area (::after, -6px) clear of the icon buttons' own
     expanded area in .controls right below it, so a tap near that boundary can't land on the wrong button. */
  .titlerow { display: flex; align-items: center; gap: 8px; min-height: 44px; padding: 6px 12px 10px; }
  h1 { min-width: 0; margin: 0; font-size: 20px; font-weight: 700; letter-spacing: -0.01em; line-height: 1.15; }
  /* The title doubles as a button (LB-60): tap it to jump to another project. The chevron matches the count pill's color. */
  .titlebtn { display: flex; align-items: center; gap: 6px; min-width: 0; max-width: 100%; border: 0; padding: 0; background: transparent; color: inherit; font: inherit; letter-spacing: inherit; text-align: left; -webkit-tap-highlight-color: transparent; }
  .titlebtn:active { opacity: 0.7; }
  .chev { flex: none; color: var(--muted); }
  .pname { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pdot { flex: none; width: 10px; height: 10px; border-radius: 50%; background: var(--dot); }
  .row { display: flex; align-items: center; flex: 1; gap: 8px; min-width: 0; overflow: hidden; }
  .pn { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .count { flex: none; padding: 0 8px; height: 22px; display: inline-flex; align-items: center; border-radius: 11px; background: var(--m-count); color: var(--muted); font-size: 12px; font-weight: 500; font-variant-numeric: tabular-nums; }
  .controls { display: flex; align-items: center; gap: 8px; height: 46px; padding: 0 12px; background: var(--m-page); border-bottom: 1px solid var(--m-line); }
  .pill { position: relative; overflow: visible; display: inline-flex; align-items: center; gap: 6px; height: 32px; padding: 0 12px; border: 1px solid var(--m-line-strong); border-radius: 16px; background: var(--m-card); color: var(--fg); font-size: 14px; font-weight: 500; white-space: nowrap; -webkit-tap-highlight-color: transparent; }
  .pill.on { border-color: var(--m-accent); background: var(--m-accent-soft); color: var(--m-accent); }
  .badge { display: inline-grid; place-items: center; min-width: 18px; height: 18px; padding: 0 5px; border-radius: 9px; background: var(--m-accent); color: var(--accent-fg); font-size: 11px; font-weight: 600; }
  .selbar { display: flex; align-items: center; gap: 4px; height: 48px; padding: 0 12px 0 4px; background: var(--m-card); border-bottom: 1px solid var(--m-line); }
  .selcount { margin-left: 4px; font-size: 16px; }
  /* The options scroll when the screen is short; the hint below them always stays fully visible. */
  .menu { display: flex; flex-direction: column; min-height: 0; padding: 4px 0; }
  .opts { display: grid; min-height: 0; overflow-y: auto; overscroll-behavior: contain; }
  .mi { display: flex; align-items: center; justify-content: space-between; gap: 8px; min-height: 36px; padding: 0 12px; border: 0; background: transparent; text-align: left; font-size: 14px; }
  .mi[aria-selected='true'] { color: var(--m-accent); font-weight: 600; }
  .mi:active { background: var(--m-chip); }
  .sep { height: 1px; margin: 4px 0; background: var(--m-line); }
  .divider { display: flex; align-items: center; gap: 8px; padding: 10px 12px 4px; font-size: 11px; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase; color: var(--muted); }
  .divider .rule { flex: 1; height: 1px; background: var(--m-line); }
  .star { flex: none; color: #ca8a04; }
  .hint { flex: none; margin: 0; padding: 4px 12px 8px; font-size: 12px; }
</style>
