<script lang="ts">
  import { link, loc, navigate } from '../lib/router.svelte';
  import { app, logout } from '../lib/store.svelte';
  import type { Project } from '../lib/types';
  import { openCreate, setSidebarCollapsed, ui } from '../lib/ui.svelte';
  import ThemeSwitch from './ThemeSwitch.svelte';

  let filter = $state('');
  let filterEl = $state<HTMLInputElement>();

  const active = $derived(app.projects.filter((p) => !p.archived));
  const q = $derived(filter.trim().toLowerCase());
  const shown = $derived(active.filter((p) => !q || p.name.toLowerCase().includes(q) || p.key.toLowerCase().includes(q)));
  const favorites = $derived(shown.filter((p) => p.favorite));
  const rest = $derived(shown.filter((p) => !p.favorite));
  const currentKey = $derived(loc.path.startsWith('/p/') ? decodeURIComponent(loc.path.slice(3).split('/')[0]).toLowerCase() : '');
  const onAll = $derived(loc.path === '/' || loc.path === '/tasks');

  function firstMatch(e: KeyboardEvent) {
    if (e.key === 'Enter' && shown[0]) {
      navigate('/p/' + shown[0].key);
      filter = '';
      ui.sidebar = false;
    } else if (e.key === 'Escape') {
      filter = '';
      filterEl?.blur();
    }
  }
</script>

