<script lang="ts">
  let {
    value, onchange, readonly = false, always = false,
  }: { value: number | null; onchange: (v: number | null) => void; readonly?: boolean; always?: boolean } = $props();

  let editing = $state(false);
  let draft = $state('');
  let error = $state(false);
  let input = $state<HTMLInputElement>();

  function edit() {
    if (readonly) return;
    draft = value === null ? '' : String(value);
    error = false;
    editing = true;
  }
  $effect(() => {
    if (editing) {
      input?.focus();
      input?.select();
    }
  });

  function commit() {
    if (!editing) return;
    const text = draft.trim();
    if (text === '') {
      editing = false;
      if (value !== null) onchange(null);
      return;
    }
    const n = Number(text);
    if (!Number.isInteger(n) || n < 0 || n > 1_000_000) {
      error = true; // stay in edit mode and say why
      return;
    }
    editing = false;
    if (n !== value) onchange(n);
  }
</script>

{#if editing}
  <input class="input seq-input" class:bad={error} bind:this={input} bind:value={draft} inputmode="numeric" aria-label="Sequence" aria-invalid={error}
    title={error ? 'Enter a whole number from 0 to 1,000,000, or leave empty for none' : 'Order of work: 1 is done first'}
    onblur={commit} oninput={() => (error = false)}
    onkeydown={(e) => { e.stopPropagation(); if (e.key === 'Enter') commit(); else if (e.key === 'Escape') editing = false; }} />
{:else}
  <button class="seq-btn" disabled={readonly} aria-label="Sequence: {value === null ? 'none' : value}" title="Order of work: 1 is done first"
    onclick={(e) => { e.stopPropagation(); edit(); }}>
    {#if value !== null}<span class="num">{value}</span>{:else}<span class="muted add" class:always>{readonly ? (always ? 'None' : '') : always ? 'Set order' : '+ order'}</span>{/if}
  </button>
{/if}

<style>
  .seq-btn { display: flex; align-items: center; width: 100%; height: 100%; padding: 0 4px; margin: 0 -4px; border: 0; background: transparent; text-align: left; }
  .seq-btn:not(:disabled):hover { background: var(--bg-hover); border-radius: 6px; }
  .num { font-variant-numeric: tabular-nums; font-weight: 600; }
  .add { opacity: 0; font-size: 12px; white-space: nowrap; }
  .seq-btn:hover .add, .seq-btn:focus-visible .add { opacity: 1; }
  .add.always { opacity: 1; font-size: 13px; }
  .seq-input { height: 28px; width: 100%; min-width: 0; padding: 0 6px; font-variant-numeric: tabular-nums; }
  .seq-input.bad { border-color: var(--danger); }
</style>
