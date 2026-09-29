<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, api, get } from '../lib/api';
  import { todayISO } from '../lib/filters';
  import { setParams } from '../lib/router.svelte';
  import { app, cache, deleteTasksWithUndo, errorText, loadTask, patchTask, projectById, statusBySlug, toast } from '../lib/store.svelte';
  import type { Attachment, Task } from '../lib/types';
  import { ui } from '../lib/ui.svelte';
  import DueCell from './DueCell.svelte';
  import Editor from './Editor.svelte';
  import LabelsCell from './LabelsCell.svelte';
  import PickerCell from './PickerCell.svelte';
  import SequenceCell from './SequenceCell.svelte';
  import TypeCell from './TypeCell.svelte';

  let { taskRef }: { taskRef: string } = $props();

  let id = $state<string | null>(null);
  let error = $state('');
  let loading = $state(true);
  let draftDesc = $state('');
  let draftTitle = $state('');
  let saveState = $state<'idle' | 'dirty' | 'saving' | 'saved'>('idle');
  let attachments = $state<Attachment[]>([]);
  let fileInput = $state<HTMLInputElement>();
  let uploading = $state(false);
  let confirmDelete = $state(false);
  let timer: ReturnType<typeof setTimeout>;
  let panel = $state<HTMLElement>();

  const task = $derived(id ? (cache[id] as Task | undefined) : undefined);
  const project = $derived(task ? projectById(task.project.id) : undefined);
  const readonly = $derived(!project || project.role === 'viewer' || project.archived);
  const done = $derived(!!task && !!statusBySlug(task.status)?.is_done);
  const overdue = $derived(!!task?.due_date && !done && task.due_date < todayISO());

  const titleDirty = $derived(!!task && draftTitle.trim() !== '' && draftTitle.trim() !== task.title);
  const dirty = $derived(titleDirty || saveState === 'dirty' || saveState === 'saving');
  const saveLabel = $derived(saveState === 'saving' ? 'Saving…' : dirty ? 'Save' : 'Saved');

  const statusItems = $derived(app.statuses.map((s) => ({ value: s.slug, label: s.name, color: s.color })));
  const priorityItems = $derived(app.priorities.map((p) => ({ value: p.slug, label: p.name, color: p.color })));
  const projectItems = $derived(app.projects.filter((p) => !p.archived && p.role !== 'viewer').map((p) => ({ value: p.id, label: p.name, color: p.color, hint: p.key })));

  $effect(() => {
    const ref = taskRef;
    id = null;
    error = '';
    loading = true;
    confirmDelete = false;
    saveState = 'idle';
    loadTask(ref)
      .then(async (t) => {
        id = t.id;
        draftDesc = t.description ?? '';
        draftTitle = t.title;
        loading = false;
        attachments = (await get<Attachment[]>(`/api/v1/tasks/${t.id}/attachments`)).data;
      })
      .catch((e) => {
        loading = false;
        error = e instanceof ApiError && e.status === 404 ? 'This task does not exist or you no longer have access.' : errorText(e);
      });
  });

  // Follow renames/moves so the URL keeps pointing at the task's current ref.
  $effect(() => {
    if (task && task.ref !== taskRef && task.id === id) setParams((p) => p.set('task', task!.ref), { replace: true });
  });

  function close() {
    if (ui.panelPushed) {
      ui.panelPushed = false;
      history.back();
    } else setParams((p) => p.delete('task'), { replace: true });
  }

  function typing(t: EventTarget | null) {
    return t instanceof HTMLElement && !!t.closest('input, textarea, select, [contenteditable="true"]');
  }

  onMount(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !e.defaultPrevented) {
        if (typing(e.target)) (e.target as HTMLElement).blur();
        else if (!ui.create && !ui.palette) close();
      }
    };
    document.addEventListener('keydown', key);
    return () => {
      document.removeEventListener('keydown', key);
      clearTimeout(timer);
    };
  });

  // Size the title box to its wrapped text so a long title is fully visible without scrolling.
  function grow(node: HTMLTextAreaElement, _value: string) {
    const fit = () => {
      node.style.height = 'auto';
      node.style.height = `${node.scrollHeight + node.offsetHeight - node.clientHeight}px`; // scrollHeight leaves out the border
    };
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(node);
    return { update: fit, destroy: () => ro.disconnect() };
  }

  function commitTitle() {
    const v = draftTitle.trim();
    if (!task) return;
    if (!v) draftTitle = task.title;
    else if (v !== task.title) void patchTask(task.id, { title: v });
  }

  function saveAll() {
    if (!task) return;
    commitTitle();
    if (saveState === 'dirty') void saveDesc();
  }

  function descChanged() {
    saveState = 'dirty';
    clearTimeout(timer);
    timer = setTimeout(saveDesc, 1200);
  }

  async function saveDesc() {
    clearTimeout(timer);
    if (!task || saveState !== 'dirty' || draftDesc === (task.description ?? '')) {
      if (saveState === 'dirty') saveState = 'idle';
      return;
    }
    saveState = 'saving';
    const ok = await patchTask(task.id, { description: draftDesc });
    saveState = ok ? 'saved' : 'dirty';
  }

  async function upload(files: FileList | null) {
    if (!files || !task) return;
    uploading = true;
    try {
      for (const f of Array.from(files)) {
        const fd = new FormData();
        fd.append('file', f, f.name);
        const r = await api<Attachment>('POST', `/api/v1/tasks/${task.id}/attachments`, fd);
        attachments = [...attachments, r.data];
      }
    } catch (e) {
      toast(`Upload failed: ${errorText(e)}`, 'error');
    } finally {
      uploading = false;
    }
  }

  /** Upload used by the editor (paste, drop, toolbar); the file also appears in the attachment list. */
  async function uploadInline(file: File) {
    if (!task) throw new Error('task not loaded');
    const fd = new FormData();
    fd.append('file', file, file.name || 'image.png');
    const r = await api<Attachment>('POST', `/api/v1/tasks/${task.id}/attachments`, fd);
    attachments = [...attachments, r.data];
    return { url: r.data.url, inline: r.data.inline, filename: r.data.filename };
  }

  async function removeAttachment(a: Attachment) {
    try {
      await api('DELETE', `/api/v1/attachments/${a.id}`);
      attachments = attachments.filter((x) => x.id !== a.id);
    } catch (e) {
      toast(`Couldn't delete: ${errorText(e)}`, 'error');
    }
  }

  /** Deletes with a 10-second Undo (the toast is shown by the store). Close the panel first: the task leaves the cache. */
  function remove() {
    if (!task) return;
    const victim = $state.snapshot(task) as Task;
    ui.panelPushed = false;
    setParams((p) => p.delete('task'), { replace: true });
    deleteTasksWithUndo([victim]);
  }

  function fmt(iso: string) {
    return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  }
  function size(n: number) {
    return n > 1 << 20 ? (n / (1 << 20)).toFixed(1) + ' MB' : Math.max(1, Math.round(n / 1024)) + ' KB';
  }
