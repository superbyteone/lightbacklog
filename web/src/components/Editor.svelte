<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import type { Editor } from '@tiptap/core';
  import type { UploadResult } from '../lib/editor';

  let {
    value, onchange, readonly = false, upload, placeholder = 'Add a description… paste or drop screenshots', onerror = () => {}, minHeight = 180,
  }: {
    value: string;
    onchange: (markdown: string) => void;
    readonly?: boolean;
    upload: (file: File) => Promise<UploadResult>;
    placeholder?: string;
    onerror?: (message: string) => void;
    minHeight?: number;
  } = $props();

  let host = $state<HTMLDivElement>();
  let fileInput = $state<HTMLInputElement>();
  let editor = $state<Editor | null>(null);
  let tick = $state(0);
  let failed = $state(false);
  let destroyed = false;

  onMount(async () => {
    try {
      const { createEditor } = await import('../lib/editor');
      if (destroyed || !host) return;
      editor = createEditor({
        element: host, content: value, editable: !readonly, placeholder, upload,
        onUpdate: (md) => { if (!destroyed) onchange(md); }, onTransaction: () => queueMicrotask(() => { if (!destroyed) tick++; }), onError: onerror,
      });
    } catch {
      failed = true;
    }
  });
  onDestroy(() => {
    // Svelte removes the DOM before running this, and removing the focused editor makes ProseMirror
    // emit a blur transaction. Writing Svelte state synchronously inside that callback would throw
    // (state_unsafe_mutation), so transactions only bump `tick` from a microtask, and not after destroy.
    destroyed = true;
    editor?.destroy();
  });

  $effect(() => {
    editor?.setEditable(!readonly);
  });

  // Reflect selection/formatting state in the toolbar.
  const active = (name: string, attrs?: Record<string, unknown>) => {
    void tick; // re-evaluate on every editor transaction
    return editor?.isActive(name, attrs) ?? false;
  };

  function setLink() {
    if (!editor) return;
    const prev = editor.getAttributes('link').href as string | undefined;
    const url = window.prompt('Link URL (http, https or mailto)', prev ?? 'https://');
    if (url === null) return;
    if (url.trim() === '') return void editor.chain().focus().extendMarkRange('link').unsetLink().run();
    if (!/^(https?:\/\/|mailto:)/i.test(url.trim())) return onerror('Links must start with http://, https:// or mailto:');
    editor.chain().focus().extendMarkRange('link').setLink({ href: url.trim() }).run();
  }

  async function pickFiles(files: FileList | null) {
    if (!files || !editor) return;
    await (editor as Editor & { insertFiles: (f: File[]) => Promise<void> }).insertFiles(Array.from(files));
  }

  const tools = $derived(
    editor
      ? [
          { id: 'h2', label: 'Heading', text: 'H', on: () => editor!.chain().focus().toggleHeading({ level: 2 }).run(), is: active('heading', { level: 2 }) },
          { id: 'h3', label: 'Subheading', text: 'H₂', on: () => editor!.chain().focus().toggleHeading({ level: 3 }).run(), is: active('heading', { level: 3 }) },
          { id: 'b', label: 'Bold', text: 'B', on: () => editor!.chain().focus().toggleBold().run(), is: active('bold') },
          { id: 'i', label: 'Italic', text: 'I', on: () => editor!.chain().focus().toggleItalic().run(), is: active('italic') },
          { id: 's', label: 'Strikethrough', text: 'S', on: () => editor!.chain().focus().toggleStrike().run(), is: active('strike') },
          { id: 'c', label: 'Inline code', text: '‹›', on: () => editor!.chain().focus().toggleCode().run(), is: active('code') },
          { id: 'ul', label: 'Bullet list', text: '•', on: () => editor!.chain().focus().toggleBulletList().run(), is: active('bulletList') },
          { id: 'ol', label: 'Numbered list', text: '1.', on: () => editor!.chain().focus().toggleOrderedList().run(), is: active('orderedList') },
          { id: 'tl', label: 'Checklist', text: '☑', on: () => editor!.chain().focus().toggleTaskList().run(), is: active('taskList') },
          { id: 'q', label: 'Quote', text: '❝', on: () => editor!.chain().focus().toggleBlockquote().run(), is: active('blockquote') },
          { id: 'cb', label: 'Code block', text: '{ }', on: () => editor!.chain().focus().toggleCodeBlock().run(), is: active('codeBlock') },
          { id: 'l', label: 'Link', text: '🔗', on: setLink, is: active('link') },
          { id: 'img', label: 'Insert image or file', text: '🖼', on: () => fileInput?.click(), is: false },
        ]
      : [],
  );
</script>

