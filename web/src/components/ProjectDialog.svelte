<script lang="ts">
  import { onMount } from 'svelte';
  import { post } from '../lib/api';
  import { navigate } from '../lib/router.svelte';
  import { errorText, refreshProjects, toast } from '../lib/store.svelte';
  import type { Project } from '../lib/types';
  import { ui } from '../lib/ui.svelte';

  let name = $state('');
  let key = $state('');
  let description = $state('');
  let error = $state('');
  let busy = $state(false);
  let nameEl = $state<HTMLInputElement>();
  onMount(() => nameEl?.focus());

  function close() {
    ui.newProject = false;
  }

  async function submit(e: Event) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      const body: Record<string, string> = { name: name.trim(), description };
      if (key.trim()) body.key = key.trim();
      const r = await post<Project>('/api/v1/projects', body);
      await refreshProjects();
      toast(`Created project ${r.data.name}`, 'success');
      close();
      navigate('/p/' + r.data.key);
    } catch (err) {
      error = errorText(err);
    } finally {
      busy = false;
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="backdrop" onpointerdown={(e) => { if (e.target === e.currentTarget) close(); }} onkeydown={(e) => { if (e.key === 'Escape') close(); }}>
  <div class="dialog" role="dialog" aria-modal="true" aria-label="New project">
  <form onsubmit={submit}>
    <header>New project <span class="spacer"></span><button type="button" class="icon-btn" aria-label="Close" onclick={close}>✕</button></header>
    <div class="body">
      <label class="field">Name <input class="input" bind:this={nameEl} bind:value={name} required maxlength="100" /></label>
      <label class="field">Key (optional) <input class="input" bind:value={key} maxlength="8" placeholder="Derived from the name, e.g. WEB" style="text-transform:uppercase" /></label>
      <label class="field">Description (optional) <textarea class="input" rows="3" bind:value={description}></textarea></label>
      {#if error}<p class="error-text" role="alert">{error}</p>{/if}
    </div>
    <footer><span class="spacer"></span><button type="button" class="btn" onclick={close}>Cancel</button><button class="btn primary" disabled={busy || !name.trim()}>Create project</button></footer>
  </form>
  </div>
</div>
