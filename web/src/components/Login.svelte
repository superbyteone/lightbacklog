<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError } from '../lib/api';
  import { login } from '../lib/store.svelte';

  let username = $state('');
  let password = $state('');
  let error = $state('');
  let busy = $state(false);
  let userEl = $state<HTMLInputElement>();
  onMount(() => userEl?.focus());

  async function submit(e: Event) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await login(username, password);
    } catch (err) {
      error = err instanceof ApiError ? (err.status === 429 ? 'Too many attempts. Wait a few minutes and try again.' : err.message) : 'Login failed';
    } finally {
      busy = false;
    }
  }
</script>

<main>
  <form onsubmit={submit}>
    <h1>LightBacklog</h1>
    <p class="muted">Sign in to continue</p>
    <label class="field">Username <input class="input" bind:value={username} autocomplete="username" required bind:this={userEl} /></label>
    <label class="field">Password <input class="input" type="password" bind:value={password} autocomplete="current-password" required /></label>
    {#if error}<p class="error-text" role="alert">{error}</p>{/if}
    <button class="btn primary" disabled={busy || !username || !password}>{busy ? 'Signing in…' : 'Sign in'}</button>
  </form>
</main>

<style>
  main { min-height: 100%; display: grid; place-items: center; padding: 24px; background: var(--bg-soft); }
  form { display: grid; gap: 14px; width: min(360px, 100%); padding: 28px; border: 1px solid var(--line); border-radius: 12px; background: var(--bg); box-shadow: var(--shadow); }
  h1 { margin: 0; font-size: 22px; }
  p { margin: 0; }
  .btn { justify-content: center; height: 38px; }
</style>