<div class="editor" class:readonly>
  {#if !readonly && editor}
    <div class="toolbar" role="toolbar" aria-label="Formatting">
      {#each tools as t (t.id)}
        <button type="button" class="tb" class:on={t.is} aria-label={t.label} title={t.label} aria-pressed={t.is} onmousedown={(e) => e.preventDefault()} onclick={t.on}>{t.text}</button>
      {/each}
      <input bind:this={fileInput} type="file" multiple hidden onchange={(e) => { void pickFiles(e.currentTarget.files); e.currentTarget.value = ''; }} />
    </div>
  {/if}
  {#if !editor && !failed}<p class="muted loading" style="min-height:{minHeight}px">Loading editor…</p>{/if}
  {#if failed}<p class="error-text">The editor could not be loaded. Reload the page to try again.</p>{/if}
  <div class="host" bind:this={host} style="min-height:{editor ? minHeight : 0}px"></div>
</div>

<style>
  .editor { border: 1px solid var(--line-strong); border-radius: 8px; background: var(--bg); }
  .editor:focus-within { border-color: var(--accent); outline: 2px solid var(--accent-soft); }
  .editor.readonly { border-color: var(--line); }
  .toolbar { display: flex; flex-wrap: wrap; gap: 2px; padding: 4px; border-bottom: 1px solid var(--line); background: var(--bg-soft); border-radius: 8px 8px 0 0; }
  .tb { min-width: 28px; height: 26px; padding: 0 6px; border: 0; border-radius: 5px; background: transparent; color: var(--muted); font-size: 13px; font-weight: 600; }
  .tb:hover { background: var(--bg-hover); color: var(--fg); }
  .tb.on { background: var(--accent-soft); color: var(--accent); }
  .loading { margin: 0; padding: 12px; }
  .host { padding: 10px 12px; }
  .host :global(.prose) { outline: none; min-height: inherit; line-height: 1.6; overflow-wrap: anywhere; }
  .host :global(.prose > * + *) { margin-top: 0.6em; }
  .host :global(.prose h1) { font-size: 1.5em; margin-bottom: 0.2em; }
  .host :global(.prose h2) { font-size: 1.25em; }
  .host :global(.prose h3) { font-size: 1.08em; }
  .host :global(.prose h1), .host :global(.prose h2), .host :global(.prose h3) { line-height: 1.25; }
  .host :global(.prose ul), .host :global(.prose ol) { padding-left: 1.4em; margin: 0; }
  .host :global(.prose li > p) { margin: 0; }
  /* A task item's content isn't `li > p` like a plain list item: TipTap wraps it in an extra `div` (the node
     view's contentDOM), so the rule above never reaches it and its paragraph keeps the browser's default
     top margin — the actual cause of "Hello" rendering well below the checkbox rather than beside it. */
  .host :global(.prose ul[data-type='taskList'] li > div > p) { margin: 0; }
  .host :global(.prose ul[data-type='taskList']) { list-style: none; padding-left: 0.2em; }
  .host :global(.prose ul[data-type='taskList'] li) { display: flex; gap: 8px; align-items: flex-start; }
  /* The label's height matches the paragraph's line-height (1.6em) so the checkbox centers on the text's first
     line instead of its own (much shorter) native height; flex-start on the li then keeps it pinned there even
     when the item wraps onto further lines. */
  .host :global(.prose ul[data-type='taskList'] li > label) { flex: none; display: flex; align-items: center; height: 1.6em; margin: 0; user-select: none; }
  .host :global(.prose ul[data-type='taskList'] li > label input) { margin: 0; }
  .host :global(.prose ul[data-type='taskList'] li > div) { flex: 1; min-width: 0; }
  .host :global(.prose ul[data-type='taskList'] li[data-checked='true'] > div) { color: var(--muted); text-decoration: line-through; }
  .host :global(.prose code) { padding: 1px 5px; border-radius: 4px; background: var(--bg-soft); font: 0.9em ui-monospace, SFMono-Regular, Menlo, monospace; }
  .host :global(.prose pre) { padding: 10px 12px; border-radius: 6px; background: var(--bg-soft); border: 1px solid var(--line); overflow: auto; }
  .host :global(.prose pre code) { padding: 0; background: none; }
  .host :global(.prose blockquote) { margin-left: 0; padding-left: 12px; border-left: 3px solid var(--line-strong); color: var(--muted); }
  .host :global(.prose img) { max-width: 100%; height: auto; border-radius: 6px; border: 1px solid var(--line); }
  .host :global(.prose img.ProseMirror-selectednode) { outline: 2px solid var(--accent); }
  .host :global(.prose a) { color: var(--accent); text-decoration: underline; cursor: pointer; }
  .host :global(.prose p.is-editor-empty:first-child::before) { content: attr(data-placeholder); float: left; height: 0; color: var(--muted); pointer-events: none; }
</style>
