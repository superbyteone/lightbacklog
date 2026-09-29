<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import type { Filters } from '../lib/filters';
  import { app } from '../lib/store.svelte';
  import ColumnsMenu from './ColumnsMenu.svelte';
  import SearchPicker from './SearchPicker.svelte';

  type Draft = Pick<Filters, 'status' | 'priority' | 'type' | 'label' | 'project' | 'done'>;
  let { filters, lockedProject = false, hideStatus = false, showDone = true, onapply, onclose }: {
    filters: Filters;
    lockedProject?: boolean;
    hideStatus?: boolean;
    showDone?: boolean;
    onapply: (next: Draft) => void;
    onclose: () => void;
  } = $props();

  // The sheet edits a draft; nothing changes in the list until "Apply filters".
  const norm = (list: string[], all: { value: string }[]) => list.map((v) => all.find((a) => a.value.toLowerCase() === v.toLowerCase())?.value ?? v);
  const statusItems = $derived(app.statuses.map((s) => ({ value: s.slug, label: s.name })));
  const priorityItems = $derived(app.priorities.map((s) => ({ value: s.slug, label: s.name })));
  const typeItems = $derived(app.types.map((s) => ({ value: s.slug, label: s.name })));
  const labelItems = $derived([...app.labels].sort((a, b) => a.name.localeCompare(b.name)).map((l) => ({ value: l.name, label: l.name, color: undefined as string | undefined })));
  const projectItems = $derived(app.projects.filter((p) => !p.archived).sort((a, b) => a.name.localeCompare(b.name)).map((p) => ({ value: p.key, label: p.name, color: p.color as string | undefined, hint: p.key })));

  let draft = $state<Draft>(untrack(() => ({
    status: norm(filters.status, statusItems), priority: norm(filters.priority, priorityItems), type: norm(filters.type, typeItems),
    label: [...filters.label], project: norm(filters.project, projectItems), done: filters.done,
  })));
  let fieldsOpen = $state(false);

  const active = $derived(draft.status.length + draft.priority.length + draft.type.length + draft.label.length + (lockedProject ? 0 : draft.project.length));
  const has = (list: string[], v: string) => list.some((x) => x.toLowerCase() === v.toLowerCase());
  function toggle(key: 'status' | 'priority' | 'type' | 'label' | 'project', v: string) {
    draft[key] = has(draft[key], v) ? draft[key].filter((x) => x.toLowerCase() !== v.toLowerCase()) : [...draft[key], v];
  }
  function reset() {
    draft = { status: [], priority: [], type: [], label: [], project: lockedProject ? draft.project : [], done: false };
  }

  // --- closing: escape, back, apply and the scrim all go through the browser history, so the
  // Android back gesture closes the sheet instead of leaving the page ---
  let sheet = $state<HTMLDivElement>();
  let pending: Draft | null = null;
  let done = false;
  let pushed = false;
  function finish() {
    if (done) return;
    done = true;
    if (pending) onapply(pending);
    onclose();
  }
  function request(apply: boolean) {
    if (done) return;
    pending = apply ? $state.snapshot(draft) : null;
    if (pushed && history.state?.lbSheet) {
      history.back();
      setTimeout(finish, 400); // popstate normally does it; this is only a safety net
    } else finish();
  }
  const opener = document.activeElement as HTMLElement | null;
  onMount(() => {
    history.pushState({ ...(history.state ?? {}), lbSheet: 1 }, '', location.href);
    pushed = true;
    const pop = () => finish();
    window.addEventListener('popstate', pop);
    const off = follow();
    queueMicrotask(() => sheet?.focus());
    return () => {
      window.removeEventListener('popstate', pop);
      off();
      opener?.focus?.();
    };
  });

  // Sit on top of the on-screen keyboard instead of under it.
  function follow() {
    const vv = window.visualViewport;
    if (!vv) return () => {};
    const set = () => {
      sheet?.style.setProperty('--vv-bottom', Math.max(0, window.innerHeight - vv.offsetTop - vv.height) + 'px');
      sheet?.style.setProperty('--vv-h', vv.height + 'px');
    };
    set();
    vv.addEventListener('resize', set);
    vv.addEventListener('scroll', set);
    return () => { vv.removeEventListener('resize', set); vv.removeEventListener('scroll', set); };
  }

  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); request(false); return; }
    if (e.key !== 'Tab' || !sheet) return;
    const f = [...sheet.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])')].filter((el) => el.offsetParent !== null);
    if (!f.length) return;
    const first = f[0], last = f[f.length - 1];
    if (e.shiftKey && (document.activeElement === first || document.activeElement === sheet)) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }
