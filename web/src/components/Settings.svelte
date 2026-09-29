<script lang="ts">
  import { onMount } from 'svelte';
  import { api, del, get, post } from '../lib/api';
  import { app, errorText, toast } from '../lib/store.svelte';
  import type { ApiToken } from '../lib/types';
  import ThemeSwitch from './ThemeSwitch.svelte';

  // --- password ---
  let current = $state('');
  let next = $state('');
  let pwMsg = $state('');
  let pwErr = $state('');

  async function changePassword(e: Event) {
    e.preventDefault();
    pwMsg = pwErr = '';
    try {
      await post('/api/v1/me/password', { current_password: current, new_password: next });
      pwMsg = 'Password changed. Other sessions were signed out.';
      current = next = '';
    } catch (err) {
      pwErr = errorText(err);
    }
  }

  // --- tokens ---
  let tokens = $state<ApiToken[]>([]);
  let tname = $state('');
  let tscope = $state<'read' | 'write'>('write');
  let tprojects = $state<string[]>([]);
  let secret = $state('');
  let tokErr = $state('');
  let showRevoked = $state(false);

  const revokedCount = $derived(tokens.filter((t) => t.revoked_at).length);
  const shownTokens = $derived(showRevoked ? tokens : tokens.filter((t) => !t.revoked_at));

  function fmt(iso: string) {
    return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  }

  async function loadTokens() {
    tokens = (await get<ApiToken[]>('/api/v1/tokens')).data;
  }
  async function createToken(e: Event) {
    e.preventDefault();
    tokErr = '';
    try {
      const r = await post<ApiToken & { secret: string }>('/api/v1/tokens', { name: tname, scope: tscope, projects: tprojects });
      secret = r.data.secret;
      tname = '';
      tprojects = [];
      await loadTokens();
    } catch (err) {
      tokErr = errorText(err);
    }
  }
  async function revoke(t: ApiToken) {
    if (!confirm(`Revoke token “${t.name}”? Agents using it will lose access immediately.`)) return;
    try {
      await del(`/api/v1/tokens/${t.id}`);
      await loadTokens();
    } catch (err) {
      toast(errorText(err), 'error');
    }
  }
  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast('Copied', 'success');
    } catch {
      toast('Copy failed — select the text and copy it manually', 'error');
    }
  }

  // --- users (admin) ---
  interface U { id: string; username: string; email: string; is_admin: boolean; disabled_at?: string }
  let users = $state<U[]>([]);
  let nu = $state({ username: '', password: '', is_admin: false });
  let userErr = $state('');
  async function loadUsers() {
    if (app.me?.is_admin) users = (await get<U[]>('/api/v1/users')).data;
  }
  async function createUser(e: Event) {
    e.preventDefault();
    userErr = '';
    try {
      await post('/api/v1/users', nu);
      nu = { username: '', password: '', is_admin: false };
      await loadUsers();
      toast('User created', 'success');
    } catch (err) {
      userErr = errorText(err);
    }
  }
  async function toggleUser(u: U) {
    try {
      await post(`/api/v1/users/${u.id}/${u.disabled_at ? 'enable' : 'disable'}`);
      await loadUsers();
    } catch (err) {
      toast(errorText(err), 'error');
    }
  }

  onMount(() => {
    loadTokens().catch((e) => (tokErr = errorText(e)));
    loadUsers().catch(() => {});
  });

  const origin = location.origin;
</script>

