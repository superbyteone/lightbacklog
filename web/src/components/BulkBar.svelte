<script lang="ts">
  import { onMount } from 'svelte';
  import { todayISO } from '../lib/filters';
  import { app, bulkPatch, canEditTask, deleteTasksWithUndo } from '../lib/store.svelte';
  import type { Task } from '../lib/types';
  import Combobox from './Combobox.svelte';
  import Popover from './Popover.svelte';

  let { tasks, onclear, compact = false }: { tasks: Task[]; onclear: () => void; compact?: boolean } = $props();

  type Key = 'status' | 'priority' | 'type' | 'project' | 'labels' | 'due';
  let open = $state<Key | 'more' | null>(null);
  let anchors = $state<Record<string, HTMLButtonElement | undefined>>({});

  const editable = $derived(tasks.filter(canEditTask));
  const n = $derived(tasks.length);

  // Lift toasts above the bar while it is showing.
  onMount(() => {
    document.body.classList.add('has-bulk');
    return () => document.body.classList.remove('has-bulk');
  });

  const statusItems = $derived(app.statuses.map((s) => ({ value: s.slug, label: s.name, color: s.color })));
  const priorityItems = $derived(app.priorities.map((p) => ({ value: p.slug, label: p.name, color: p.color })));
  const typeItems = $derived([...app.types.map((t) => ({ value: t.slug, label: t.name, color: t.color })), { value: 'none', label: 'No type' }]);
  const projectItems = $derived(app.projects.filter((p) => !p.archived && p.role !== 'viewer').map((p) => ({ value: p.id, label: p.name, color: p.color, hint: p.key })));
  const labelItems = $derived([...app.labels].sort((a, b) => a.name.localeCompare(b.name)).map((l) => ({ value: l.name, label: l.name, color: l.color })));
  // A label is "on" when every selected task has it: picking it then removes it from all, otherwise adds it to all.
  const onAll = $derived(
    editable.length === 0 ? [] : editable[0].labels.map((l) => l.name).filter((name) => editable.every((t) => t.labels.some((x) => x.name.toLowerCase() === name.toLowerCase()))),
  );

  function pickLabel(name: string) {
    const on = onAll.some((x) => x.toLowerCase() === name.toLowerCase());
    void bulkPatch(editable, on ? { remove_labels: [name] } : { add_labels: [name] }, on ? `Removed “${name}”` : `Added “${name}”`);
  }
  function apply(patch: Parameters<typeof bulkPatch>[1], what: string) {
    open = null;
    void bulkPatch(editable, patch, what);
  }
  const btns: { key: Key; label: string }[] = [
    { key: 'status', label: 'Status' },
    { key: 'priority', label: 'Priority' },
    { key: 'type', label: 'Type' },
    { key: 'project', label: 'Project' },
    { key: 'labels', label: 'Labels' },
    { key: 'due', label: 'Due' },
  ];
</script>

