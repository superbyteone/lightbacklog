<script lang="ts">
  import { dismissToast, toasts } from '../lib/store.svelte';
</script>

<div class="toasts" aria-live="polite">
  {#each toasts as t (t.id)}
    <div class="toast {t.kind}" role={t.kind === 'error' ? 'alert' : 'status'}>
      <span>{t.text}</span>
      {#if t.action}
        <button class="btn" onclick={() => { t.action?.run(); dismissToast(t.id); }}>{t.action.label}</button>
      {/if}
      <button class="icon-btn" aria-label="Dismiss" onclick={() => dismissToast(t.id)}>✕</button>
    </div>
  {/each}
</div>

<style>
  .toast span { flex: 1; }
</style>
