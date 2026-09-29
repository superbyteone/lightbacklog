<script lang="ts">
  import { setTheme, theme, type Theme } from '../lib/theme.svelte';

  const options: { value: Theme; label: string; icon: string; hint: string }[] = [
    { value: 'auto', label: 'Auto', icon: '◐', hint: 'Follow your device setting' },
    { value: 'light', label: 'Light', icon: '☀', hint: 'Always light' },
    { value: 'dark', label: 'Dark', icon: '☾', hint: 'Always dark' },
  ];
</script>

<div class="switch" role="radiogroup" aria-label="Colour theme">
  {#each options as o (o.value)}
    <button type="button" role="radio" aria-checked={theme.value === o.value} class:on={theme.value === o.value} title={o.hint} onclick={() => setTheme(o.value)}>
      <span aria-hidden="true">{o.icon}</span>{o.label}
    </button>
  {/each}
</div>

<style>
  .switch { display: flex; padding: 2px; border: 1px solid var(--line-strong); border-radius: 8px; background: var(--bg); }
  button { display: inline-flex; align-items: center; justify-content: center; gap: 5px; flex: 1; min-height: 30px; padding: 0 8px; border: 0; border-radius: 6px; background: transparent; color: var(--muted); font-size: 13px; white-space: nowrap; }
  button:hover { color: var(--fg); }
  button.on { background: var(--accent-soft); color: var(--accent); font-weight: 600; }
</style>