{#snippet picker(key: Key, anchor: HTMLElement)}
  <Popover {anchor} onclose={() => (open = null)} width={key === 'due' ? 250 : 240}>
          {#if key === 'status'}
            <Combobox items={statusItems} placeholder="Set status for {n}…" onpick={(v) => apply({ status: v }, 'Status set')} onclose={() => (open = null)} />
          {:else if key === 'priority'}
            <Combobox items={priorityItems} placeholder="Set priority for {n}…" onpick={(v) => apply({ priority: v }, 'Priority set')} onclose={() => (open = null)} />
          {:else if key === 'type'}
            <Combobox items={typeItems} placeholder="Set type for {n}…" onpick={(v) => apply({ type: v === 'none' ? null : v }, v === 'none' ? 'Type cleared' : 'Type set')} onclose={() => (open = null)} />
          {:else if key === 'project'}
            <Combobox items={projectItems} placeholder="Move {n} to project…" onpick={(v) => apply({ project: v }, 'Moved')} onclose={() => (open = null)} />
          {:else if key === 'labels'}
            <Combobox items={labelItems} selected={onAll} multiple placeholder="Add or remove a label…" onpick={pickLabel} oncreate={(t) => pickLabel(t)} onclose={() => (open = null)} />
          {:else}
            <div class="due">
              <input class="input" type="date" aria-label="Due date for {n} tasks" onchange={(e) => e.currentTarget.value && apply({ due_date: e.currentTarget.value }, 'Due date set')} />
              <div class="quick">
                <button class="btn ghost" onclick={() => apply({ due_date: todayISO() }, 'Due date set')}>Today</button>
                <button class="btn ghost" onclick={() => apply({ due_date: todayISO(1) }, 'Due date set')}>Tomorrow</button>
                <button class="btn ghost" onclick={() => apply({ due_date: todayISO(7) }, 'Due date set')}>Next week</button>
                <button class="btn ghost danger" onclick={() => apply({ due_date: null }, 'Due date cleared')}>Clear</button>
              </div>
            </div>
          {/if}
        </Popover>
{/snippet}

{#if compact}
  <!-- Phones and tablets: the design's bottom action bar (Status, Priority, Label, More). It only exists while something is selected. -->
  <div class="cbar" role="toolbar" aria-label="Actions for {n} selected tasks">
    <button class="cb" bind:this={anchors.status} aria-haspopup="listbox" aria-expanded={open === 'status'} disabled={editable.length === 0} onclick={() => (open = open === 'status' ? null : 'status')}>
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="12" cy="12" r="8.5" /><circle cx="12" cy="12" r="3.5" fill="currentColor" stroke="none" /></svg>Status</button>
    <button class="cb" bind:this={anchors.priority} aria-haspopup="listbox" aria-expanded={open === 'priority'} disabled={editable.length === 0} onclick={() => (open = open === 'priority' ? null : 'priority')}>
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 21V4h11l-2.5 4.5L17 13H6" /></svg>Priority</button>
    <button class="cb" bind:this={anchors.labels} aria-haspopup="listbox" aria-expanded={open === 'labels'} disabled={editable.length === 0} onclick={() => (open = open === 'labels' ? null : 'labels')}>
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3.5 12.2V4.5a1 1 0 0 1 1-1h7.7a1 1 0 0 1 .7.3l7.8 7.8a1 1 0 0 1 0 1.4l-7.7 7.7a1 1 0 0 1-1.4 0l-7.8-7.8a1 1 0 0 1-.3-.7Z" /><circle cx="8.5" cy="8.5" r="1.3" fill="currentColor" stroke="none" /></svg>Label</button>
    <button class="cb" bind:this={anchors.more} aria-haspopup="menu" aria-expanded={open === 'more'} onclick={() => (open = open === 'more' ? null : 'more')}>
      <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.8" /><circle cx="12" cy="12" r="1.8" /><circle cx="19" cy="12" r="1.8" /></svg>More</button>
    {#each ['status', 'priority', 'labels', 'type', 'project', 'due'] as const as k (k)}
      {#if open === k}{@render picker(k, anchors[k] ?? anchors.more!)}{/if}
    {/each}
    {#if open === 'more' && anchors.more}
      <Popover anchor={anchors.more} width={230} onclose={() => (open = null)}>
        <div class="more" role="menu" aria-label="More actions">
          <button role="menuitem" disabled={editable.length === 0} onclick={() => (open = 'type')}>Set type…</button>
          <button role="menuitem" disabled={editable.length === 0} onclick={() => (open = 'project')}>Move to project…</button>
          <button role="menuitem" disabled={editable.length === 0} onclick={() => (open = 'due')}>Set due date…</button>
          <button role="menuitem" class="danger" disabled={editable.length === 0} onclick={() => { open = null; deleteTasksWithUndo(editable); onclear(); }}>Delete {n === 1 ? 'task' : `${n} tasks`}</button>
        </div>
      </Popover>
    {/if}
  </div>
{:else}
<div class="bar" role="toolbar" aria-label="Actions for {n} selected tasks">
  <strong class="count">{n} selected</strong>
  <div class="btns">
    {#each btns as b (b.key)}
      <button class="btn" bind:this={anchors[b.key]} aria-haspopup="listbox" aria-expanded={open === b.key} disabled={editable.length === 0} onclick={() => (open = open === b.key ? null : b.key)}>{b.label} ▾</button>
      {#if open === b.key && anchors[b.key]}{@render picker(b.key, anchors[b.key]!)}{/if}
    {/each}
    <button class="btn danger" disabled={editable.length === 0} onclick={() => { deleteTasksWithUndo(editable); onclear(); }}>Delete</button>
  </div>
  <button class="icon-btn" aria-label="Clear selection" title="Clear selection (Esc)" onclick={onclear}>✕</button>
</div>
{/if}

<style>
  .bar {
    position: absolute; z-index: 30; left: 50%; bottom: 14px; transform: translateX(-50%); display: flex; align-items: center; gap: 10px;
    max-width: calc(100% - 24px); padding: 8px 10px; border: 1px solid var(--line-strong); border-radius: 12px; background: var(--bg); box-shadow: var(--shadow);
  }
  .count { white-space: nowrap; padding-left: 4px; }
  .btns { display: flex; flex-wrap: wrap; gap: 6px; min-width: 0; }
  .due { display: grid; gap: 8px; padding: 10px; }
  .quick { display: flex; flex-wrap: wrap; gap: 4px; }
  .quick .btn { height: 28px; padding: 0 8px; }
  .cbar { position: absolute; z-index: 30; left: 0; right: 0; bottom: 0; display: grid; grid-template-columns: repeat(4, 1fr); padding: 4px 8px calc(4px + env(safe-area-inset-bottom)); border-top: 1px solid var(--m-line); background: var(--m-card); }
  .cb { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 2px; min-height: 48px; border: 0; border-radius: 6px; background: transparent; font-size: 11px; font-weight: 500; }
  .cb:active { background: var(--m-chip); }
  .cb:disabled { opacity: 0.45; }
  .more { display: grid; padding: 4px 0; }
  .more button { min-height: 36px; padding: 0 12px; border: 0; background: transparent; text-align: left; font-size: 14px; }
  .more button:active { background: var(--m-chip); }
  .more .danger { color: var(--danger); }
  @media (max-width: 800px) {
    .bar { left: 8px; right: 8px; bottom: 8px; transform: none; max-width: none; flex-wrap: wrap; }
    .btns { flex: 1 1 100%; order: 3; }
    .count { flex: 1; }
  }
</style>
