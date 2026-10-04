<script lang="ts">
  import { onMount } from 'svelte';
  import { del, get, patch, post, api } from '../lib/api';
  import { navigate } from '../lib/router.svelte';
  import { app, errorText, projectByKey, refreshProjects, setProjectFavorite, toast } from '../lib/store.svelte';
  import type { Role } from '../lib/types';
  import { ui } from '../lib/ui.svelte';

  interface Member { user_id: string; username: string; role: Role }
  const colors = ['#2563eb', '#7c3aed', '#db2777', '#dc2626', '#ea580c', '#ca8a04', '#16a34a', '#0d9488', '#0891b2', '#4b5563'];

  const project = $derived(ui.projectSettings ? projectByKey(ui.projectSettings) : undefined);
  const isOwner = $derived(project?.role === 'owner');

  let name = $state('');
  let description = $state('');
  let color = $state('#2563eb');
  let members = $state<Member[]>([]);
  let newUser = $state('');
  let newRole = $state<Role>('editor');
  let error = $state('');
  let saving = $state(false);
  let confirmArchive = $state(false);

  onMount(async () => {
    if (!project) return;
    name = project.name;
    description = project.description;
    color = project.color;
    await loadMembers();
  });

  async function loadMembers() {
    if (!project) return;
    try {
      members = (await get<Member[]>(`/api/v1/projects/${project.id}/members`)).data;
    } catch (e) {
      error = errorText(e);
    }
  }

  function close() {
    ui.projectSettings = null;
  }

  async function save(e: Event) {
    e.preventDefault();
    if (!project) return;
    saving = true;
    error = '';
    try {
      await patch(`/api/v1/projects/${project.id}`, { name: name.trim(), description, color });
      await refreshProjects();
      toast('Project saved', 'success');
      close();
    } catch (err) {
      error = errorText(err);
    } finally {
      saving = false;
    }
  }

  async function addMember(e: Event) {
    e.preventDefault();
    if (!project || !newUser.trim()) return;
    error = '';
    try {
      const r = await api<Member[]>('PUT', `/api/v1/projects/${project.id}/members/${encodeURIComponent(newUser.trim())}`, { role: newRole });
      members = r.data;
      newUser = '';
    } catch (err) {
      error = errorText(err);
    }
  }

  async function setRole(m: Member, role: Role) {
    if (!project) return;
    error = '';
    try {
      members = (await api<Member[]>('PUT', `/api/v1/projects/${project.id}/members/${encodeURIComponent(m.username)}`, { role })).data;
    } catch (err) {
      error = errorText(err);
      await loadMembers();
    }
  }

  async function remove(m: Member) {
    if (!project) return;
    const leaving = m.user_id === app.me?.id;
    if (leaving && !confirm(`Leave ${project.name}? You will lose access to it.`)) return;
    try {
      await del(`/api/v1/projects/${project.id}/members/${m.user_id}`);
      if (leaving) {
        await refreshProjects();
        close();
        navigate('/');
      } else await loadMembers();
    } catch (err) {
      error = errorText(err);
    }
  }

  async function setFavorite(v: boolean) {
    if (!project) return;
    try {
      await setProjectFavorite(project, v);
    } catch (err) {
      error = errorText(err);
    }
  }

  async function archive() {
    if (!project) return;
    // Read these first: once the project list is refreshed the archived project is no longer in it,
    // so `project` becomes undefined (this used to throw and leave the dialog saying "Project not found").
    const { id, name } = project;
    try {
      await post(`/api/v1/projects/${id}/archive`);
      await refreshProjects();
      toast(`Archived ${name}`, 'success');
      close();
      navigate('/archived');
    } catch (err) {
      error = errorText(err);
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="backdrop" onpointerdown={(e) => { if (e.target === e.currentTarget) close(); }} onkeydown={(e) => { if (e.key === 'Escape') close(); }}>
  <div class="dialog" role="dialog" aria-modal="true" aria-label="Project settings">
    <header>{project?.name ?? 'Project'} — settings <span class="spacer"></span><button class="icon-btn" aria-label="Close" onclick={close}>✕</button></header>
    {#if project}
      <div class="body">
        <form class="grid" onsubmit={save}>
          <label class="field">Name <input class="input" bind:value={name} required maxlength="100" disabled={!isOwner} /></label>
          <label class="field">Key <input class="input" value={project.key} disabled title="The key is part of every task ID and cannot be changed" /></label>
          <label class="field wide">Description <textarea class="input" rows="2" bind:value={description} disabled={!isOwner}></textarea></label>
          <div class="field wide">Color
            <div class="swatches">
              {#each colors as c}
                <button type="button" class="sw" class:on={color === c} style="background:{c}" aria-label="Color {c}" aria-pressed={color === c} disabled={!isOwner} onclick={() => (color = c)}></button>
              {/each}
            </div>
          </div>
          <div class="field wide">Favourite
            <div class="yn">
              <button type="button" class="btn {project.favorite ? 'primary' : ''}" aria-pressed={project.favorite} onclick={() => setFavorite(true)}>Yes</button>
              <button type="button" class="btn {project.favorite ? '' : 'primary'}" aria-pressed={!project.favorite} onclick={() => setFavorite(false)}>No</button>
            </div>
          </div>
          {#if isOwner}<div class="wide"><button class="btn primary" disabled={saving || !name.trim()}>Save changes</button></div>{/if}
        </form>

        <h3>Members</h3>
        <ul class="members">
          {#each members as m (m.user_id)}
            <li>
              <span class="who">{m.username}{#if m.user_id === app.me?.id} <span class="muted">(you)</span>{/if}</span>
              <span class="spacer"></span>
              {#if isOwner}
                <select class="input role" value={m.role} aria-label="Role of {m.username}" onchange={(e) => setRole(m, e.currentTarget.value as Role)}>
                  <option value="owner">Owner</option><option value="editor">Editor</option><option value="viewer">Viewer</option>
                </select>
              {:else}<span class="chip">{m.role}</span>{/if}
              {#if isOwner || m.user_id === app.me?.id}<button class="btn ghost danger" onclick={() => remove(m)}>{m.user_id === app.me?.id ? 'Leave' : 'Remove'}</button>{/if}
            </li>
          {/each}
        </ul>
        {#if isOwner}
          <form class="add" onsubmit={addMember}>
            <input class="input" placeholder="Username to add" bind:value={newUser} aria-label="Username to add" />
            <select class="input role" bind:value={newRole} aria-label="Role for new member">
              <option value="editor">Editor</option><option value="viewer">Viewer</option><option value="owner">Owner</option>
            </select>
            <button class="btn" disabled={!newUser.trim()}>Add member</button>
          </form>
          <p class="muted small">Viewers can read; editors can create and change tasks; owners also manage settings and members.</p>
        {/if}

        {#if isOwner}
          <h3>Archive</h3>
          {#if confirmArchive}
            <p>Archive <strong>{project.name}</strong>? It disappears from the sidebar and All Tasks, and its tasks become read-only. You can restore it, or delete it permanently, from Archived Projects.</p>
            <div class="row"><button class="btn danger" onclick={archive}>Archive project</button><button class="btn" onclick={() => (confirmArchive = false)}>Cancel</button></div>
          {:else}
            <button class="btn danger" onclick={() => (confirmArchive = true)}>Archive project…</button>
          {/if}
        {/if}
        {#if error}<p class="error-text" role="alert">{error}</p>{/if}
      </div>
    {:else}
      <div class="body"><p class="muted">Project not found.</p></div>
    {/if}
  </div>
</div>

<style>
  .dialog { width: min(600px, 100%); }
  .grid { display: grid; grid-template-columns: 1fr 140px; gap: 12px; }
  @media (max-width: 520px) { .grid { grid-template-columns: 1fr; } }
  .wide { grid-column: 1 / -1; }
  .field { display: grid; gap: 4px; font-size: 12px; font-weight: 600; color: var(--muted); }
  .swatches { display: flex; gap: 6px; }
  .yn { display: flex; gap: 6px; }
  .sw { width: 24px; height: 24px; border-radius: 50%; border: 2px solid transparent; }
  .sw.on { border-color: var(--fg); outline: 2px solid var(--bg); outline-offset: -4px; }
  h3 { margin: 6px 0 0; font-size: 14px; }
  .members { list-style: none; margin: 0; padding: 0; border: 1px solid var(--line); border-radius: 8px; }
  .members li { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 8px; padding: 6px 10px; }
  .members li + li { border-top: 1px solid var(--line); }
  .who { font-weight: 600; }
  .role { width: 110px; height: 30px; flex: none; }
  .add { display: flex; flex-wrap: wrap; gap: 8px; }
  .add .input:first-child { flex: 1 1 160px; min-width: 0; }
  .row { display: flex; gap: 8px; }
  .small { font-size: 12px; margin: 0; }
</style>
