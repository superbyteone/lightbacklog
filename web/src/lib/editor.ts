/**
 * Rich-text editor factory. This module is loaded lazily (dynamic import) so the ~100 KB
 * editor code is only downloaded when a description is first shown.
 *
 * Markdown is the stored format. The ProseMirror schema is an allow-list: raw HTML,
 * scripts and event handlers in pasted or stored text are dropped on load, and links are
 * limited to http, https and mailto.
 */
import { Editor, mergeAttributes } from '@tiptap/core';
import Image from '@tiptap/extension-image';
import Placeholder from '@tiptap/extension-placeholder';
import { TaskItem } from '@tiptap/extension-task-item';
import { TaskList } from '@tiptap/extension-task-list';
import { Markdown } from '@tiptap/markdown';
import StarterKit from '@tiptap/starter-kit';

/**
 * Images may only point at this server's uploads or plain http(s) URLs. Anything else (javascript:,
 * data:, file:, ...) is rendered as inert text; the stored Markdown is left untouched. The page CSP
 * additionally restricts img-src to same-origin.
 */
const SafeImage = Image.extend({
  renderHTML({ HTMLAttributes }) {
    const src = String(HTMLAttributes.src ?? '');
    if (!/^(\/files\/[0-9a-f-]{36}|https?:\/\/)/i.test(src)) return ['span', { class: 'blocked-image' }, `[image not shown: ${String(HTMLAttributes.alt ?? '').slice(0, 60)}]`];
    return ['img', mergeAttributes(this.options.HTMLAttributes, HTMLAttributes)];
  },
});

export interface UploadResult {
  url: string;
  inline: boolean;
  filename: string;
}

export interface EditorOptions {
  element: HTMLElement;
  content: string;
  editable: boolean;
  placeholder: string;
  upload: (file: File) => Promise<UploadResult>;
  onUpdate: (markdown: string) => void;
  onTransaction: () => void;
  onError: (message: string) => void;
}

export function createEditor(o: EditorOptions): Editor {
  let editor: Editor;

  const insertFiles = async (files: File[], pos?: number) => {
    for (const file of files) {
      try {
        const r = await o.upload(file);
        const node = r.inline
          ? { type: 'image', attrs: { src: r.url, alt: r.filename } }
          : { type: 'text', text: r.filename, marks: [{ type: 'link', attrs: { href: r.url } }] };
        const at = pos ?? editor.state.selection.to;
        editor.chain().focus().insertContentAt(at, [node, { type: 'text', text: ' ' }]).run();
        pos = undefined;
      } catch (e) {
        o.onError(`Upload failed: ${e instanceof Error ? e.message : String(e)}`);
      }
    }
  };

  editor = new Editor({
    element: o.element,
    editable: o.editable,
    extensions: [
      StarterKit.configure({
        heading: { levels: [1, 2, 3] },
        link: { openOnClick: false, autolink: true, protocols: ['http', 'https', 'mailto'], HTMLAttributes: { rel: 'noopener noreferrer nofollow', target: '_blank' } },
      }),
      Markdown,
      TaskList,
      TaskItem.configure({ nested: true }),
      SafeImage.configure({ inline: false, allowBase64: false }),
      Placeholder.configure({ placeholder: o.placeholder }),
    ],
    content: o.content,
    contentType: 'markdown',
    editorProps: {
      attributes: { class: 'prose', 'aria-label': 'Description', spellcheck: 'true' },
      handlePaste(_view, event) {
        const files = Array.from(event.clipboardData?.files ?? []);
        if (!files.length) return false;
        event.preventDefault();
        void insertFiles(files);
        return true;
      },
      handleDrop(view, event) {
        const files = Array.from(event.dataTransfer?.files ?? []);
        if (!files.length) return false;
        event.preventDefault();
        const pos = view.posAtCoords({ left: event.clientX, top: event.clientY })?.pos;
        void insertFiles(files, pos);
        return true;
      },
    },
    onUpdate: ({ editor: e }) => o.onUpdate(e.getMarkdown()),
    onTransaction: () => o.onTransaction(),
  });
  (editor as Editor & { insertFiles?: typeof insertFiles }).insertFiles = insertFiles;
  return editor;
}

export type { Editor };
