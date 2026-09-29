<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import Archived from './components/Archived.svelte';
  import CommandPalette from './components/CommandPalette.svelte';
  import CreateDialog from './components/CreateDialog.svelte';
  import Login from './components/Login.svelte';
  import ProjectDialog from './components/ProjectDialog.svelte';
  import ProjectSettings from './components/ProjectSettings.svelte';
  import Settings from './components/Settings.svelte';
  import Sidebar from './components/Sidebar.svelte';
  import TaskListView from './components/TaskListView.svelte';
  import TaskPanel from './components/TaskPanel.svelte';
  import Toasts from './components/Toasts.svelte';
  import { loc, navigate } from './lib/router.svelte';
  import { app, bootstrap } from './lib/store.svelte';
  import { openCreate, ui } from './lib/ui.svelte';

  onMount(bootstrap);

  const params = $derived(new URLSearchParams(loc.search));
  const taskRef = $derived(params.get('task'));
  const projectKey = $derived(loc.path.startsWith('/p/') ? decodeURIComponent(loc.path.slice(3).split('/')[0]) : null);
  const page = $derived(loc.path === '/archived' ? 'archived' : loc.path === '/settings' ? 'settings' : projectKey ? 'project' : 'all');

  $effect(() => {
    // Close the mobile menu only when the route changes. (It used to share the effect above, which also
    // depends on the project list, so any background refresh of projects closed an open menu.)
    loc.path;
    untrack(() => (ui.sidebar = false));
  });
  $effect(() => {
    // keep the document title informative
    document.title = taskRef ? `${taskRef} · LightBacklog` : projectKey ? `${projectKey.toUpperCase()} · LightBacklog` : 'LightBacklog';
  });
  $effect(() => {
    // a task opened via URL (not by an in-app click) has no history entry to go back to
    if (!taskRef) ui.panelPushed = false;
  });

  function typing(t: EventTarget | null) {
    return t instanceof HTMLElement && (t.isContentEditable || !!t.closest('input, textarea, select'));
  }

  function keydown(e: KeyboardEvent) {
    if (!app.me) return;
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      ui.palette = !ui.palette;
      return;
    }
    if (e.ctrlKey || e.metaKey || e.altKey || typing(e.target) || ui.create || ui.palette || ui.newProject || ui.projectSettings) return;
    if (e.key === 'c') {
      e.preventDefault();
      openCreate();
    } else if (e.key === '/') {
      const el = document.getElementById('filter-q');
      if (el) {
        e.preventDefault();
        el.focus();
      }
    }
  }
</script>

<svelte:window onkeydown={keydown} />

{#if !app.ready}
  <p class="boot muted">Loading…</p>
{:else if !app.me}
  <Login />
{:else}
  <div class="shell" class:with-panel={!!taskRef}>
    <Sidebar />
    <main>
      <!-- Phones get a slim top bar for the menu button on pages without a task list (the list has its own header, which includes the menu button). -->
      {#if page === 'settings' || page === 'archived'}<div class="mbar"><button class="hamburger icon-btn" aria-label="Open menu" onclick={() => (ui.sidebar = true)}>☰</button><span class="mbrand">LightBacklog</span></div>{/if}
      <div class="content">
        {#if page === 'settings'}<Settings />
        {:else if page === 'archived'}<Archived />
        {:else if page === 'project'}{#key projectKey}<TaskListView projectKey={projectKey ?? undefined} />{/key}
        {:else}<TaskListView />{/if}
      </div>
    </main>
    {#if taskRef}<TaskPanel {taskRef} />{/if}
  </div>
  {#if ui.create}<CreateDialog />{/if}
  {#if ui.palette}<CommandPalette />{/if}
  {#if ui.newProject}<ProjectDialog />{/if}
  {#if ui.projectSettings}{#key ui.projectSettings}<ProjectSettings />{/key}{/if}
{/if}
<Toasts />

<style>
  .boot { padding: 24px; }
  .shell { display: flex; height: 100%; }
  main { position: relative; display: flex; flex-direction: column; flex: 1; min-width: 0; height: 100%; }
  .content { position: relative; flex: 1; min-height: 0; min-width: 0; }
  .mbar { display: none; }
  /* On wide screens the task panel sits beside the list instead of covering its right-hand columns. */
  @media (min-width: 1280px) {
    .with-panel main { margin-right: 600px; }
  }
  @media (max-width: 800px) {
    .mbar { display: flex; align-items: center; gap: 6px; flex: none; height: 48px; padding: 0 6px; border-bottom: 1px solid var(--line); background: var(--bg); }
    .hamburger { width: 40px; height: 40px; font-size: 20px; }
    .mbrand { font-weight: 700; letter-spacing: -0.01em; }
  }
</style>
