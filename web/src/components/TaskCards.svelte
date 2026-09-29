<script lang="ts">
  import { todayISO } from '../lib/filters';
  import { layout, visibleColumns, type ColumnId } from '../lib/layout.svelte';
  import { app, cache, patchTask, priorityBySlug, projectById, statusBySlug, type TaskList } from '../lib/store.svelte';
  import DueCell from './DueCell.svelte';
  import LabelsCell from './LabelsCell.svelte';
  import PickerCell from './PickerCell.svelte';
  import TypeCell from './TypeCell.svelte';

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

  // Card height is fixed for a given set of fields (so the list can be windowed like the table) but shrinks
  // when the top line (ID/project/sequence), the title, the field pills or the labels/dates line are switched off.
  const ROW_TOP = 22, ROW_TITLE = 24, ROW_PILLS = 28, ROW_META = 20, ROW_GAP = 4, CARD_CHROME = 15; // CARD_CHROME: padding + borders
  const OVERSCAN = 4;
  let scroller = $state<HTMLDivElement>();
  let scrollTop = $state(0);
  let viewH = $state(600);

  // Which fields a card shows comes from its own saved layout, independent of the table's columns (on desktop).
  const columns = $derived((layout.viewport, visibleColumns(showProject, 'cards')));
  const ids = $derived(columns.map((c) => c.id));
  const pillFields = $derived(ids.filter((id) => ['status', 'priority', 'type'].includes(id)) as ColumnId[]);
  const dateFields = $derived(ids.filter((id) => ['created', 'updated', 'completed'].includes(id)) as ColumnId[]);
  const has = (id: ColumnId) => ids.includes(id);
  const hasMeta = $derived(has('labels') || dateFields.length > 0);

  // Sequence sits in the top-right corner of the ID line; without that line it moves down beside the title.
  const hasTop = $derived(has('ref') || has('project'));
  const seqInTitle = $derived(!hasTop && has('sequence'));
  const hasTitleRow = $derived(has('title') || seqInTitle); // the Due badge rides beside the title
  const rows = $derived([hasTop ? ROW_TOP : 0, hasTitleRow ? ROW_TITLE : 0, pillFields.length ? ROW_PILLS : 0, hasMeta ? ROW_META : 0].filter((h) => h > 0));
  // The checkbox is centred on the first line the card shows (ID line, title line, pills or labels) so they sit on one horizontal line.
  const firstLine = $derived(hasTop ? ROW_TOP : hasTitleRow ? ROW_TITLE : pillFields.length ? ROW_PILLS : ROW_META);
  const selTop = $derived(8 + firstLine / 2 - 17); // 8px body padding + half the first line - half the 34px checkbox box
  const CARD = $derived(Math.max(44, rows.reduce((a, b) => a + b, 0) + ROW_GAP * Math.max(0, rows.length - 1) + CARD_CHROME));
  const STRIDE = $derived(CARD + 8);

  const dateLabel = { created: 'Created', updated: 'Updated', completed: 'Completed' } as const;
  const fmtDate = (iso: string) => new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });

  const start = $derived(Math.max(0, Math.floor(scrollTop / STRIDE) - OVERSCAN));
  const end = $derived(Math.min(list.ids.length, Math.ceil((scrollTop + viewH) / STRIDE) + OVERSCAN));
  const visible = $derived(list.ids.slice(start, end));
  const allSelected = $derived(list.ids.length > 0 && list.ids.every((id) => selected.has(id)));
  const someSelected = $derived(!allSelected && list.ids.some((id) => selected.has(id)));
  const sortField = $derived(sort.replace(/^-/, ''));
  const desc = $derived(sort.startsWith('-'));

  $effect(() => {
    if (end >= list.ids.length - 15 && list.hasMore && !list.loading) void list.more();
  });

  const sorts = [
    ['updated_at', 'Updated'], ['created_at', 'Newest'], ['completed_at', 'Completed'], ['title', 'Title'], ['status', 'Status'], ['priority', 'Priority'],
    ['sequence', 'Sequence'], ['type', 'Type'], ['due_date', 'Due date'], ['project', 'Project'],
  ] as const;
  const statusItems = $derived(app.statuses.map((s) => ({ value: s.slug, label: s.name, color: s.color })));
  const priorityItems = $derived(app.priorities.map((p) => ({ value: p.slug, label: p.name, color: p.color })));
