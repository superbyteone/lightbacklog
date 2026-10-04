<script lang="ts">
  import { formatDue, todayISO } from '../lib/filters';
  import Popover from './Popover.svelte';

  let { value, overdue = false, onchange, readonly = false, always = false, pill = false, badge = false }: { value: string | null; overdue?: boolean; onchange: (v: string | null) => void; readonly?: boolean; always?: boolean; pill?: boolean; badge?: boolean } = $props();
  let open = $state(false);
  let btn = $state<HTMLButtonElement>();

  function pick(v: string | null) {
    open = false;
    btn?.focus();
    if (v !== value) onchange(v);
  }
</script>

<button class="cell-btn" class:overdue class:badged={badge && !!value} bind:this={btn} disabled={readonly} aria-label="Due date: {value ?? 'none'}" aria-expanded={open}
  onclick={(e) => { e.stopPropagation(); open = !open; }}>
  {#if badge && value}<span class="badge">Due {formatDue(value)}</span>
  {:else if pill}<span class="pill" class:unset={!value}>{value ? formatDue(value) : 'Set date'}</span>
  {:else if value}{formatDue(value)}{:else}<span class="muted add" class:always>{readonly ? (always ? 'None' : '') : always ? 'Set date' : '+ date'}</span>{/if}
</button>

{#if open && btn}
  <Popover anchor={btn} onclose={() => (open = false)}>
    <div class="due">
      <input class="input" type="date" value={value ?? ''} aria-label="Due date"
        onchange={(e) => pick((e.currentTarget as HTMLInputElement).value || null)} />
      <div class="quick">
        <button class="btn ghost" onclick={() => pick(todayISO())}>Today</button>
        <button class="btn ghost" onclick={() => pick(todayISO(1))}>Tomorrow</button>
        <button class="btn ghost" onclick={() => pick(todayISO(7))}>Next week</button>
        {#if value}<button class="btn ghost danger" onclick={() => pick(null)}>Clear</button>{/if}
      </div>
    </div>
  </Popover>
{/if}

<style>
  .cell-btn { display: flex; align-items: center; width: 100%; height: 100%; padding: 0 4px; margin: 0 -4px; border: 0; background: transparent; text-align: left; white-space: nowrap; }
  .cell-btn:not(:disabled):hover { background: var(--bg-hover); border-radius: 6px; }
  .cell-btn:not(:disabled):hover .pill { background: var(--bg-hover); box-shadow: inset 0 0 0 1px var(--line-strong); }
  .badge { display: inline-flex; align-items: center; height: 22px; padding: 0 9px; border: 1px solid var(--due-line, #d2d6dd); border-radius: 11px; font-size: 12px; font-weight: 600; color: var(--due-fg, #4b5563); }
  :global(:root[data-theme='dark']) .badge { --due-line: var(--line-strong); --due-fg: var(--muted); }
  @media (prefers-color-scheme: dark) { :global(:root:not([data-theme='light'])) .badge { --due-line: var(--line-strong); --due-fg: var(--muted); } }
  /* Cards badge: the button hugs the pill so the hover state is the pill itself, not an offset rectangle behind it */
  .cell-btn.badged { width: auto; height: auto; padding: 0; margin: 0; }
  .cell-btn.badged:not(:disabled):hover { background: transparent; }
  .cell-btn.badged:not(:disabled):hover .badge { background: var(--bg-hover); }
  .overdue .badge { border-color: var(--danger); color: var(--danger); }
  .overdue { color: var(--danger); font-weight: 600; }
  .add { opacity: 0; font-size: 12px; }
  .add.always { opacity: 1; font-size: 13px; }
  .cell-btn:hover .add, .cell-btn:focus-visible .add { opacity: 1; }
  .due { display: grid; gap: 8px; padding: 10px; }
  .quick { display: flex; flex-wrap: wrap; gap: 4px; }
  .quick .btn { height: 28px; padding: 0 8px; }
</style>
