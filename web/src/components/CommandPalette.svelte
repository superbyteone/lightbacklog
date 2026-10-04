<script lang="ts">
  import { onMount } from 'svelte';
  import { get } from '../lib/api';
  import { navigate, setParams } from '../lib/router.svelte';
  import { app, isPendingDelete, priorityBySlug, remember, statusBySlug } from '../lib/store.svelte';
  import type { Task } from '../lib/types';
  import { openCreate, ui } from '../lib/ui.svelte';
  import { cycleTheme, theme, themeLabel } from '../lib/theme.svelte';

  interface Row {
    id: string;
    group: 'Actions' | 'Projects' | 'Tasks';
    label: string;
    sub?: string;
    color?: string;
    run: () => void;
  }

  let text = $state('');
  let index = $state(0);
  let input = $state<HTMLInputElement>();
  let taskHits = $state<Task[]>([]);
  let searching = $state(false);
  let seq = 0;
  let timer: ReturnType<typeof setTimeout>;

  onMount(() => input?.focus());

  function close() {
    ui.palette = false;
  }

  $effect(() => {
    const q = text.trim();
    clearTimeout(timer);
    if (q.length < 2) {
      taskHits = [];
      searching = false;
      return;
    }
    searching = true;
    const my = ++seq;
    timer = setTimeout(async () => {
      try {
        const r = await get<Task[]>(`/api/v1/tasks?q=${encodeURIComponent(q)}&sort=relevance&limit=8&include_archived=true`);
        if (my !== seq) return;
        const rows = r.data.filter((t) => !isPendingDelete(t.id));
        remember(rows);
        taskHits = rows;
      } catch {
        if (my === seq) taskHits = [];
      } finally {
        if (my === seq) searching = false;
      }
    }, 140);
  });

  const rows = $derived.by<Row[]>(() => {
    const q = text.trim().toLowerCase();
    const out: Row[] = [];
    const act = (id: string, label: string, run: () => void, sub?: string) => out.push({ id, group: 'Actions', label, sub, run });
    if (q) act('create', `Create task “${text.trim()}”`, () => openCreate({ title: text.trim() }), 'C');
    else act('create', 'Create task', () => openCreate(), 'C');
    const nav: [string, string, () => void][] = [
      ['nav-all', 'Go to All Tasks', () => navigate('/')],
      ['nav-archived', 'Go to Archived Projects', () => navigate('/archived')],
      ['nav-settings', 'Settings & API tokens', () => navigate('/settings')],
      ['new-project', 'Create project', () => (ui.newProject = true)],
      ['theme', `Change colour theme (now ${themeLabel[theme.value]})`, () => cycleTheme()],
    ];
    for (const [id, label, run] of nav) if (!q || label.toLowerCase().includes(q)) act(id, label, run);
    const actions = out.splice(0, out.length);

    const projects = app.projects
      .filter((p) => !q || p.name.toLowerCase().includes(q) || p.key.toLowerCase().includes(q))
      .sort((a, b) => a.name.localeCompare(b.name))
      .slice(0, q ? 8 : 5)
      .map<Row>((p) => ({ id: 'p:' + p.id, group: 'Projects', label: p.name, sub: p.key, color: p.color, run: () => navigate('/p/' + p.key) }));

    const tasks = taskHits.map<Row>((t) => ({
      id: 't:' + t.id, group: 'Tasks', label: t.title, sub: `${t.ref} · ${statusBySlug(t.status)?.name ?? t.status} · ${priorityBySlug(t.priority)?.name ?? t.priority}`,
      color: t.project.color, run: () => setParams((p) => p.set('task', t.ref), { replace: false }),
    }));
    // With a query, matching tasks and projects come before generic actions.
    return q ? [...tasks, ...projects, ...actions] : [...actions.slice(0, 1), ...projects, ...actions.slice(1)];
  });

  $effect(() => {
    rows;
    if (index >= rows.length) index = 0;
  });

  function choose(i: number) {
    const r = rows[i];
    if (!r) return;
    close();
    r.run();
  }

  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      close();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      index = (index + 1) % Math.max(rows.length, 1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      index = (index - 1 + rows.length) % Math.max(rows.length, 1);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      choose(index);
    }
    queueMicrotask(() => document.querySelector('.pal-item.active')?.scrollIntoView({ block: 'nearest' }));
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="backdrop" onpointerdown={(e) => { if (e.target === e.currentTarget) close(); }} onkeydown={key}>
  <div class="dialog pal" role="dialog" aria-modal="true" aria-label="Command palette">
    <input class="pal-input" bind:this={input} bind:value={text} placeholder="Search tasks and projects, or type a new task title…" role="combobox" aria-expanded="true" aria-controls="pal-list" autocomplete="off" />
    <div class="pal-list" id="pal-list" role="listbox">
      {#each rows as r, i (r.id)}
        {#if i === 0 || rows[i - 1].group !== r.group}<div class="pal-group">{r.group}{#if r.group === 'Tasks' && searching}…{/if}</div>{/if}
        <button class="pal-item" class:active={i === index} role="option" aria-selected={i === index} onmouseenter={() => (index = i)} onclick={() => choose(i)}>
          {#if r.color}<span class="dot" style="--dot:{r.color}"></span>{/if}
          <span class="pal-label">{r.label}</span>
          {#if r.sub}<span class="muted pal-sub">{r.sub}</span>{/if}
        </button>
      {/each}
      {#if text.trim().length >= 2 && !searching && taskHits.length === 0}<div class="muted pal-none">No matching tasks</div>{/if}
    </div>
    <div class="pal-foot muted"><kbd>↑</kbd><kbd>↓</kbd> navigate <kbd>Enter</kbd> select <kbd>Esc</kbd> close</div>
  </div>
</div>

<style>
  .pal { width: min(620px, 100%); overflow: hidden; }
  .pal-input { width: 100%; height: 52px; padding: 0 18px; border: 0; border-bottom: 1px solid var(--line); background: transparent; font-size: 16px; outline: none; }
  .pal-list { max-height: min(420px, 55vh); overflow: auto; padding: 6px; }
  .pal-group { padding: 8px 10px 4px; font-size: 11px; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase; color: var(--muted); }
  .pal-item { display: flex; align-items: center; gap: 10px; width: 100%; min-height: 36px; padding: 6px 10px; border: 0; border-radius: 6px; background: transparent; text-align: left; }
  .pal-item.active { background: var(--accent-soft); }
  .pal-label { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pal-sub { flex: none; font-size: 12px; }
  .pal-none { padding: 10px; font-size: 13px; }
  .pal-foot { display: flex; gap: 6px; align-items: center; padding: 8px 14px; border-top: 1px solid var(--line); font-size: 12px; }
</style>
