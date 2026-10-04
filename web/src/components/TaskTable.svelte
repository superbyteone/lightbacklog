<script lang="ts">
  import { FLEX_MIN, MAX_WIDTH, resetWidth, setWidth, visibleColumns, layout, type ResolvedColumn } from '../lib/layout.svelte';
  import { cache, type TaskList } from '../lib/store.svelte';
  import TaskRow from './TaskRow.svelte';

  let {
    list, showProject, sort, onsort, onopen, activeRef, selected, onselect, onselectall, emptyText = 'No tasks here yet.', filtered = false, onclear,
  }: {
    list: TaskList;
    showProject: boolean;
    sort: string;
    onsort: (field: string) => void;
    onopen: (ref: string) => void;
    activeRef?: string;
    selected: Set<string>;
    onselect: (id: string, shift: boolean) => void;
    onselectall: () => void;
    emptyText?: string;
    filtered?: boolean;
    onclear?: () => void;
  } = $props();

  const ROW = 38;
  const OVERSCAN = 8;
  let scroller = $state<HTMLDivElement>();
  let scrollTop = $state(0);
  let viewH = $state(600);
  let cursor = $state(-1);

  // Which columns show, in which order and how wide comes from the user's saved layout for this device class
  // (on desktop, the table keeps its own layout separate from the Cards view's fields).
  const columns = $derived((layout.viewport, visibleColumns(showProject, 'list')));
  const SEL = 36; // width of the selection checkbox column
  const cols = $derived(SEL + 'px ' + columns.map((c) => (c.current ? `${c.current}px` : `minmax(${FLEX_MIN}px,1fr)`)).join(' '));
  const minWidth = $derived(SEL + columns.reduce((sum, c) => sum + (c.current ?? FLEX_MIN), 0));
  const allSelected = $derived(list.ids.length > 0 && list.ids.every((id) => selected.has(id)));
  const someSelected = $derived(!allSelected && list.ids.some((id) => selected.has(id)));
  const start = $derived(Math.max(0, Math.floor(scrollTop / ROW) - OVERSCAN));
  const end = $derived(Math.min(list.ids.length, Math.ceil((scrollTop + viewH) / ROW) + OVERSCAN));
  const visible = $derived(list.ids.slice(start, end));
  const sortField = $derived(sort.replace(/^-/, ''));
  const desc = $derived(sort.startsWith('-'));

  $effect(() => {
    if (end >= list.ids.length - 30 && list.hasMore && !list.loading) void list.more();
  });

  function onKey(e: KeyboardEvent) {
    const t = e.target as HTMLElement;
    if (t.closest('input, textarea, [role="combobox"]')) return;
    if (e.key === 'ArrowDown' || e.key === 'j') cursor = Math.min(list.ids.length - 1, cursor + 1);
    else if (e.key === 'ArrowUp' || e.key === 'k') cursor = Math.max(0, cursor - 1);
    else if (e.key === 'Enter' && cursor >= 0 && t === scroller) {
      const task = cache[list.ids[cursor]];
      if (task) onopen(task.ref);
      return;
    } else return;
    e.preventDefault();
    const top = cursor * ROW;
    if (scroller) {
      if (top < scroller.scrollTop) scroller.scrollTop = top;
      else if (top + ROW > scroller.scrollTop + viewH) scroller.scrollTop = top + ROW - viewH;
    }
  }

  // --- column resizing: drag a header edge (mouse or touch), or focus it and use the arrow keys ---
  let drag: { id: ResolvedColumn['id']; startX: number; startW: number } | null = null;
  const cellWidth = (el: HTMLElement) => el.closest<HTMLElement>('.hwrap')!.getBoundingClientRect().width;

  function startResize(e: PointerEvent, c: ResolvedColumn) {
    e.preventDefault();
    e.stopPropagation();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    drag = { id: c.id, startX: e.clientX, startW: cellWidth(e.currentTarget as HTMLElement) };
  }
  function moveResize(e: PointerEvent) {
    if (drag) setWidth(drag.id, drag.startW + (e.clientX - drag.startX));
  }
  function endResize() {
    drag = null;
  }
  function keyResize(e: KeyboardEvent, c: ResolvedColumn) {
    const step = e.shiftKey ? 48 : 16;
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      setWidth(c.id, cellWidth(e.currentTarget as HTMLElement) + (e.key === 'ArrowRight' ? step : -step));
    } else if (e.key === 'Enter' || e.key === 'Backspace' || e.key === 'Delete') {
      e.preventDefault();
      resetWidth(c.id);
    }
  }
</script>