<section class="page">
  <h1>Settings</h1>

  <div class="block">
    <h2>Appearance</h2>
    <p class="muted desc">Auto follows your device's light or dark setting. Your choice is remembered on this device.</p>
    <div class="theme-row"><ThemeSwitch /></div>
  </div>

  <div class="block">
    <h2>API tokens for AI agents</h2>
    <p class="muted desc">Tokens let Claude Code, Codex and scripts use the API and MCP server. They act as you, can be limited to read-only or to specific projects, and can be revoked at any time. See <code>docs/agents.md</code>.</p>
    {#if secret}
      <div class="secret" role="status">
        <strong>Copy this token now — it will not be shown again.</strong>
        <code>{secret}</code>
        <div class="row"><button class="btn" onclick={() => copy(secret)}>Copy token</button><button class="btn ghost" onclick={() => (secret = '')}>Done</button></div>
        <p class="muted small">Claude Code: <code>claude mcp add --transport http lightbacklog http://127.0.0.1:8100/mcp --header "Authorization: Bearer &lt;token&gt;"</code></p>
      </div>
    {/if}
    <form class="inline" onsubmit={createToken}>
      <input class="input" placeholder="Token name, e.g. codex-webapp" bind:value={tname} required maxlength="80" aria-label="Token name" />
      <select class="input sel" bind:value={tscope} aria-label="Scope"><option value="write">Read &amp; write</option><option value="read">Read only</option></select>
      <select class="input sel" multiple size="1" bind:value={tprojects} aria-label="Restrict to projects" title="Hold Ctrl/Cmd to pick several; none = all your projects">
        {#each app.projects as p (p.id)}<option value={p.key}>{p.name} ({p.key})</option>{/each}
      </select>
      <button class="btn primary" disabled={!tname.trim()}>Create token</button>
    </form>
    {#if tokErr}<p class="error-text">{tokErr}</p>{/if}
    {#if revokedCount > 0}
      <label class="chk small"><input type="checkbox" bind:checked={showRevoked} /> Show revoked ({revokedCount})</label>
    {/if}
    <ul class="list">
      {#each shownTokens as t (t.id)}
        <li class:revoked={t.revoked_at}>
          <strong>{t.name}</strong> <code>{t.prefix}…</code>
          <span class="chip" style="--chip:{t.scope === 'write' ? '#d97706' : '#2563eb'}">{t.scope}</span>
          {#if t.project_ids.length}<span class="muted">{t.project_ids.length} project{t.project_ids.length === 1 ? '' : 's'}</span>{/if}
          <span class="spacer"></span>
          <span class="muted small">
            {#if t.revoked_at}Created {fmt(t.created_at)} · Revoked {fmt(t.revoked_at)}
            {:else if t.last_used_at}used {new Date(t.last_used_at).toLocaleDateString()}
            {:else}never used{/if}
          </span>
          {#if !t.revoked_at}<button class="btn danger" onclick={() => revoke(t)}>Revoke</button>{/if}
        </li>
      {/each}
      {#if shownTokens.length === 0}<li class="muted">{tokens.length === 0 ? 'No tokens yet.' : 'No active tokens.'}</li>{/if}
    </ul>
  </div>

  <div class="block">
    <h2>Password</h2>
    <form class="inline" onsubmit={changePassword}>
      <input class="input" type="password" placeholder="Current password" bind:value={current} autocomplete="current-password" required />
      <input class="input" type="password" placeholder="New password (10+ characters)" bind:value={next} autocomplete="new-password" required minlength="10" />
      <button class="btn">Change password</button>
    </form>
    {#if pwMsg}<p class="ok">{pwMsg}</p>{/if}
    {#if pwErr}<p class="error-text">{pwErr}</p>{/if}
  </div>

  {#if app.me?.is_admin}
    <div class="block">
      <h2>Users</h2>
      <ul class="list">
        {#each users as u (u.id)}
          <li class:revoked={u.disabled_at}>
            <strong>{u.username}</strong>{#if u.is_admin}<span class="chip">admin</span>{/if}{#if u.disabled_at}<span class="muted">disabled</span>{/if}
            <span class="spacer"></span>
            {#if u.id !== app.me.id}<button class="btn" onclick={() => toggleUser(u)}>{u.disabled_at ? 'Enable' : 'Disable'}</button>{/if}
          </li>
        {/each}
      </ul>
      <p class="form-label">Add a user</p>
      <form class="inline" onsubmit={createUser}>
        <input class="input" placeholder="Username" bind:value={nu.username} required />
        <input class="input" type="password" placeholder="Initial password (10+)" bind:value={nu.password} required minlength="10" autocomplete="new-password" />
        <label class="chk"><input type="checkbox" bind:checked={nu.is_admin} /> Admin</label>
        <button class="btn">Add user</button>
      </form>
      {#if userErr}<p class="error-text">{userErr}</p>{/if}
      <p class="muted small">Users only see projects they are added to. Admins do not automatically see other users' projects.</p>
    </div>
  {/if}
</section>

<style>
  .page { max-width: 820px; padding: 20px 24px 60px; overflow: auto; height: 100%; }
  h1 { margin: 0; padding-bottom: 20px; font-size: 28px; font-weight: 700; border-bottom: 1px solid var(--line); }
  .block { padding: 26px 0; border-bottom: 1px solid var(--line); }
  .block:last-child { border-bottom: none; padding-bottom: 0; }
  h2 { margin: 0 0 6px; font-size: 17px; font-weight: 700; }
  .desc { margin: 0 0 14px; font-size: 14px; line-height: 1.5; }
  .form-label { display: none; margin: 18px 0 8px; font-size: 12px; font-weight: 700; letter-spacing: 0.04em; text-transform: uppercase; color: var(--muted); }
  @media (max-width: 800px) {
    .form-label { display: block; }
  }
  .theme-row { max-width: 320px; margin: 8px 0; }
  .inline { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin: 10px 0; }
  .inline .input { flex: 1 1 200px; width: auto; }
  .sel { flex: 1 1 150px !important; width: auto !important; min-width: 0; }
  .list { list-style: none; margin: 8px 0; padding: 0; border: 1px solid var(--line); border-radius: var(--radius); }
  .list li { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; padding: 9px 14px; }
  .list li + li { border-top: 1px solid var(--line); }
  .revoked { opacity: 0.55; }
  .secret { display: grid; gap: 8px; padding: 12px 14px; margin: 10px 0; border: 1px solid var(--success); border-radius: var(--radius); background: color-mix(in srgb, var(--success) 8%, var(--bg)); }
  .secret code { word-break: break-all; font-size: 13px; }
  .row { display: flex; gap: 8px; }
  .small { font-size: 12px; margin: 0; }
  .ok { color: var(--success); }
  .chk { display: inline-flex; gap: 6px; align-items: center; }
  .chk.small { margin: 4px 0; color: var(--muted); }
  code { padding: 1px 5px; border-radius: 4px; background: var(--bg-soft); font-size: 12.5px; }
</style>
