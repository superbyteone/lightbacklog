<script lang="ts">
  import type { Snippet } from 'svelte';

  let { anchor, onclose, children, width, maxHeight = 360 }: { anchor: HTMLElement; onclose: () => void; children: Snippet; width?: number; maxHeight?: number } = $props();

  // Rendered on <body>: a popover inside something with a transform (like the sliding page header) would be positioned relative to it.
  function portal(node: HTMLElement) {
    const focused = node.querySelector<HTMLElement>(':focus'); // moving a node drops focus from what is inside it
    document.body.appendChild(node);
    focused?.focus();
    return { destroy: () => node.remove() };
  }

  let el = $state<HTMLDivElement>();
  let pos = $state({ left: 0, top: 0, maxH: 320 });

  // Popovers are positioned in layout-viewport coordinates but must stay inside the *visible* area,
  // which on phones shrinks (visual viewport) when the on-screen keyboard opens.
  function place() {
    const r = anchor.getBoundingClientRect();
    const w = Math.max(width ?? 0, Math.min(r.width, 320), 220);
    const vv = window.visualViewport;
    const vh = vv?.height ?? window.innerHeight;
    const top0 = vv?.offsetTop ?? 0;
    const spaceBelow = top0 + vh - r.bottom - 8;
    const bottom0 = top0 + vh - 8; // lowest usable edge; the anchor may sit below it (behind the keyboard)
    const spaceAbove = Math.min(r.top - 4, bottom0) - top0 - 4;
    const left = Math.min(Math.max(8, r.left), window.innerWidth - w - 8);
    if (Math.max(spaceBelow, spaceAbove) < 160) {
      // The anchor is hidden behind the keyboard or off-screen: pin the list to the top of what is visible.
      const maxH = Math.max(120, Math.min(maxHeight, vh - 16));
      pos = { left, top: top0 + 8, maxH };
      return;
    }
    const below = spaceBelow >= 220 || spaceBelow >= spaceAbove;
    const maxH = Math.max(160, Math.min(maxHeight, below ? spaceBelow : spaceAbove));
    pos = { left, top: below ? r.bottom + 4 : Math.max(top0 + 8, Math.min(r.top - 4, bottom0) - Math.min(maxH, el?.offsetHeight ?? maxH)), maxH };
  }

  $effect(() => {
    place();
    const down = (e: PointerEvent) => {
      if (el && !el.contains(e.target as Node) && !anchor.contains(e.target as Node)) onclose();
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onclose();
        anchor.focus?.();
      }
    };
    // Scrolling and resizing must not dismiss the popover: on phones the on-screen keyboard (opened by
    // a search box taking focus) shrinks the visual viewport, and treating the anchor as "off-screen"
    // because it now sits behind the keyboard made dropdowns flash and vanish. Follow the anchor
    // instead; close only when it is gone or has really scrolled out of the (pre-keyboard) layout.
    const fullHeight = window.innerHeight;
    const follow = (e?: Event) => {
      if (!anchor.isConnected) return onclose();
      if (e?.type === 'scroll' && !(e.target instanceof Node && el?.contains(e.target))) {
        const r = anchor.getBoundingClientRect();
        if (r.bottom < 0 || r.top > Math.max(fullHeight, window.innerHeight)) return onclose();
      }
      place();
    };
    document.addEventListener('pointerdown', down, true);
    document.addEventListener('keydown', key, true);
    window.addEventListener('scroll', follow, true);
    window.addEventListener('resize', follow);
    window.visualViewport?.addEventListener('resize', follow);
    window.visualViewport?.addEventListener('scroll', follow);
    return () => {
      document.removeEventListener('pointerdown', down, true);
      document.removeEventListener('keydown', key, true);
      window.removeEventListener('scroll', follow, true);
      window.removeEventListener('resize', follow);
      window.visualViewport?.removeEventListener('resize', follow);
      window.visualViewport?.removeEventListener('scroll', follow);
    };
  });
</script>

<div class="popover" use:portal bind:this={el} style="left:{pos.left}px; top:{pos.top}px; max-height:{pos.maxH}px; width:{Math.max(width ?? 0, 220)}px; display:flex; flex-direction:column;">
  {@render children()}
</div>
