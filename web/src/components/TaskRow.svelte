<script lang="ts">
  import { todayISO } from '../lib/filters';
  import type { ResolvedColumn } from '../lib/layout.svelte';
  import { app, patchTask, priorityBySlug, projectById, statusBySlug } from '../lib/store.svelte';
  import type { Task } from '../lib/types';
  import DueCell from './DueCell.svelte';
  import LabelsCell from './LabelsCell.svelte';
  import PickerCell from './PickerCell.svelte';
  import SequenceCell from './SequenceCell.svelte';
  import TypeCell from './TypeCell.svelte';

  let { task, columns, active, top, selected = false, onselect, onopen }: { task: Task; columns: ResolvedColumn[]; active: boolean; top: number; selected?: boolean; onselect: (id: string, shift: boolean) => void; onopen: (ref: string) => void } = $props();

  const project = $derived(projectById(task.project.id));
  const readonly = $derived(!project || project.role === 'viewer' || project.archived);
  const done = $derived(!!statusBySlug(task.status)?.is_done);
  const overdue = $derived(!!task.due_date && !done && task.due_date < todayISO());

  const statusItems = $derived(app.statuses.map((s) => ({ value: s.slug, label: s.name, color: s.color })));
  const priorityItems = $derived(app.priorities.map((p) => ({ value: p.slug, label: p.name, color: p.color })));
  const projectItems = $derived(app.projects.filter((p) => !p.archived && p.role !== 'viewer').map((p) => ({ value: p.id, label: p.name, color: p.color, hint: p.key })));

  let editing = $state(false);
  let draft = $state('');
  let input = $state<HTMLInputElement>();

  // A single click opens the task; a double click edits the title in place. Wait briefly so
  // the first click of a double click does not open the panel.
  let openTimer: ReturnType<typeof setTimeout> | undefined;
  function clickTitle() {
    clearTimeout(openTimer);
    openTimer = setTimeout(() => onopen(task.ref), 230);
  }
  function edit() {
    clearTimeout(openTimer);
    if (readonly) return;
    draft = task.title;
    editing = true;
  }
  $effect(() => {
    if (editing) {
      input?.focus();
      input?.select();
    }
  });
  function commit() {
    if (!editing) return;
    editing = false;
    const v = draft.trim();
    if (v && v !== task.title) void patchTask(task.id, { title: v });
  }
</script>

<div class="row" class:active class:selected class:done role="row" data-ref={task.ref} style="top:{top}px">
  <div class="cell sel" role="gridcell"><label class="selbox"><input type="checkbox" checked={selected} aria-label="Select {task.ref}" onclick={(e) => onselect(task.id, e.shiftKey)} /></label></div>
  {#each columns as c (c.id)}
    {#if c.id === 'ref'}
      <div class="cell ref" role="gridcell"><button class="ref-btn" onclick={() => onopen(task.ref)} tabindex="-1" aria-label="Open {task.ref}">{task.ref}</button></div>
    {:else if c.id === 'title'}
      <div class="cell title" role="gridcell">
        {#if editing}
          <input class="input title-input" bind:this={input} bind:value={draft} aria-label="Task title"
            onblur={commit}
            onkeydown={(e) => { if (e.key === 'Enter') commit(); else if (e.key === 'Escape') { e.stopPropagation(); editing = false; } }} />
        {:else}
          <button class="title-btn" onclick={clickTitle} ondblclick={edit} title={task.title}>{task.title}</button>
          {#if !readonly}<button class="icon-btn edit-btn" aria-label="Edit title" tabindex="-1" onclick={edit}>✎</button>{/if}
        {/if}
      </div>
    {:else if c.id === 'project'}
      <div class="cell" role="gridcell">
        <PickerCell label="Project" value={task.project.id} items={projectItems} {readonly} placeholder="Move to project…" onchange={(v) => patchTask(task.id, { project: v })}>
          {#snippet children()}<span class="pill"><span class="dot" style="--dot:{task.project.color}"></span>{task.project.name}</span>{/snippet}
        </PickerCell>
      </div>
    {:else if c.id === 'status'}
      <div class="cell" role="gridcell">
        <PickerCell label="Status" value={task.status} items={statusItems} {readonly} placeholder="Set status…" onchange={(v) => patchTask(task.id, { status: v })} />
      </div>
    {:else if c.id === 'priority'}
      <div class="cell" role="gridcell">
        <PickerCell label="Priority" value={task.priority} items={priorityItems} {readonly} placeholder="Set priority…" onchange={(v) => patchTask(task.id, { priority: v })} />
      </div>
    {:else if c.id === 'type'}
      <div class="cell" role="gridcell">
        <TypeCell value={task.type} {readonly} onchange={(v) => patchTask(task.id, { type: v })} />
      </div>
    {:else if c.id === 'sequence'}
      <div class="cell" role="gridcell">
        <SequenceCell value={task.sequence} {readonly} onchange={(v) => patchTask(task.id, { sequence: v })} />
      </div>
    {:else if c.id === 'labels'}
      <div class="cell" role="gridcell">
        <LabelsCell labels={task.labels} {readonly} onchange={(names) => patchTask(task.id, { labels: names })} />
      </div>
    {:else if c.id === 'due'}
      <div class="cell" role="gridcell">
        <DueCell value={task.due_date} {overdue} {readonly} onchange={(v) => patchTask(task.id, { due_date: v })} />
      </div>
    {:else if c.id === 'created' || c.id === 'updated' || c.id === 'completed'}
      {@const iso = c.id === 'created' ? task.created_at : c.id === 'updated' ? task.updated_at : task.completed_at}
      <div class="cell" role="gridcell">
        {#if iso}<time class="date" datetime={iso} title={new Date(iso).toLocaleString()}>{new Date(iso).toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}</time>{/if}
      </div>
    {/if}
  {/each}
</div>

<style>
  .row { position: absolute; left: 0; right: 0; display: grid; grid-template-columns: var(--cols); align-items: center; height: var(--row-h); border-bottom: 1px solid var(--line); }
  .row:hover { background: var(--bg-soft); }
  .row.active, .row.selected { background: var(--accent-soft); }
  .cell.sel { padding: 0; }
  .selbox { display: grid; place-items: center; width: 100%; height: 100%; cursor: pointer; }
  .cell { min-width: 0; height: 100%; display: flex; align-items: center; gap: 4px; padding: 0 8px; }
  .cell.title { padding-left: 4px; }
  .ref-btn { border: 0; background: transparent; color: var(--muted); font: 12px ui-monospace, SFMono-Regular, Menlo, monospace; padding: 2px 4px; border-radius: 4px; }
  .ref-btn:hover { color: var(--accent); background: var(--bg-hover); }
  .title-btn { flex: 1; min-width: 0; padding: 0; border: 0; background: transparent; text-align: left; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  .title-btn:hover { color: var(--accent); }
  .row.done .title-btn { color: var(--muted); text-decoration: line-through; text-decoration-color: color-mix(in srgb, var(--muted) 60%, transparent); }
  .edit-btn { flex: none; opacity: 0; width: 22px; height: 22px; font-size: 12px; }
  .row:hover .edit-btn, .edit-btn:focus-visible { opacity: 1; }
  .title-input { height: 28px; }
  .date { color: var(--muted); font-size: 12px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