</script>

<div class="cards">
  <div class="strip">
    <label class="selbox" title="Select all {list.ids.length} loaded tasks"><input type="checkbox" checked={allSelected} indeterminate={someSelected} disabled={list.ids.length === 0} aria-label="Select all loaded tasks" onclick={() => onselectall()} /></label>
    <label class="sort">Sort
      <select class="input" value={sortField} aria-label="Sort by" onchange={(e) => onsort(e.currentTarget.value)}>
        {#each sorts as [v, l]}<option value={v}>{l}</option>{/each}
      </select>
    </label>
    <button class="btn" aria-label={desc ? 'Descending: click for ascending' : 'Ascending: click for descending'} title={desc ? 'Descending' : 'Ascending'} onclick={() => onsort(sortField)}>{desc ? '↓' : '↑'}</button>
  </div>

  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="scroll" role="list" tabindex="0" aria-label="Tasks" bind:this={scroller} bind:clientHeight={viewH} onscroll={(e) => (scrollTop = e.currentTarget.scrollTop)}>
    <div class="inner" style="height:{list.ids.length * STRIDE}px">
      {#each visible as id, i (id)}
        {@const task = cache[id]}
        {#if task}
          {@const project = projectById(task.project.id)}
          {@const readonly = !project || project.role === 'viewer' || project.archived}
          {@const done = !!statusBySlug(task.status)?.is_done}
          <div class="card" class:selected={selected.has(id)} class:active={task.ref === activeRef} class:done role="listitem" data-ref={task.ref} style="top:{(start + i) * STRIDE}px; height:{CARD}px">
            <label class="selbox csel" style="margin-top:{selTop}px"><input type="checkbox" checked={selected.has(id)} aria-label="Select {task.ref}" onclick={(e) => onselect(id, e.shiftKey)} /></label>
            <div class="body">
              {#if hasTop}<div class="top">
                {#if has('ref')}<button class="ref" onclick={() => onopen(task.ref)} aria-label="Open {task.ref}">{task.ref}</button>{/if}
                {#if has('project')}<span class="muted proj"><span class="dot" style="--dot:{task.project.color}"></span>{task.project.name}</span>{/if}
                <span class="spacer"></span>
                {#if has('sequence') && task.sequence !== null}<span class="seq" title="Order of work: 1 is done first">#{task.sequence}</span>{/if}
              </div>{/if}
              {#if hasTitleRow}
                <div class="trow">
                  {#if has('title')}<button class="title" title={task.title} onclick={() => onopen(task.ref)}>{task.title}</button>{/if}
                  {#if has('due') && task.due_date}<span class="dueslot"><DueCell value={task.due_date} badge overdue={!done && task.due_date < todayISO()} {readonly} onchange={(v) => patchTask(task.id, { due_date: v })} /></span>{/if}
                  <span class="spacer"></span>
                  {#if seqInTitle && task.sequence !== null}<span class="seq">#{task.sequence}</span>{/if}
                </div>
              {/if}
              {#if pillFields.length}<div class="pills">
                {#each pillFields as f (f)}
                  <div class="slot">
                    {#if f === 'status'}<PickerCell label="Status" value={task.status} items={statusItems} {readonly} placeholder="Set status…" onchange={(v) => patchTask(task.id, { status: v })} />
                    {:else if f === 'priority'}<PickerCell label="Priority" value={task.priority} items={priorityItems} {readonly} placeholder="Set priority…" onchange={(v) => patchTask(task.id, { priority: v })} />
                    {:else if f === 'type'}<TypeCell value={task.type} {readonly} onchange={(v) => patchTask(task.id, { type: v })} />{/if}
                  </div>
                {/each}
              </div>{/if}
              {#if hasMeta}<div class="meta">
                {#if has('labels')}<div class="lbls"><LabelsCell plain labels={task.labels} max={3} {readonly} onchange={(names) => patchTask(task.id, { labels: names })} /></div>{/if}
                <span class="spacer"></span>
                {#if dateFields.length}<span class="dates">
                  {#each dateFields.map((f) => [f, f === 'created' ? task.created_at : f === 'updated' ? task.updated_at : task.completed_at] as const).filter(([, iso]) => iso) as [f, iso], i (f)}
                    {#if i > 0}<span class="sep">·</span>{/if}<span>{dateLabel[f as 'created' | 'updated' | 'completed']} {fmtDate(iso!)}</span>
                  {/each}
                </span>{/if}
              </div>{/if}
            </div>
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
</div>

<style>
  .cards { display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; }
  .strip { display: flex; align-items: center; gap: 10px; flex: none; padding: 6px 12px; border-bottom: 1px solid var(--line); background: var(--bg-soft); }
  .selbox { display: grid; place-items: center; width: 34px; height: 34px; cursor: pointer; }
  .sort { display: flex; align-items: center; gap: 8px; color: var(--muted); font-size: 13px; margin-left: auto; }
  .sort .input { width: auto; height: 32px; }
  .scroll { position: relative; flex: 1; overflow-y: auto; overflow-x: hidden; padding: 8px 10px; outline: none; }
  .inner { position: relative; }
  .card { position: absolute; left: 0; right: 0; display: grid; grid-template-columns: 34px 1fr; align-items: stretch; overflow: hidden; border: 1px solid var(--line); border-radius: 10px; background: var(--bg); box-shadow: 0 1px 1px rgba(16, 24, 40, 0.04); }
  .card.selected { background: var(--accent-soft); border-color: var(--accent); }
  .card.active { border-color: var(--accent); }
  .csel { align-self: start; }
  .body { display: flex; flex-direction: column; gap: 4px; min-width: 0; padding: 8px 10px 8px 0; }
  .top { display: flex; align-items: center; gap: 8px; min-width: 0; height: 22px; }
  .ref { padding: 0; border: 0; background: transparent; color: var(--muted); font: 12px ui-monospace, SFMono-Regular, Menlo, monospace; }
  .proj { display: inline-flex; align-items: center; gap: 6px; min-width: 0; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; font-size: 12px; }
  .trow { display: flex; align-items: center; gap: 10px; min-width: 0; height: 24px; }
  .trow .title { flex: 0 1 auto; min-width: 0; }
  .dueslot { flex: none; display: flex; align-items: center; }
  .seq { flex: none; font-size: 12px; font-weight: 400; color: #6b7280; font-variant-numeric: tabular-nums; }
  .title { padding: 0; border: 0; background: transparent; text-align: left; font-size: 15px; font-weight: 600; line-height: 24px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .card.done .title { color: var(--muted); text-decoration: line-through; text-decoration-color: color-mix(in srgb, var(--muted) 60%, transparent); }
  .pills { display: flex; align-items: center; gap: 6px; min-width: 0; overflow: hidden; }
  .meta { display: flex; align-items: center; gap: 12px; min-width: 0; height: 20px; }
  .lbls { flex: 0 1 auto; min-width: 0; height: 20px; overflow: hidden; }
  .dates { flex: none; display: flex; align-items: center; gap: 8px; font-size: 11px; color: #9aa0ab; white-space: nowrap; }
  .sep { color: #d5d8de; }
  .slot { flex: none; max-width: 100%; height: 28px; display: flex; align-items: center; }
  .state { position: absolute; inset: 40px 0 auto; margin: 0; text-align: center; display: flex; gap: 10px; justify-content: center; align-items: center; }
  .more { margin: 0; padding: 8px; text-align: center; font-size: 12px; }
</style>