<div class="table" style="--cols:{cols}; --minw:{minWidth}px">
  <div class="head" role="row">
    <div class="hwrap"><label class="selbox hsel" title="Select all {list.ids.length} loaded tasks"><input type="checkbox" checked={allSelected} indeterminate={someSelected} disabled={list.ids.length === 0} aria-label="Select all loaded tasks" onclick={() => onselectall()} /></label></div>
    {#each columns as c (c.id)}
      <div class="hwrap">
        {#if c.sort}
          <div class="hcell" role="columnheader" aria-sort={sortField === c.sort ? (desc ? 'descending' : 'ascending') : 'none'}>
            <button class="hbtn" title={c.note} onclick={() => onsort(c.sort)}>{c.label}{#if sortField === c.sort}<span class="arrow">{desc ? '↓' : '↑'}</span>{/if}</button>
          </div>
        {:else}
          <div class="hcell" role="columnheader"><span class="hbtn static">{c.label}</span></div>
        {/if}
        <span class="rh" role="slider" aria-orientation="horizontal" tabindex="0" aria-label="{c.label} column width (arrow keys change it; Enter resets)" aria-valuenow={c.current ?? FLEX_MIN} aria-valuemin={c.min} aria-valuemax={MAX_WIDTH}
          title="Drag to resize · double-click to reset" onpointerdown={(e) => startResize(e, c)} onpointermove={moveResize} onpointerup={endResize} onpointercancel={endResize}
          onkeydown={(e) => keyResize(e, c)} ondblclick={() => resetWidth(c.id)}></span>
      </div>
    {/each}
  </div>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="body" role="grid" tabindex="0" aria-label="Tasks" aria-rowcount={list.total ?? list.ids.length}
    bind:this={scroller} bind:clientHeight={viewH} onscroll={(e) => (scrollTop = e.currentTarget.scrollTop)} onkeydown={onKey}>
    <div class="inner" style="height:{list.ids.length * ROW}px">
      {#each visible as id, i (id)}
        {@const task = cache[id]}
        {#if task}
          <TaskRow {task} {columns} selected={selected.has(task.id)} {onselect} active={task.ref === activeRef || start + i === cursor} top={(start + i) * ROW} {onopen} />
        {/if}
      {/each}
    </div>
    {#if list.loading && list.ids.length === 0}
      <p class="state muted">Loading tasks…</p>
    {:else if list.error}
      <p class="state"><span class="error-text">{list.error}</span> <button class="btn" onclick={() => list.refresh()}>Retry</button></p>
    {:else if list.ids.length === 0}
      <p class="state muted">{filtered ? 'No tasks match these filters.' : emptyText}
        {#if filtered && onclear}<button class="btn" onclick={onclear}>Clear filters</button>{/if}</p>
    {/if}
    {#if list.loading && list.ids.length > 0}<p class="more muted">Loading more…</p>{/if}
  </div>
</div>

<style>
  .table { display: flex; flex-direction: column; min-height: 0; min-width: 0; flex: 1; overflow-x: auto; overflow-y: hidden; }
  .head { min-width: var(--minw); display: grid; grid-template-columns: var(--cols); flex: none; height: 32px; border-bottom: 1px solid var(--line-strong); background: var(--bg-soft); font-size: 12px; font-weight: 600; color: var(--muted); }
  .hsel { display: grid; place-items: center; width: 100%; height: 100%; cursor: pointer; }
  .hwrap { position: relative; display: flex; min-width: 0; }
  .hcell { display: flex; flex: 1; min-width: 0; align-items: center; }
  .hbtn { display: flex; align-items: center; gap: 4px; flex: 1; min-width: 0; height: 100%; padding: 0 8px; border: 0; background: transparent; text-align: left; color: inherit; font: inherit; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
  .hbtn.static { cursor: default; }
  .hwrap:nth-child(1) .hbtn, .hwrap .hbtn { min-width: 0; }
  button.hbtn:hover { color: var(--fg); background: var(--bg-hover); }
  /* Resize handle: a wide invisible grab area with a thin visible line on hover and focus. */
  .rh { position: absolute; top: 0; bottom: 0; right: -7px; z-index: 2; width: 14px; cursor: col-resize; touch-action: none; }
  .rh::after { content: ''; position: absolute; top: 6px; bottom: 6px; left: 6px; width: 2px; border-radius: 1px; background: var(--line-strong); opacity: 0.55; }
  .rh:hover::after, .rh:focus-visible::after { background: var(--accent); opacity: 1; }
  .arrow { color: var(--accent); }
  .body { position: relative; flex: 1; min-width: var(--minw); overflow-x: hidden; overflow-y: auto; outline: none; }
  .inner { position: relative; }
  .state { position: absolute; inset: 40px 0 auto; margin: 0; text-align: center; display: flex; gap: 10px; justify-content: center; align-items: center; }
  .more { margin: 0; padding: 8px; text-align: center; font-size: 12px; }
</style>
