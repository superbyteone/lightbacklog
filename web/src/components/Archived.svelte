<script lang="ts">
  import { onMount } from 'svelte';
  import { get, post } from '../lib/api';
  import { link } from '../lib/router.svelte';
  import { errorText, refreshProjects, toast } from '../lib/store.svelte';
  import type { Project } from '../lib/types';
  import DeleteProjectDialog from './DeleteProjectDialog.svelte';

  let projects = $state<Project[]>([]);
  let loading = $state(true);
  let error = $state('');
  let deleting = $state<Project | null>(null);

  async function load() {
    try {
      projects = (await get<Project[]>('/api/v1/projects?archived=archived')).data;
    } catch (e) {
      error = errorText(e);
    } finally {
      loading = false;
    }
  }
  onMount(load);

  async function restore(p: Project) {
    try {
      await post(`/api/v1/projects/${p.id}/unarchive`);
      toast(`Restored ${p.name}`, 'success');
      await Promise.all([load(), refreshProjects()]);
    } catch (e) {
      toast(`Couldn't restore: ${errorText(e)}`, 'error');
    }
  }
</script>

<section class="page">
  <h1>Archived Projects</h1>
  <p class="muted">Archived projects keep their tasks (read-only) and stay out of the sidebar and All Tasks. Restore one to work on it again, or delete it permanently if you are sure.</p>
  {#if loading}<p class="muted">Loading…</p>
  {:else if error}<p class="error-text">{error}</p>
  {:else if projects.length === 0}<p class="muted">No archived projects.</p>
  {:else}
    <ul>
      {#each projects as p (p.id)}
        <li>
          <span class="dot" style="--dot:{p.color}"></span>
          <a href={'/p/' + p.key} use:link>{p.name}</a>
          <span class="muted">{p.key} · {p.total_tasks} tasks</span>
          <span class="spacer"></span>
          {#if p.role === 'owner'}
            <button class="btn" onclick={() => restore(p)}>Restore</button>
            <button class="btn danger" onclick={() => (deleting = p)}>Delete…</button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>

{#if deleting}
  <DeleteProjectDialog project={deleting} onclose={() => (deleting = null)} ondone={() => { deleting = null; void load(); }} />
{/if}

<style>
  .page { max-width: 720px; padding: 20px 24px; }
  h1 { margin: 0 0 4px; font-size: 20px; }
  ul { list-style: none; margin: 16px 0 0; padding: 0; border: 1px solid var(--line); border-radius: var(--radius); }
  li { display: flex; align-items: center; gap: 10px; padding: 10px 14px; }
  li + li { border-top: 1px solid var(--line); }
</style>
