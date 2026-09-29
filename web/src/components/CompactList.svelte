<script lang="ts">
  import { flushSync, untrack } from 'svelte';
  import { formatDue, todayISO } from '../lib/filters';
  import { layout, visibleColumns, type ColumnId } from '../lib/layout.svelte';
  import { app, cache, priorityBySlug, statusBySlug, type TaskList } from '../lib/store.svelte';

  /**
   * The phone and tablet task list (docs/design/mobile-redesign): stacked cards or compact rows, read-only
   * on the surface (tap opens the task; a long press starts select mode), with checkboxes only in select mode. Heights vary with the title,
   * so the window of rendered items is worked out from measured heights (cached per task).
   */
  let {
    list, mode, showProject, onopen, activeRef, selectMode, selected, onselect, onlongpress, emptyText = 'No tasks here yet.', filtered = false, onclear,
  }: {
    list: TaskList;
    mode: 'cards' | 'list';
    showProject: boolean;
    onopen: (ref: string) => void;
    activeRef?: string;
    selectMode: boolean;
    selected: Set<string>;
    onselect: (id: string) => void;
    /** Called with the task id when a task is pressed and held while not in select mode. */
    onlongpress?: (id: string) => void;
    emptyText?: string;
    filtered?: boolean;
    onclear?: () => void;
  } = $props();

  const cards = $derived(mode === 'cards');
  const GAP = $derived(cards ? 8 : 0);
  const DEFAULT_H = $derived(cards ? 122 : 50);
  const OVERSCAN_PX = 700;

  let scroller = $state<HTMLDivElement>();
  let inner = $state<HTMLDivElement>();
  let scrollTop = $state(0);
  let viewH = $state(700);

  // --- long press on a task starts select mode ---
  const LONG_PRESS_MS = 500;
  const MOVE_TOLERANCE = 10; // px of finger drift that still counts as holding still
  let press: { x: number; y: number; timer: number } | null = null;
  let swallowClick = false; // the release after a long press must not also open the task

  function cancelPress() {
    if (press) clearTimeout(press.timer);
    press = null;
  }
  function pressStart(e: PointerEvent, id: string) {
    swallowClick = false; // a fresh press: whatever click follows is a real one
    cancelPress();
    if (selectMode || !onlongpress || (e.pointerType === 'mouse' && e.button !== 0)) return;
    press = {
      x: e.clientX,
      y: e.clientY,
      timer: window.setTimeout(() => {
        press = null;
        swallowClick = true;
        navigator.vibrate?.(12);
        onlongpress(id);
      }, LONG_PRESS_MS),
    };
  }
  function pressMove(e: PointerEvent) {
    if (press && Math.hypot(e.clientX - press.x, e.clientY - press.y) > MOVE_TOLERANCE) cancelPress();
  }
  function hitClick(e: MouseEvent, id: string, ref: string) {
    if (swallowClick && e.detail > 0) { swallowClick = false; return; } // keyboard activation (detail 0) is never swallowed
    swallowClick = false;
    if (selectMode) onselect(id);
    else onopen(ref);
  }
  $effect(() => () => cancelPress());

  // --- variable-height windowing ---
  const heights = new Map<string, number>();
  let sum = 0;
  let ver = $state(0);
  const avg = () => (heights.size ? sum / heights.size : DEFAULT_H);

  const offsets = $derived.by(() => {
    ver;
    const ids = list.ids;
    const a = avg();
    const o = new Array<number>(ids.length + 1);
    let y = 0;
    for (let i = 0; i < ids.length; i++) {
      o[i] = y;
      y += (heights.get(ids[i]) ?? a) + GAP;
    }
    o[ids.length] = y;
    return o;
  });
  const total = $derived(offsets[offsets.length - 1] ?? 0);

  function findIndex(y: number): number {
    // last index whose top is <= y
    let lo = 0, hi = offsets.length - 2;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (offsets[mid] <= y) lo = mid;
      else hi = mid - 1;
    }
    return Math.max(0, lo);
  }
  const start = $derived(findIndex(Math.max(0, scrollTop - OVERSCAN_PX)));
  const end = $derived(Math.min(list.ids.length, findIndex(scrollTop + viewH + OVERSCAN_PX) + 1));
  const visible = $derived(list.ids.slice(start, end));

  const ro = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver((entries) => {
    const ids = list.ids;
    const first = findIndex(scrollTop);
    let shift = 0, changed = false;
    for (const e of entries) {
      const id = (e.target as HTMLElement).dataset.id;
      if (!id) continue;
      const h = e.borderBoxSize?.[0]?.blockSize ?? e.contentRect.height;
      if (!h) continue;
      const old = heights.get(id);
      if (old !== undefined && Math.abs(old - h) < 0.5) continue;
      const idx = ids.indexOf(id);
      if (idx >= 0 && idx < first) shift += h - (old ?? avg()); // an item above the viewport got taller/shorter: keep what is on screen still
      sum += h - (old ?? 0);
      heights.set(id, h);
      changed = true;
    }
    if (!changed) return;
    ver++;
    flushSync();
    if (shift && scroller) scroller.scrollTop += shift;
  });
  function measure(node: HTMLElement, id: string) {
    node.dataset.id = id;
    ro?.observe(node);
    return { update(next: string) { node.dataset.id = next; }, destroy() { ro?.unobserve(node); } };
  }
  $effect(() => {
    // new width (rotation, resize) means new line breaks: forget what was measured
    layout.viewport;
    untrack(() => {
      heights.clear();
      sum = 0;
      ver++;
    });
  });

  $effect(() => {
    if (end >= list.ids.length - 15 && list.hasMore && !list.loading) void list.more();
  });

  // --- what each item shows ---
  const columns = $derived((layout.viewport, visibleColumns(showProject)));
  const has = (id: ColumnId) => columns.some((c) => c.id === id);
  const fields = $derived(columns.map((c) => c.id));
  const today = todayISO();
  const typeBySlug = (slug: string | null) => (slug ? app.types.find((t) => t.slug === slug) : undefined);
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="scroll" role="region" aria-label="Tasks" tabindex="0" bind:this={scroller} bind:clientHeight={viewH}
  onscroll={(e) => (scrollTop = Math.max(0, e.currentTarget.scrollTop - (inner?.offsetTop ?? 0)))}>
  <div class="inner" class:cards class:rows={!cards} role="list" bind:this={inner} style="height:{total}px">
    {#each visible as id, i (id)}
      {@const task = cache[id]}
      {#if task}
        {@const done = !!statusBySlug(task.status)?.is_done}
        {@const st = statusBySlug(task.status)}
        {@const pr = priorityBySlug(task.priority)}
        {@const ty = typeBySlug(task.type)}
        {@const isSel = selected.has(id)}
        <div class="item" role="listitem" data-ref={task.ref} class:sel={isSel} class:active={task.ref === activeRef} class:done use:measure={id} style="top:{offsets[start + i]}px">
          {#if selectMode}
            <label class="check"><input type="checkbox" checked={isSel} aria-label="Select {task.ref}" onchange={() => onselect(id)} /></label>
          {/if}
          <button class="hit" onclick={(e) => hitClick(e, id, task.ref)} onpointerdown={(e) => pressStart(e, id)} onpointermove={pressMove} onpointerup={cancelPress} onpointercancel={cancelPress} onpointerleave={cancelPress}
            oncontextmenu={(e) => e.preventDefault()} aria-label="{selectMode ? 'Select' : 'Open'} {task.ref}: {task.title}">
            {#if cards}
              <span class="top">
                {#if has('ref')}<span class="ref">{task.ref}</span>{/if}
                {#if showProject}<span class="pdot" style="--dot:{task.project.color}"></span>{/if}
                {#if has('project') && showProject}<span class="proj">{task.project.name}</span>{/if}
                {#if has('sequence') && task.sequence !== null}<span class="seq">#{task.sequence}</span>{/if}
              </span>
              <span class="title">{task.title}</span>
              <span class="meta">
                {#each fields as f (f)}
                  {#if f === 'status' && st}<span class="pill"><span class="dot" style="--dot:{st.color}"></span>{st.name}</span>
                  {:else if f === 'priority' && pr}<span class="pill"><span class="dot" style="--dot:{pr.color}"></span>{pr.name}</span>
                  {:else if f === 'type' && task.type}<span class="tpill" style="--tc:{ty?.color ?? '#6b7280'}" data-type={task.type}>{ty?.name ?? task.type}</span>
                  {:else if f === 'due' && task.due_date}<span class="txt" class:overdue={!done && task.due_date < today}>{formatDue(task.due_date)}</span>
                  {:else if (f === 'created' || f === 'updated' || f === 'completed') && (f === 'created' ? task.created_at : f === 'updated' ? task.updated_at : task.completed_at)}<span class="txt">{f === 'created' ? 'Created' : f === 'updated' ? 'Updated' : 'Completed'} {new Date((f === 'created' ? task.created_at : f === 'updated' ? task.updated_at : task.completed_at) ?? '').toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}</span>
                  {:else if f === 'labels' && task.labels.length}<span class="txt labels">{task.labels.slice(0, 2).map((l) => l.name).join(' · ')}{task.labels.length > 2 ? ` +${task.labels.length - 2}` : ''}</span>{/if}
                {/each}
              </span>
            {:else}
              <span class="title">{task.title}</span>
              <span class="meta">
                {#if has('ref')}<span class="ref">{#if showProject}<span class="pdot" style="--dot:{task.project.color}"></span>{/if}{task.ref}</span>{:else if showProject}<span class="pdot" style="--dot:{task.project.color}"></span>{/if}
                {#each fields as f (f)}
                  {#if f === 'status' && st}<span class="lp"><span class="dot" style="--dot:{st.color}"></span>{st.name}</span>
                  {:else if f === 'priority' && pr}<span class="lp"><span class="dot" style="--dot:{pr.color}"></span>{pr.name}</span>
                  {:else if f === 'type' && task.type}<span class="tpill" style="--tc:{ty?.color ?? '#6b7280'}" data-type={task.type}>{ty?.name ?? task.type}</span>
                  {:else if f === 'sequence' && task.sequence !== null}<span class="txt">#{task.sequence}</span>
                  {:else if f === 'due' && task.due_date}<span class="txt" class:overdue={!done && task.due_date < today}>{formatDue(task.due_date)}</span>
                  {:else if (f === 'created' || f === 'updated' || f === 'completed') && (f === 'created' ? task.created_at : f === 'updated' ? task.updated_at : task.completed_at)}<span class="txt">{f === 'created' ? 'Created' : f === 'updated' ? 'Updated' : 'Completed'} {new Date((f === 'created' ? task.created_at : f === 'updated' ? task.updated_at : task.completed_at) ?? '').toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}</span>
                  {:else if f === 'labels' && task.labels.length}<span class="txt labels">{task.labels.slice(0, 2).map((l) => l.name).join(' · ')}{task.labels.length > 2 ? ` +${task.labels.length - 2}` : ''}</span>{/if}
                {/each}
              </span>
            {/if}
          </button>
        </div>
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

<style>
  /* overscroll-behavior-x only (not the shorthand): the y-axis is left at its default `auto` so that
     over-scrolling past the top or bottom chains to the document, letting Chrome's native pull-to-refresh
     fire once the list itself is fully scrolled (LB-94). Horizontal chaining stays contained, though
     overflow-x: hidden already leaves nothing for it to chain. */
  .scroll { position: relative; flex: 1; min-height: 0; overflow-y: auto; overflow-x: hidden; padding: var(--fold-h, 0px) 0 var(--tail, 24px); background: var(--m-page); outline: none; overscroll-behavior-x: contain; }
  .inner { position: relative; }
  .inner.cards { margin: 12px 12px 0; }
  .item { position: absolute; left: 0; right: 0; display: flex; align-items: center; gap: 4px; }
  .item.sel .hit { background: var(--m-sel); border-color: var(--m-accent); }
  .check { flex: none; display: grid; place-items: center; width: 44px; height: 44px; cursor: pointer; }
  .check input { width: 22px; height: 22px; accent-color: var(--m-accent); margin: 0; }
  .hit { -webkit-touch-callout: none; user-select: none; -webkit-user-select: none; flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 6px; text-align: left; border: 1px solid transparent; background: transparent; padding: 0; color: inherit; -webkit-tap-highlight-color: transparent; }
  .cards .hit { padding: 12px 14px 12px; border-color: var(--m-line); border-radius: 14px; background: var(--m-card); }
  .cards .item.active .hit { border-color: var(--m-accent); }
  .rows .hit { padding: 6px 12px 7px; gap: 2px; border-bottom-color: var(--m-line); background: var(--m-card); justify-content: center; border-radius: 0; }
  .rows .item.active .hit { background: var(--m-accent-soft); }
  .rows .item.sel .hit { background: var(--m-sel); }
  .rows .check { background: var(--m-card); height: auto; align-self: stretch; border-bottom: 1px solid var(--m-line); }
  .rows .item { gap: 0; align-items: stretch; }
  .top, .meta { display: flex; align-items: center; gap: 8px; min-width: 0; }
  .top { font-size: 14px; color: var(--muted); white-space: nowrap; }
  .top .proj { overflow: hidden; text-overflow: ellipsis; }
  .seq { margin-left: auto; font-variant-numeric: tabular-nums; }
  .pdot { flex: none; width: 10px; height: 10px; border-radius: 50%; background: var(--dot); }
  .title { display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; font-size: 16px; font-weight: 500; line-height: 21px; color: var(--fg); }
  /* the compact list: one line of title and one small line of details */
  .rows .title { -webkit-line-clamp: 1; line-clamp: 1; font-size: 14px; line-height: 19px; }
  .rows .meta { gap: 8px; line-height: 15px; }
  .rows .ref, .rows .lp, .rows .txt { font-size: 12px; gap: 5px; }
  .rows .lp .dot { width: 7px; height: 7px; }
  .rows .pdot { width: 8px; height: 8px; }
  .rows .tpill { height: 16px; padding: 0 7px; border-radius: 9px; font-size: 11px; }
  .rows .check { width: 40px; }
  .rows .check input { width: 18px; height: 18px; }
  .done .title { color: var(--muted); text-decoration: line-through; text-decoration-color: color-mix(in srgb, var(--muted) 60%, transparent); }
  .meta { flex-wrap: nowrap; overflow: hidden; }
  .cards .meta { flex-wrap: wrap; overflow: visible; row-gap: 6px; }
  .pill { display: inline-flex; align-items: center; gap: 7px; height: 28px; padding: 0 12px; border-radius: 14px; background: var(--m-chip); font-size: 14px; white-space: nowrap; color: var(--fg); }
  .lp { display: inline-flex; align-items: center; gap: 6px; font-size: 14px; color: var(--muted); white-space: nowrap; }
  .lp .dot, .pill .dot { width: 9px; height: 9px; }
  .ref { display: inline-flex; align-items: center; gap: 8px; font-size: 14px; color: var(--muted); white-space: nowrap; }
  .rows .proj { font-size: 14px; color: var(--muted); white-space: nowrap; }
  .tpill { display: inline-flex; align-items: center; height: 26px; padding: 0 11px; border-radius: 13px; font-size: 14px; font-weight: 500; white-space: nowrap;
    color: color-mix(in srgb, var(--tc) 72%, var(--fg)); background: color-mix(in srgb, var(--tc) 15%, transparent); }
  .tpill[data-type='bug'] { background: var(--m-bug-bg); color: var(--m-bug-fg); }
  .tpill[data-type='improvement'] { background: var(--m-imp-bg); color: var(--m-imp-fg); }
  .tpill[data-type='feature'] { background: var(--m-feat-bg); color: var(--m-feat-fg); }
  .txt { font-size: 14px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
  .txt.overdue { color: var(--danger); }
  .state { position: absolute; inset: calc(var(--fold-h, 0px) + 40px) 0 auto; margin: 0; text-align: center; display: flex; gap: 10px; justify-content: center; align-items: center; padding: 0 16px; }
  .more { margin: 0; padding: 8px; text-align: center; font-size: 12px; }
</style>
