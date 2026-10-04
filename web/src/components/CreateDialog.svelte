<script lang="ts">
  import { onMount } from 'svelte';
  import { todayISO } from '../lib/filters';
  import { parseQuickAdd } from '../lib/quickadd';
  import { loc, navigate, setParams } from '../lib/router.svelte';
  import { api } from '../lib/api';
  import { app, createTask, errorText, toast } from '../lib/store.svelte';
  import type { Attachment } from '../lib/types';
  import { closeCreate, ui } from '../lib/ui.svelte';
  import DueCell from './DueCell.svelte';
  import Editor from './Editor.svelte';
  import LabelsCell from './LabelsCell.svelte';
  import PickerCell from './PickerCell.svelte';
  import TypeCell from './TypeCell.svelte';

  /** How much the visual viewport must shrink before it counts as an on-screen keyboard rather than the address bar. */
  const KEYBOARD_MIN_PX = 140;
  const TOP_GAP = 0.12; // the desktop dialog's margin above it, as a share of the screen height (see the style block)
  const BOTTOM_GAP_PX = 16;

  const opts = $derived(ui.create ?? {});
  const writable = $derived(app.projects.filter((p) => !p.archived && p.role !== 'viewer'));

  let title = $state('');
  let description = $state('');
  let editorKey = $state(0); // bumped to give "Create & add another" a fresh, empty editor
  let projectId = $state(''); // pre-selected only when the dialog is opened from a project page (see onMount)
  let status = $state('todo');
  let priority = $state('low'); // optional, but pre-filled to match the server's own default so the field never looks unset
  let projectOpen = $state(false);
  let taskType = $state<string | null>(null); // optional; never pre-filled
  let missing = $state<{ title?: boolean; project?: boolean }>({});
  let labels = $state<string[]>([]);
  let due = $state<string | null>(null);
  let busy = $state(false);
  let error = $state('');
  let titleInput = $state<HTMLInputElement>();
  let dlg = $state<HTMLDivElement>();
  let backdrop = $state<HTMLDivElement>();
  let tall = $state(false); // desktop: the dialog is taller than the screen would leave room for, so it sits flush at the top (LB-77)

  onMount(() => {
    title = opts.title ?? '';
    // Opened from inside a project (/p/KEY): that is very likely the project the task is for, so pre-select it when the person can add to it.
    // Elsewhere nothing is pre-filled: the person chooses. Priority already starts at its own default (Low, see above) regardless of where the dialog was opened.
    const key = loc.path.startsWith('/p/') ? decodeURIComponent(loc.path.slice(3).split('/')[0]).toLowerCase() : '';
    projectId = writable.find((p) => p.key.toLowerCase() === key)?.id ?? '';
    titleInput?.focus();

    // The gap above the dialog only makes sense while the dialog fits. Once it needs scrolling, drop the gap so the header is
    // pinned at the top at every scroll position, including the very top. Measured without the gap, so it cannot flip-flop.
    const measure = () => {
      if (!dlg) return;
      tall = dlg.offsetHeight + window.innerHeight * TOP_GAP + BOTTOM_GAP_PX > window.innerHeight;
    };
    const ro = new ResizeObserver(measure);
    if (dlg) ro.observe(dlg);
    window.addEventListener('resize', measure);
    measure();
    const stopTall = () => { ro.disconnect(); window.removeEventListener('resize', measure); };

    // With the on-screen keyboard open, a phone shrinks and pans the *visual* viewport inside the layout viewport, which
    // carries a fixed full-screen dialog (and its header) off the visible top. Follow the visual viewport then (LB-71).
    // Only then: the address bar sliding in and out while scrolling also nudges offsetTop by a few pixels, and following
    // that lets the page behind show through a crack above the header (LB-76).
    const vv = window.visualViewport;
    if (!vv) return stopTall;
    const follow = () => {
      const keyboard = window.innerHeight - vv.height > KEYBOARD_MIN_PX;
      backdrop?.style.setProperty('--vv-top', (keyboard ? vv.offsetTop : 0) + 'px');
      backdrop?.style.setProperty('--vv-h', (keyboard ? vv.height : window.innerHeight) + 'px');
    };
    follow();
    vv.addEventListener('resize', follow);
    vv.addEventListener('scroll', follow);
    return () => { stopTall(); vv.removeEventListener('resize', follow); vv.removeEventListener('scroll', follow); };
  });

  const quick = $derived(parseQuickAdd(title, { projects: app.projects, priorities: app.priorities, statuses: app.statuses, labels: app.labels }));
  const effProject = $derived(quick.project?.id ?? projectId);
  const effStatus = $derived(quick.status ?? status);
  const effPriority = $derived(quick.priority ?? priority);
  const effLabels = $derived([...new Set([...labels, ...quick.labels])]);
  const project = $derived(writable.find((p) => p.id === effProject));

  const statusItems = $derived(app.statuses.map((s) => ({ value: s.slug, label: s.name, color: s.color })));
  const priorityItems = $derived(app.priorities.map((p) => ({ value: p.slug, label: p.name, color: p.color })));
  const projectItems = $derived(writable.map((p) => ({ value: p.id, label: p.name, color: p.color, hint: p.key })));
  const labelObjs = $derived(effLabels.map((n) => app.labels.find((l) => l.name.toLowerCase() === n.toLowerCase()) ?? { id: 'new:' + n, name: n, color: '#6b7280' }));

  /** Screenshots pasted before the task exists are uploaded to the chosen project and linked on save. */
  async function uploadInline(file: File) {
    if (!project) throw new Error('choose a project first');
    const fd = new FormData();
    fd.append('project', project.id);
    fd.append('file', file, file.name || 'image.png');
    const r = await api<Attachment>('POST', '/api/v1/uploads', fd);
    return { url: r.data.url, inline: r.data.inline, filename: r.data.filename };
  }

  async function submit(another: boolean) {
    const t = (quick.title || title).trim();
    // Title and project are required; the project is never guessed (only pre-selected on a project page): point at whatever is still missing.
    missing = { title: !t, project: !project };
    if (missing.title || missing.project) {
      error = '';
      if (missing.title) titleInput?.focus();
      else projectOpen = true;
      return;
    }
    const chosen = project!; // validated above
    busy = true;
    error = '';
    try {
      const created = await createTask({ project: chosen.id, title: t, description: description || undefined, status: effStatus, priority: effPriority || undefined, type: taskType, due_date: due, labels: effLabels });
      toast(`Created ${created.ref}`, 'success', { label: 'Open', run: () => setParams((p) => p.set('task', created.ref), { replace: false }) });
      if (another) {
        title = '';
        description = '';
        editorKey++;
        labels = [];
        due = null;
        taskType = null;
        // keep what was just chosen so a batch of similar tasks is quick: this is the person's own choice, not a default
        projectId = chosen.id;
        status = effStatus;
        priority = effPriority;
        missing = {};
        titleInput?.focus();
      } else closeCreate();
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }

  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      if (e.defaultPrevented) return;
      e.stopPropagation();
      closeCreate();
    } else if (e.key === 'Enter' && !e.ctrlKey && !e.metaKey && (e.target as HTMLElement) === titleInput) {
      // Enter in the title field creates the task; Enter on a button or in a picker keeps its own meaning.
      e.preventDefault();
      void submit(false);
    } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      void submit(true);
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="backdrop create" bind:this={backdrop} onpointerdown={(e) => { if (e.target === e.currentTarget) closeCreate(); }} onkeydown={key}>
  <div class="dialog create" class:tall role="dialog" aria-modal="true" aria-label="Create task" bind:this={dlg}>
    <header>New task <span class="spacer"></span>
      <button class="btn" onclick={closeCreate}>Cancel</button>
      <button class="btn primary" disabled={busy || writable.length === 0} onclick={() => submit(false)}>{busy ? 'Creating…' : 'Create task'}</button>
    </header>
    <div class="body">
      {#if writable.length === 0}
        <p>You need a project first. <button class="btn" onclick={() => { closeCreate(); ui.newProject = true; }}>Create a project</button></p>
      {:else}
        <input class="input big" class:bad={missing.title} oninput={() => (missing.title = false)} bind:this={titleInput} bind:value={title} placeholder="What needs to be done?" aria-label="Title" autocomplete="off" />
        {#if quick.matched.length}
          <div class="muted hint">Applying: {#each quick.matched as m}<span class="chip" style="--chip:var(--accent)">{m.token} → {m.text}</span> {/each}</div>
        {:else}
          <div class="muted hint shortcuts">Shortcuts: <code>#project</code> <code>!high</code> <code>/blocked</code> <code>@label</code></div>
        {/if}
        <div class="fields">
          <div class="f"><span class="lbl">Project <b class="req" aria-hidden="true">*</b></span>
            <PickerCell bind:open={projectOpen} label="Project" value={effProject} items={projectItems} empty="Choose a project" placeholder="Find a project…" invalid={!!missing.project && !project}
              onchange={(v) => { projectId = v; missing.project = false; }}>
              {#snippet children(cur)}<span class="pill wide" class:unset={!cur} class:invalid={!!missing.project && !cur}>{#if cur?.color}<span class="dot" style="--dot:{cur.color}"></span>{/if}{cur?.label ?? 'Choose a project'}{#if cur?.hint}<span class="muted">{cur.hint}</span>{/if}</span>{/snippet}
            </PickerCell>
          </div>
          <div class="f"><span class="lbl">Status</span><PickerCell label="Status" value={effStatus} items={statusItems} onchange={(v) => (status = v)} /></div>
          <div class="f"><span class="lbl">Priority</span><PickerCell label="Priority" value={effPriority} items={priorityItems} empty="Choose a priority" onchange={(v) => (priority = v)} /></div>
          <div class="f"><span class="lbl">Due</span><DueCell always pill value={due} overdue={!!due && due < todayISO()} onchange={(v) => (due = v)} /></div>
          <div class="f"><span class="lbl">Type</span><TypeCell always pill value={taskType} onchange={(v) => (taskType = v)} /></div>
          <div class="f wide"><span class="lbl">Labels</span><LabelsCell always labels={labelObjs} max={6} onchange={(names) => (labels = names.filter((n) => !quick.labels.some((q) => q.toLowerCase() === n.toLowerCase())))} /></div>
          <div class="f wide"><span class="lbl">Description</span>
            {#key editorKey}<Editor value={description} onchange={(md) => (description = md)} minHeight={110} upload={uploadInline} onerror={(m) => toast(m, 'error')} placeholder="Description… paste or drop screenshots" />{/key}
          </div>
        </div>
        {#if missing.title || missing.project}
          <p class="error-text" role="alert">Please {[missing.title && 'enter a title', missing.project && 'choose a project'].filter(Boolean).join(', ').replace(/, ([^,]*)$/, ' and $1')}.</p>
        {/if}
        {#if error}<p class="error-text" role="alert">{error}</p>{/if}
      {/if}
    </div>
    <footer>
      <span class="muted keys"><kbd>Enter</kbd> create · <kbd>Ctrl</kbd>+<kbd>Enter</kbd> create &amp; add another</span>
    </footer>
  </div>
</div>

<style>
  .big { height: 42px; font-size: 16px; }
  .hint { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; font-size: 12px; margin-top: -6px; }
  .hint code { padding: 0 4px; border-radius: 4px; background: var(--bg-soft); }
  .fields { display: grid; grid-template-columns: 1fr 1fr; gap: 10px 14px; }
  .f { display: grid; gap: 4px; min-width: 0; }
  .f.wide { grid-column: 1 / -1; }
  .lbl { font-size: 12px; font-weight: 600; color: var(--muted); }
  .f :global(.cell-btn) { height: 30px; }
  .pill.wide { height: 28px; }
  .req { color: var(--danger); }
  .big.bad { border-color: var(--danger); }
  .keys { font-size: 12px; }
  /* Cancel and Create task live in the header, which stays pinned to the top of the viewport when a long form makes the dialog scroll. */
  .dialog.create header { position: sticky; top: 0; z-index: 2; background: var(--bg); border-radius: 12px 12px 0 0; }
  /* The scroll container is the backdrop, and a sticky header sticks inside the container's padding: so the gap above the
     dialog is the dialog's own margin (it scrolls away), leaving the header free to pin at the very top of the screen. */
  .backdrop.create { padding-top: 0; }
  .dialog.create { margin-top: 12vh; }
  /* taller than the screen: no gap above it, and square top corners so the pinned header meets the edge cleanly */
  .dialog.create.tall { margin-top: 0; border-top-left-radius: 0; border-top-right-radius: 0; }
  .dialog.create.tall header { border-radius: 0; }
  /* the static #project / !high hint is a keyboard aid: the same rule as the other shortcut hints (phones, tablets, touch-only devices) */
  @media (max-width: 1100px), (hover: none) and (pointer: coarse) { .hint.shortcuts { display: none; } }
  /* Phones and tablets (the compact layout): the dialog is a full screen card like an opened task, with the footer pinned and only the body scrolling. */
  @media (max-width: 1100px) {
    .backdrop.create { display: block; padding: 0; overflow: hidden; background: var(--bg); inset: var(--vv-top, 0px) 0 auto 0; height: var(--vv-h, 100%); overscroll-behavior: none; }
    .dialog.create { display: flex; flex-direction: column; margin-top: 0; width: 100%; height: 100%; border: 0; border-radius: 0; box-shadow: none; }
    .dialog.create header { flex: none; }
    .dialog.create header { border-radius: 0; }
    .dialog.create .body { flex: 1; min-height: 0; overflow: auto; align-content: start; overscroll-behavior: contain; }
    /* only the keyboard-shortcut hint is left in the footer, which a touch screen has no use for */
    .dialog.create footer { display: none; }
  }
</style>