</script>

<aside class="panel" bind:this={panel} aria-label="Task details">
  <header>
    <span class="ref">{task?.ref ?? taskRef}</span>
    {#if task}<span class="pill"><span class="dot" style="--dot:{task.project.color}"></span>{task.project.name}</span>{/if}
    <span class="spacer"></span>
    {#if task && !readonly}
      <button class="btn {dirty ? 'primary' : ''}" disabled={!dirty || saveState === 'saving'} onclick={saveAll}>{saveLabel}</button>
    {/if}
    <button class="icon-btn" aria-label="Close task (Esc)" onclick={close}>✕</button>
  </header>

  {#if loading}
    <p class="msg muted">Loading…</p>
  {:else if error}
    <p class="msg error-text">{error}</p>
  {:else if !task}
    <p class="msg muted">This task was deleted, moved out of your reach, or no longer exists.</p>
  {:else}
    <div class="scroll">
      <textarea class="title" rows="1" bind:value={draftTitle} use:grow={draftTitle} disabled={readonly} aria-label="Title" onblur={commitTitle}
        onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); (e.currentTarget as HTMLTextAreaElement).blur(); } else if (e.key === 'Escape') { draftTitle = task!.title; } }}></textarea>
      {#if project?.archived}<p class="muted note">This project is archived; tasks are read-only.</p>
      {:else if readonly}<p class="muted note">You have view-only access to this project.</p>{/if}

      <dl class="props">
        <dt>Status</dt>
        <dd><PickerCell label="Status" value={task.status} items={statusItems} {readonly} onchange={(v) => patchTask(task!.id, { status: v })} /></dd>
        <dt>Priority</dt>
        <dd><PickerCell label="Priority" value={task.priority} items={priorityItems} {readonly} onchange={(v) => patchTask(task!.id, { priority: v })} /></dd>
        <dt>Project</dt>
        <dd>
          <PickerCell label="Project" value={task.project.id} items={projectItems} {readonly} placeholder="Move to project…" onchange={(v) => patchTask(task!.id, { project: v })}>
            {#snippet children()}<span class="pill"><span class="dot" style="--dot:{task!.project.color}"></span>{task!.project.name}</span>{/snippet}
          </PickerCell>
        </dd>
        <dt>Type</dt>
        <dd><TypeCell always value={task.type} {readonly} onchange={(v) => patchTask(task!.id, { type: v })} /></dd>
        <dt>Due</dt>
        <dd><DueCell always value={task.due_date} {overdue} {readonly} onchange={(v) => patchTask(task!.id, { due_date: v })} /></dd>
        <dt>Sequence</dt>
        <dd><SequenceCell always value={task.sequence} {readonly} onchange={(v) => patchTask(task!.id, { sequence: v })} /></dd>
        <dt>Labels</dt>
        <dd><LabelsCell always labels={task.labels} {readonly} max={8} onchange={(names) => patchTask(task!.id, { labels: names })} /></dd>
      </dl>

      <div class="desc-head">
        <h2>Description</h2>
        <span class="muted state" aria-live="polite">{saveState === 'saving' ? 'Saving…' : saveState === 'saved' ? 'Saved' : saveState === 'dirty' ? 'Unsaved changes' : ''}</span>
      </div>
      {#key task.id}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div onfocusout={saveDesc}>
          <Editor value={draftDesc} {readonly} upload={uploadInline} onerror={(m) => toast(m, 'error')}
            placeholder={readonly ? 'No description' : 'Add a description… paste or drop screenshots'}
            onchange={(md) => { draftDesc = md; descChanged(); }} />
        </div>
      {/key}

      <div class="desc-head">
        <h2>Attachments</h2>
      </div>
      {#if !readonly}
        <input type="file" multiple hidden bind:this={fileInput} onchange={(e) => { upload(e.currentTarget.files); e.currentTarget.value = ''; }} />
        <button class="add-file" disabled={uploading} onclick={() => fileInput?.click()}><span class="muted add">{uploading ? 'Uploading…' : '+ Add attachments'}</span></button>
      {:else if attachments.length === 0}<p class="muted note">No attachments.</p>{/if}
      <ul class="files">
        {#each attachments as a (a.id)}
          <li>
            {#if a.inline}<a class="thumb" href={a.url} target="_blank" rel="noopener"><img src={a.url} alt={a.filename} loading="lazy" /></a>{/if}
            <div class="file-info">
              <a class="fname" href={a.url} target="_blank" rel="noopener" download={a.inline ? undefined : a.filename}>{a.filename}</a>
              <span class="muted size">{size(a.size)}</span>
            </div>
            {#if !readonly}<button class="icon-btn" aria-label="Delete {a.filename}" onclick={() => removeAttachment(a)}>✕</button>{/if}
          </li>
        {/each}
      </ul>

      <p class="muted meta"><span class="created">Created {fmt(task.created_at)}</span><span class="updated">Updated {fmt(task.updated_at)}</span>{#if task.completed_at}<span class="completed">Completed {fmt(task.completed_at)}</span>{/if}</p>

      {#if !readonly}
        <div class="delete-row">
          {#if confirmDelete}
            <span class="muted">Delete this task?</span>
            <button class="btn danger" onclick={remove}>Delete</button>
            <button class="btn" onclick={() => (confirmDelete = false)}>Cancel</button>
          {:else}
            <button class="icon-btn danger" aria-label="Delete task" onclick={() => (confirmDelete = true)}>
              <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                <line x1="10" y1="11" x2="10" y2="17" />
                <line x1="14" y1="11" x2="14" y2="17" />
              </svg>
            </button>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</aside>

<style>
  .panel {
    position: fixed; z-index: 40; top: 0; right: 0; bottom: 0; display: flex; flex-direction: column; width: min(600px, 100vw);
    border-left: 1px solid var(--line-strong); background: var(--bg); box-shadow: var(--shadow);
  }
  @media (prefers-reduced-motion: no-preference) { .panel { animation: slide 0.16s ease-out; } @keyframes slide { from { transform: translateX(24px); opacity: 0; } } }
  header { display: flex; align-items: center; gap: 8px; flex: none; padding: 10px 12px 10px 16px; border-bottom: 1px solid var(--line); }
  .ref { font: 13px ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--muted); }
  .scroll { flex: 1; overflow: auto; padding: 16px; }
  .msg { padding: 24px 16px; }
  .title { width: 100%; padding: 4px 6px; margin: 0 -6px 8px; border: 1px solid transparent; border-radius: 6px; background: transparent; font-size: 20px; font-weight: 650; font-family: inherit; line-height: 1.3; resize: none; overflow: hidden; display: block; }
  .title:hover:not(:disabled) { border-color: var(--line); }
  .title:focus { outline: none; border-color: var(--accent); background: var(--bg); }
  .note { margin: 4px 0 8px; font-size: 13px; }
  .props { display: grid; grid-template-columns: 84px 1fr; align-items: center; gap: 6px 8px; margin: 12px 0 20px; }
  .props dt { color: var(--muted); font-size: 13px; }
  .props dd { margin: 0; min-height: 30px; display: flex; align-items: center; }
  .desc-head { display: flex; align-items: center; gap: 8px; margin: 18px 0 6px; }
  h2 { margin: 0; font-size: 13px; text-transform: uppercase; letter-spacing: 0.04em; color: var(--muted); }
  .state { font-size: 12px; }
  /* a text button like "+ Add label" in the properties above */
  .add-file { display: flex; align-items: center; height: 30px; margin-bottom: 6px; padding: 0; border: 0; background: transparent; text-align: left; }
  .add-file:not(:disabled):hover { background: var(--bg-hover); border-radius: 6px; }
  .add-file .add { font-size: 13px; }
  .files { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
  .files li { display: flex; align-items: center; gap: 8px; }
  .files img { display: block; height: 44px; width: 64px; object-fit: cover; border: 1px solid var(--line); border-radius: 4px; }
  .files .thumb, .files .icon-btn { flex-shrink: 0; }
  .file-info { flex: 1; min-width: 0; display: grid; gap: 2px; }
  .file-info .fname {
    display: -webkit-box;
    -webkit-line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
    word-break: break-word;
    overflow-wrap: anywhere;
    line-height: 1.3;
  }
  .file-info .size { font-size: 12px; }
  .meta { display: grid; gap: 2px; margin-top: 24px; font-size: 12px; }
  .delete-row { display: flex; align-items: center; justify-content: center; gap: 8px; margin-top: 12px; min-height: 32px; }
  .icon-btn.danger:hover { color: var(--danger); background: var(--bg-hover); }
</style>