<nav class:open={ui.sidebar} class:collapsed={ui.sidebarCollapsed} aria-label="Main">
  <!-- Folded down (wide screens only): just the logo and an arrow that brings the sidebar back. -->
  <div class="mini">
    <a href="/" use:link class="mark" aria-label="LightBacklog: all tasks"><img src="/favicon.svg" width="28" height="28" alt="" /></a>
    <button class="icon-btn" aria-label="Expand sidebar" title="Expand sidebar" aria-expanded="false" onclick={() => setSidebarCollapsed(false)}>
      <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 5 7 7-7 7" /></svg></button>
  </div>
  <div class="full">
  <div class="brand"><a href="/" use:link class="logo">LightBacklog</a>
    <button class="icon-btn fold" aria-label="Collapse sidebar" title="Collapse sidebar" aria-expanded="true" onclick={() => setSidebarCollapsed(true)}>
      <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m15 5-7 7 7 7" /></svg></button></div>
  <button class="btn primary new" onclick={() => openCreate()}>+ New task <kbd>C</kbd></button>

  <a href="/" use:link class="item" class:current={onAll} aria-current={onAll ? 'page' : undefined}>All Tasks</a>

  <div class="section">
    <span>Projects</span>
    <button class="icon-btn" title="New project" aria-label="New project" onclick={() => (ui.newProject = true)}>＋</button>
  </div>
  <input class="input filter" placeholder="Find project…" bind:value={filter} bind:this={filterEl} onkeydown={firstMatch} aria-label="Filter projects" />

  {#snippet row(p: Project)}
    <a href={'/p/' + p.key} use:link class="item proj" class:current={currentKey === p.key.toLowerCase()} aria-current={currentKey === p.key.toLowerCase() ? 'page' : undefined}>
      <span class="dot" style="--dot:{p.color}"></span><span class="name">{p.name}</span>
      {#if p.open_tasks}<span class="count">{p.open_tasks}</span>{/if}
      {#if p.favorite}<svg class="star" viewBox="0 0 24 24" width="14" height="14" fill="currentColor" aria-hidden="true"><path d="M12 2.5l2.9 5.88 6.49.94-4.7 4.58 1.11 6.47L12 17.27l-5.8 3.1 1.11-6.47-4.7-4.58 6.49-.94z" /></svg>{/if}
    </a>
  {/snippet}

  <div class="list">
    {#if favorites.length > 0}
      <div class="divider"><span>Favorites</span><span class="rule"></span></div>
      {#each favorites as p (p.id)}{@render row(p)}{/each}
      <div class="divider"><span>A&ndash;Z</span><span class="rule"></span></div>
    {/if}
    {#each rest as p (p.id)}{@render row(p)}{/each}
    {#if shown.length === 0}
      <p class="muted none">{active.length === 0 ? 'No projects yet.' : 'No matching projects.'}</p>
    {/if}
  </div>

  <div class="foot">
    <a href="/archived" use:link class="item" class:current={loc.path === '/archived'}>Archived Projects</a>
    <a href="/settings" use:link class="item" class:current={loc.path === '/settings'}>Settings</a>
    <button class="item palette" onclick={() => (ui.palette = true)}>Search <kbd>{navigator.platform.includes('Mac') ? '⌘' : 'Ctrl'}</kbd><kbd>K</kbd></button>
    <div class="theme"><ThemeSwitch /></div>
    <div class="user"><span class="who" title={app.me?.username}>{app.me?.username}</span><button class="btn ghost" onclick={logout}>Sign out</button></div>
  </div>
  </div>
</nav>
{#if ui.sidebar}<button class="scrim" aria-label="Close menu" onclick={() => (ui.sidebar = false)}></button>{/if}

<style>
  nav { display: flex; flex-direction: column; gap: 4px; width: 250px; flex: none; height: 100%; padding: 12px 10px; border-right: 1px solid var(--line); background: var(--bg-soft); overflow: hidden; }
  .full { display: flex; flex-direction: column; gap: 4px; flex: 1; min-height: 0; }
  .mini { display: none; }
  .brand { display: flex; align-items: center; justify-content: space-between; padding: 2px 4px 8px 8px; }
  .fold { width: 28px; height: 28px; }
  @media (prefers-reduced-motion: no-preference) { nav { transition: width 0.15s ease-out; } }
  /* Folded: a white strip a fifth of the sidebar's width (permanent-sidebar layouts only; the phone drawer is never folded). */
  @media (min-width: 801px) {
    nav.collapsed { width: 50px; padding: 12px 0; align-items: center; background: var(--bg); }
    nav.collapsed .full { display: none; }
    nav.collapsed .mini { display: flex; flex-direction: column; align-items: center; gap: 10px; }
    .mark { display: block; line-height: 0; }
    .mark img { border-radius: 6px; }
  }
  .logo { font-weight: 750; font-size: 16px; color: var(--fg); letter-spacing: -0.01em; }
  .logo:hover { text-decoration: none; }
  .new { justify-content: center; margin-bottom: 6px; }
  .new kbd { background: transparent; border-color: color-mix(in srgb, var(--accent-fg) 40%, transparent); color: var(--accent-fg); }
  .item { display: flex; align-items: center; gap: 8px; min-height: 30px; padding: 4px 8px; border: 0; border-radius: 6px; background: transparent; color: var(--fg); text-align: left; width: 100%; }
  .item:hover { background: var(--bg-hover); text-decoration: none; }
  .item.current { background: var(--accent-soft); font-weight: 600; }
  .section { display: flex; align-items: center; justify-content: space-between; padding: 12px 8px 2px; font-size: 11px; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase; color: var(--muted); }
  .divider { display: flex; align-items: center; gap: 8px; padding: 10px 8px 4px; font-size: 11px; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase; color: var(--muted); }
  .divider .rule { flex: 1; height: 1px; background: var(--line); }
  .star { flex: none; color: #ca8a04; }
  .filter { height: 30px; margin: 4px 0; }
  .list { flex: 1; min-height: 0; overflow: auto; }
  .name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .count { font-size: 12px; color: var(--muted); }
  .none { padding: 8px; margin: 0; font-size: 13px; }
  .foot { display: grid; gap: 2px; padding-top: 8px; border-top: 1px solid var(--line); }
  .palette kbd { margin-left: auto; }
  .palette kbd + kbd { margin-left: 0; }
  .theme { padding: 6px 2px 2px; }
  .user { display: flex; align-items: center; justify-content: space-between; padding: 6px 4px 0 8px; }
  .who { font-weight: 600; overflow: hidden; text-overflow: ellipsis; }
  .scrim { display: none; }
  @media (max-width: 800px) {
    .fold { display: none; }
    nav { position: fixed; z-index: 45; top: 0; bottom: 0; left: 0; transform: translateX(-100%); transition: transform 0.15s; box-shadow: var(--shadow); }
    nav.open { transform: none; }
    .scrim { display: block; position: fixed; z-index: 44; inset: 0; border: 0; background: rgba(10, 12, 18, 0.4); }
  }
</style>