</script>

<svelte:window onkeydown={key} />

<div class="scrim" role="presentation" onpointerdown={(e) => e.target === e.currentTarget && request(false)}>
  <div class="sheet" role="dialog" aria-modal="true" aria-labelledby="fs-title" tabindex="-1" bind:this={sheet}>
    <div class="handle" aria-hidden="true"></div>
    <header>
      <h2 id="fs-title">Filters</h2>
      {#if active > 0}<span class="active">{active} active</span>{/if}
      <span class="spacer"></span>
      <button class="reset" onclick={reset} disabled={active === 0 && !draft.done}>Reset</button>
    </header>
    <div class="body">
      {#if !hideStatus}
        <section aria-labelledby="fs-status"><h3 id="fs-status">Status</h3>
          <div class="chips" role="group" aria-label="Status">
            {#each statusItems as s (s.value)}{@render chip('status', s)}{/each}
          </div></section>
      {/if}
      <section aria-labelledby="fs-priority"><h3 id="fs-priority">Priority</h3>
        <div class="chips" role="group" aria-label="Priority">
          {#each priorityItems as s (s.value)}{@render chip('priority', s)}{/each}
        </div></section>
      <section aria-labelledby="fs-type"><h3 id="fs-type">Type</h3>
        <div class="chips" role="group" aria-label="Type">
          {#each typeItems as s (s.value)}{@render chip('type', s)}{/each}
        </div></section>
      <section aria-labelledby="fs-label"><h3 id="fs-label">Label</h3>
        <SearchPicker label="Label" placeholder="Search labels" items={labelItems} selected={draft.label} ontoggle={(v) => toggle('label', v)} /></section>
      {#if !lockedProject}
        <section aria-labelledby="fs-project"><h3 id="fs-project">Project</h3>
          <SearchPicker label="Project" placeholder="Search projects" items={projectItems} selected={draft.project} ontoggle={(v) => toggle('project', v)} /></section>
      {/if}
      {#if showDone && !hideStatus}
        <label class="row switch"><span>Show done tasks</span>
          <input type="checkbox" role="switch" bind:checked={draft.done} /><span class="track" aria-hidden="true"></span></label>
      {/if}
      <button class="row" aria-expanded={fieldsOpen} onclick={() => (fieldsOpen = !fieldsOpen)}>
        <span>Visible fields</span><span class="muted go">Customize <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class:open={fieldsOpen}><path d="m9 6 6 6-6 6" /></svg></span></button>
      {#if fieldsOpen}<div class="fields"><ColumnsMenu cards {lockedProject} /></div>{/if}
    </div>
    <footer><button class="apply" onclick={() => request(true)}>Apply filters</button></footer>
  </div>
</div>

{#snippet chip(key: 'status' | 'priority' | 'type', s: { value: string; label: string })}
  {@const on = has(draft[key], s.value)}
  <button class="fchip" class:on aria-pressed={on} onclick={() => toggle(key, s.value)}>
    {#if on}<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 12.5 4.5 4.5L19 7.5" /></svg>{/if}{s.label}
  </button>
{/snippet}

<style>
  /* Sizes follow the sidebar: 14px text, 32px chips and controls, 8px gaps; small controls get a 44px touch area from ::after. */
  .scrim { position: fixed; inset: 0; z-index: 80; display: flex; align-items: flex-end; justify-content: center; background: rgba(15, 17, 22, 0.5); }
  .sheet { -webkit-tap-highlight-color: transparent; outline: none; position: relative; display: flex; flex-direction: column; width: min(100%, 560px); max-height: calc(var(--vv-h, 100dvh) - 24px); margin-bottom: var(--vv-bottom, 0px); border-radius: 16px 16px 0 0; background: var(--m-card); box-shadow: var(--shadow); overflow: hidden; }
  .handle { flex: none; width: 36px; height: 4px; margin: 8px auto 0; border-radius: 2px; background: var(--m-line-strong); }
  header { display: flex; align-items: center; gap: 8px; flex: none; padding: 4px 16px 0; }
  h2 { margin: 0; font-size: 16px; font-weight: 700; }
  .active { padding: 0 8px; height: 22px; display: inline-flex; align-items: center; border-radius: 11px; background: var(--m-accent-soft); color: var(--m-accent); font-size: 12px; font-weight: 600; }
  .reset { position: relative; min-width: 44px; height: 32px; border: 0; background: transparent; color: var(--m-accent); font-size: 14px; font-weight: 600; }
  .reset::after, .fchip::after { content: ''; position: absolute; inset: -6px; }
  .reset:disabled { color: var(--muted); }
  .body { flex: 1; min-height: 0; overflow-y: auto; padding: 0 16px 8px; overscroll-behavior: contain; }
  section { padding: 2px 0 6px; }
  h3 { margin: 8px 0 6px; color: var(--muted); font-size: 11px; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase; }
  .chips { display: flex; flex-wrap: wrap; gap: 8px; }
  .fchip { position: relative; display: inline-flex; align-items: center; gap: 4px; height: 32px; padding: 0 12px; border: 1px solid var(--m-line-strong); border-radius: 16px; background: var(--m-card); font-size: 14px; }
  .fchip.on { border-color: var(--m-accent); background: var(--m-accent-soft); color: var(--m-accent); font-weight: 500; padding: 0 12px 0 8px; }
  .row { display: flex; align-items: center; justify-content: space-between; width: 100%; min-height: 44px; padding: 0; border: 0; border-top: 1px solid var(--m-line); background: transparent; font-size: 14px; text-align: left; }
  .go { display: inline-flex; align-items: center; gap: 2px; font-size: 14px; }
  .go svg { transition: transform 0.15s; }
  .go svg.open { transform: rotate(90deg); }
  .switch { position: relative; cursor: pointer; }
  .switch input { position: absolute; inset: 0; width: 100%; height: 100%; margin: 0; opacity: 0; cursor: pointer; }
  .track { position: relative; flex: none; width: 38px; height: 22px; border-radius: 11px; background: var(--m-line-strong); transition: background 0.15s; }
  .track::after { content: ''; position: absolute; top: 2px; left: 2px; width: 18px; height: 18px; border-radius: 50%; background: #fff; box-shadow: 0 1px 2px rgba(0, 0, 0, 0.3); transition: transform 0.15s; }
  .switch input:checked + .track { background: var(--m-accent); }
  .switch input:checked + .track::after { transform: translateX(16px); }
  .switch input:focus-visible + .track { outline: 2px solid var(--m-accent); outline-offset: 2px; }
  .fields { padding: 0 0 8px; border-top: 1px solid var(--m-line); }
  footer { flex: none; padding: 8px 16px calc(10px + env(safe-area-inset-bottom)); border-top: 1px solid var(--m-line); background: var(--m-card); }
  .apply { width: 100%; height: 40px; border: 0; border-radius: 8px; background: var(--m-accent); color: var(--accent-fg); font-size: 14px; font-weight: 600; }
  @media (prefers-reduced-motion: no-preference) {
    .sheet { animation: rise 0.18s ease-out; }
    .scrim { animation: fade 0.18s ease-out; }
    @keyframes rise { from { transform: translateY(40px); opacity: 0.6; } }
    @keyframes fade { from { opacity: 0; } }
  }
  @media (min-width: 700px) { .sheet { border-radius: 16px; margin-bottom: max(var(--vv-bottom, 0px), 24px); } }
</style>
