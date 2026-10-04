<script lang="ts">
  import { onMount } from 'svelte';
  import { del } from '../lib/api';
  import { errorText, refreshProjects, toast } from '../lib/store.svelte';
  import type { Project } from '../lib/types';

  let { project, ondone, onclose }: { project: Project; ondone: () => void; onclose: () => void } = $props();

  let typed = $state('');
  let busy = $state(false);
  let error = $state('');
  let input = $state<HTMLInputElement>();
  const matches = $derived(typed.trim().toLowerCase() === project.key.toLowerCase());
  onMount(() => input?.focus());

  async function remove(e: Event) {
    e.preventDefault();
    if (!matches || busy) return;
    busy = true;
    error = '';
    try {
      await del(`/api/v1/projects/${project.id}?confirm=${encodeURIComponent(project.key)}`);
      await refreshProjects();
      toast(`Deleted project ${project.name}`, 'success');
      ondone();
    } catch (err) {
      error = errorText(err);
    } finally {
      busy = false;
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="backdrop" onpointerdown={(e) => { if (e.target === e.currentTarget) onclose(); }} onkeydown={(e) => { if (e.key === 'Escape') onclose(); }}>
  <div class="dialog" role="alertdialog" aria-modal="true" aria-label="Delete project permanently">
    <form onsubmit={remove}>
      <header>Delete “{project.name}” permanently? <span class="spacer"></span><button type="button" class="icon-btn" aria-label="Close" onclick={onclose}>✕</button></header>
      <div class="body">
        <p class="warn">
          This deletes the project <strong>{project.name}</strong> ({project.key}) with <strong>{project.total_tasks.toLocaleString()} task{project.total_tasks === 1 ? '' : 's'}</strong>,
          all their attachments and its member list. <strong>It cannot be undone.</strong>
        </p>
        <p class="muted small">
          Backups are the only way back: LightBacklog keeps a daily backup for 14 days, and restoring one replaces everything in the instance, not just this project.
          If you might need this data later, keep the project archived instead.
        </p>
        <label class="field">Type <code>{project.key}</code> to confirm
          <input class="input" bind:this={input} bind:value={typed} autocomplete="off" autocapitalize="characters" spellcheck="false" placeholder={project.key} />
        </label>
        {#if error}<p class="error-text" role="alert">{error}</p>{/if}
      </div>
      <footer>
        <span class="spacer"></span>
        <button type="button" class="btn" onclick={onclose}>Keep the project</button>
        <button class="btn danger solid" disabled={!matches || busy}>{busy ? 'Deleting…' : 'Delete permanently'}</button>
      </footer>
    </form>
  </div>
</div>

<style>
  .warn { margin: 0; padding: 10px 12px; border: 1px solid color-mix(in srgb, var(--danger) 45%, var(--line)); border-radius: 8px; background: color-mix(in srgb, var(--danger) 7%, var(--bg)); }
  .small { margin: 0; font-size: 13px; }
  code { padding: 1px 6px; border-radius: 4px; background: var(--bg-soft); font-weight: 700; }
  .btn.danger.solid { background: var(--danger); border-color: var(--danger); color: #fff; font-weight: 600; }
  .btn.danger.solid:hover:not(:disabled) { filter: brightness(1.08); background: var(--danger); }
  .btn.danger.solid:disabled { background: transparent; color: var(--danger); }
</style>
