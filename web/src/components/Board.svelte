<script lang="ts">
  import { todayISO } from '../lib/filters';
  import { app, cache, createTask, errorText, patchTask, priorityBySlug, projectByKey, toast, type TaskList } from '../lib/store.svelte';
  import type { Task } from '../lib/types';

  let { list, projectKey, onopen, activeRef }: { list: TaskList; projectKey: string; onopen: (ref: string) => void; activeRef?: string } = $props();

  const project = $derived(projectByKey(projectKey));
  const canEdit = $derived(!!project && project.role !== 'viewer' && !project.archived);

  // Every column lists its tasks newest first (by creation time), so where a card sits is never a manual choice: dragging only changes its status.
  const columns = $derived(
    app.statuses.map((s) => ({
      status: s,
      tasks: list.ids
        .map((id) => cache[id])
        .filter((t): t is Task => !!t && t.status === s.slug)
        .sort((a, b) => b.created_at.localeCompare(a.created_at) || b.number - a.number),
    })),
  );

  let dragId = $state<string | null>(null);
  let dropStatus = $state<string | null>(null);
  let adding = $state<string | null>(null);
  let newTitle = $state('');

  function onDragStart(e: DragEvent, t: Task) {
    if (!canEdit) return e.preventDefault();
    dragId = t.id;
    e.dataTransfer!.effectAllowed = 'move';
    e.dataTransfer!.setData('text/plain', t.id);
  }

  function onDragOver(e: DragEvent, status: string) {
    if (!dragId) return;
    e.preventDefault();
    e.dataTransfer!.dropEffect = 'move';
    if (dropStatus !== status) dropStatus = status;
  }

  function onDrop(e: DragEvent, status: string) {
    e.preventDefault();
    const id = dragId;
    dragId = null;
    dropStatus = null;
    const t = id ? cache[id] : undefined;
    if (t && t.status !== status) void patchTask(t.id, { status });
  }

  function onKey(e: KeyboardEvent, t: Task) {
    if (e.key === 'Enter' && e.target === e.currentTarget) return onopen(t.ref);
    if (!canEdit || !e.altKey || (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight')) return;
    e.preventDefault();
    const i = app.statuses.findIndex((s) => s.slug === t.status);
    const next = app.statuses[i + (e.key === 'ArrowRight' ? 1 : -1)];
    if (next) void patchTask(t.id, { status: next.slug });
  }

  async function addCard(status: string) {
    const title = newTitle.trim();
    if (!title || !project) return;
    try {
      await createTask({ project: project.id, title, status });
      newTitle = '';
    } catch (e) {
      toast(`Couldn't create task: ${errorText(e)}`, 'error');
    }
  }
</script>

<div class="board" role="list" aria-label="Kanban board">
  {#each columns as col (col.status.slug)}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <section class="col" class:over={dropStatus === col.status.slug} role="listitem" aria-label="{col.status.name}, {col.tasks.length} tasks"
      ondragover={(e) => onDragOver(e, col.status.slug)} ondrop={(e) => onDrop(e, col.status.slug)}
      ondragleave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) dropStatus = dropStatus === col.status.slug ? null : dropStatus; }}>
      <header><span class="dot" style="--dot:{col.status.color}"></span><strong>{col.status.name}</strong><span class="muted n">{col.tasks.length}</span></header>
      <div class="cards">
        {#each col.tasks as t (t.id)}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <div class="card" class:dragging={dragId === t.id} class:active={t.ref === activeRef} data-card data-id={t.id} data-ref={t.ref} role="button" tabindex="0"
            draggable={canEdit} ondragstart={(e) => onDragStart(e, t)} ondragend={() => { dragId = null; dropStatus = null; }}
            onclick={() => onopen(t.ref)} onkeydown={(e) => onKey(e, t)} aria-label="{t.ref} {t.title}">
            <div class="top"><span class="ref">{t.ref}{#if t.sequence !== null} <b class="seq" title="Order of work">#{t.sequence}</b>{/if}</span>
              <span class="prio" title="{priorityBySlug(t.priority)?.name ?? t.priority} priority"><span class="dot" style="--dot:{priorityBySlug(t.priority)?.color}"></span>{priorityBySlug(t.priority)?.name}</span></div>
            {#if t.type}{@const tt = app.types.find((x) => x.slug === t.type)}<span class="tbadge" style="--c:{tt?.color ?? '#6b7280'}">{tt?.name ?? t.type}</span>{/if}
            <div class="title">{t.title}</div>
            {#if t.labels.length || t.due_date}
              <div class="meta">
                {#each t.labels.slice(0, 3) as l (l.id)}<span class="chip" style="--chip:{l.color}">{l.name}</span>{/each}
                {#if t.labels.length > 3}<span class="muted">+{t.labels.length - 3}</span>{/if}
                {#if t.due_date}<span class="due" class:overdue={t.due_date < todayISO() && !col.status.is_done}>{new Date(t.due_date + 'T00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</span>{/if}
              </div>
            {/if}
          </div>
        {/each}
        {#if col.tasks.length === 0 && dropStatus !== col.status.slug}<p class="muted empty">No tasks</p>{/if}
      </div>
      {#if canEdit}
        {#if adding === col.status.slug}
          <form class="add" onsubmit={(e) => { e.preventDefault(); void addCard(col.status.slug); }}>
            <input class="input" placeholder="Task title, Enter to add" bind:value={newTitle} aria-label="New task title in {col.status.name}"
              onkeydown={(e) => { if (e.key === 'Escape') { e.stopPropagation(); adding = null; newTitle = ''; } }} onblur={() => { if (!newTitle.trim()) adding = null; }} />
          </form>
        {:else}
          <button class="btn ghost addbtn" onclick={async () => { adding = col.status.slug; await Promise.resolve(); document.querySelector<HTMLInputElement>('.add input')?.focus(); }}>+ Add task</button>
        {/if}
      {/if}
    </section>
  {/each}
  {#if list.loading && list.ids.length === 0}<p class="muted loading">Loading board…</p>{/if}
  {#if list.error}<p class="error-text loading">{list.error}</p>{/if}
</div>

<style>
  .board { display: flex; gap: 12px; height: 100%; padding: 12px 16px 16px; overflow: auto; }
  .col { display: flex; flex-direction: column; flex: 1 0 260px; max-width: 360px; min-height: 0; border: 1px solid var(--line); border-radius: 10px; background: var(--bg-soft); }
  .col.over { border-color: var(--accent); background: var(--accent-soft); }
  .col > header { display: flex; align-items: center; gap: 8px; padding: 10px 12px 6px; }
  .n { font-size: 12px; }
  .cards { flex: 1; min-height: 40px; overflow: auto; padding: 4px 8px 8px; display: flex; flex-direction: column; gap: 8px; }
  .card { padding: 9px 10px; border: 1px solid var(--line); border-radius: 8px; background: var(--bg); box-shadow: 0 1px 1px rgba(16, 24, 40, 0.04); cursor: grab; }
  .card:hover { border-color: var(--line-strong); }
  .card.active { border-color: var(--accent); }
  .card.dragging { opacity: 0.4; }
  .card:active { cursor: grabbing; }
  .top { display: flex; align-items: center; justify-content: space-between; gap: 8px; font-size: 12px; color: var(--muted); }
  .tbadge { display: inline-block; margin-top: 4px; padding: 0 7px; border-radius: 10px; font-size: 11.5px; font-weight: 600; color: var(--c); background: color-mix(in srgb, var(--c) 14%, transparent); border: 1px solid color-mix(in srgb, var(--c) 35%, transparent); }
  .seq { color: var(--accent); font-weight: 700; }
  .ref { font: 12px ui-monospace, SFMono-Regular, Menlo, monospace; }
  .prio { display: inline-flex; align-items: center; gap: 5px; }
  .title { margin: 4px 0 2px; font-weight: 550; display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; }
  .meta { display: flex; flex-wrap: wrap; align-items: center; gap: 4px; margin-top: 6px; }
  .due { margin-left: auto; font-size: 12px; color: var(--muted); }
  .due.overdue { color: var(--danger); font-weight: 600; }
  .empty { margin: 6px; text-align: center; font-size: 13px; }
  .add { padding: 0 8px 8px; }
  .addbtn { justify-content: flex-start; margin: 0 6px 6px; color: var(--muted); }
  .loading { padding: 12px; }
</style>
