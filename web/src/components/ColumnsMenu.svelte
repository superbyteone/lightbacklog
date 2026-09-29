<script lang="ts">
  import { COLUMNS, type ColumnId, canHide, currentClass, deviceLabel, isCustomised, layout, moveColumn, moveColumnTo, ordered, resetLayout, toggleColumn, type ViewMode } from '../lib/layout.svelte';

  let { cards = false, lockedProject = false }: { cards?: boolean; lockedProject?: boolean } = $props();
  const cls = $derived((layout.viewport, currentClass()));
  const mode = $derived<ViewMode>(cards ? 'cards' : 'list');
  const skip = (id: ColumnId) => id === 'project' && lockedProject;
  const list = $derived((layout.viewport, layout.prefs, ordered(cls, mode).filter((c) => !skip(c.id))));
  const def = (id: string) => COLUMNS.find((c) => c.id === id)!;

  // Drag a row by its handle (mouse, pen or touch). The column follows the pointer live, so the menu itself shows where it will land.
  let ul = $state<HTMLUListElement>();
  let dragging = $state<ColumnId | null>(null);

  function grab(e: PointerEvent, id: ColumnId) {
    if (e.button !== 0) return;
    e.preventDefault();
    dragging = id;
    const move = (ev: PointerEvent) => {
      const rows = Array.from(ul?.children ?? []) as HTMLElement[];
      let to = rows.findIndex((r) => ev.clientY < r.getBoundingClientRect().bottom);
      if (to < 0) to = rows.length - 1;
      if (to !== list.findIndex((c) => c.id === id)) moveColumnTo(id, to, cls, mode, skip);
    };
    const end = () => {
      dragging = null;
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', end);
      window.removeEventListener('pointercancel', end);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', end);
    window.addEventListener('pointercancel', end);
  }
</script>

<div class="menu">
  <div class="head">
    <strong>{cards ? 'Fields on cards' : 'Columns'}</strong>
    {#if cls !== 'desktop'}<span class="muted">{deviceLabel[cls]} layout</span>{/if}
  </div>
  <ul bind:this={ul}>
    {#each list as c, i (c.id)}
      <li class:dragging={dragging === c.id}>
        <span class="grip" role="presentation" aria-hidden="true" title="Drag to reorder" onpointerdown={(e) => grab(e, c.id)}>⠿</span>
        <label class:off={c.hidden}>
          <input type="checkbox" checked={!c.hidden} disabled={!c.hidden && !canHide(c.id, cls, mode)} onchange={() => toggleColumn(c.id, cls, mode)} />
          <span class="name">{def(c.id).label}</span>
          {#if def(c.id).note}<span class="muted note">{def(c.id).note}</span>{/if}
        </label>
        <span class="mv">
          <button class="icon-btn" aria-label="Move {def(c.id).label} earlier" disabled={i === 0} onclick={() => moveColumn(c.id, -1, cls, mode, skip)}>↑</button>
          <button class="icon-btn" aria-label="Move {def(c.id).label} later" disabled={i === list.length - 1} onclick={() => moveColumn(c.id, 1, cls, mode, skip)}>↓</button>
        </span>
      </li>
    {/each}
  </ul>
  <p class="muted hint">
    Saved to your account for {deviceLabel[cls].toLowerCase()}-sized screens and used in every project.{cards ? '' : ' Drag a column edge to change its width.'}{cls === 'desktop' ? ' List and Cards are configured separately.' : ''} Keep the ID or the Title visible so tasks can be opened.
  </p>
  <button class="btn ghost" disabled={!isCustomised(cls, mode)} onclick={() => resetLayout(cls, mode)}>{cls === 'desktop' ? `Reset ${cards ? 'fields' : 'columns'}` : `Reset ${deviceLabel[cls].toLowerCase()} layout`}</button>
</div>

<style>
  .menu { display: grid; gap: 8px; padding: 10px; overflow: auto; min-width: 0; min-height: 0; }
  .head { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 2px; }
  li { display: flex; align-items: center; gap: 6px; }
  .grip { flex: none; display: grid; place-items: center; width: 22px; height: 30px; color: var(--muted); font-size: 16px; line-height: 1; cursor: grab; touch-action: none; user-select: none; border-radius: 6px; }
  .grip:hover { background: var(--bg-hover); color: var(--fg); }
  li.dragging { background: var(--accent-soft); border-radius: 6px; }
  li.dragging .grip { cursor: grabbing; color: var(--accent); }
  label { display: flex; align-items: center; gap: 8px; flex: 1; min-width: 0; min-height: 30px; padding: 0 6px; border-radius: 6px; }
  label:hover { background: var(--bg-hover); }
  label.off .name { color: var(--muted); text-decoration: line-through; text-decoration-color: color-mix(in srgb, var(--muted) 60%, transparent); }
  .note { font-size: 11px; }
  .mv { display: flex; }
  .hint { margin: 0; font-size: 12px; }
  button.btn { justify-self: start; }
</style>
