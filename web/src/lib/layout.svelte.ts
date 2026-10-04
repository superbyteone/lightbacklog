/**
 * Task list column layout: which columns show, in which order and how wide. It is kept per device
 * class (phone, tablet, desktop) rather than per device, saved to the user's account (a small
 * `list.<class>` preference) so it follows them, and applies to every project and to All Tasks.
 *
 * On desktop, the table (List) and the stacked Cards view are visually distinct enough that their
 * column/field choices are kept independent: List stays under the original `list.desktop` key and
 * Cards gets its own `cards.desktop` key. Phone and tablet never show a real column table (their
 * "List" is the same compact row list `CompactList` renders for both view modes), so there is only
 * ever one set of fields to configure there, still saved under `list.<class>`.
 */
import { api, clientId } from './api';

export type DeviceClass = 'phone' | 'tablet' | 'desktop';
export type ColumnId = 'ref' | 'title' | 'project' | 'status' | 'priority' | 'type' | 'sequence' | 'labels' | 'due' | 'created' | 'updated' | 'completed';

export interface ColumnDef {
  id: ColumnId;
  label: string;
  /** Default width in px; null = flexible (takes the remaining space, at least 220 px). */
  width: number | null;
  min: number;
  /** API sort field, or '' when the column can't be sorted. */
  sort: string;
  note?: string;
  /** Shown only once the user turns it on in the Columns menu. */
  hiddenByDefault?: boolean;
}

export const COLUMNS: ColumnDef[] = [
  { id: 'ref', label: 'ID', width: 84, min: 56, sort: '' },
  { id: 'title', label: 'Title', width: null, min: 120, sort: 'title' },
  { id: 'project', label: 'Project', width: 170, min: 90, sort: 'project' },
  { id: 'status', label: 'Status', width: 132, min: 90, sort: 'status' },
  { id: 'priority', label: 'Priority', width: 112, min: 80, sort: 'priority' },
  { id: 'type', label: 'Type', width: 104, min: 76, sort: 'type' },
  { id: 'sequence', label: 'Sequence', width: 92, min: 72, sort: 'sequence' },
  { id: 'labels', label: 'Labels', width: 200, min: 90, sort: '' },
  { id: 'due', label: 'Due', width: 96, min: 72, sort: 'due_date' },
  { id: 'created', label: 'Created', width: 168, min: 80, sort: 'created_at', hiddenByDefault: true },
  { id: 'updated', label: 'Updated', width: 168, min: 80, sort: 'updated_at', hiddenByDefault: true },
  { id: 'completed', label: 'Completed', width: 168, min: 80, sort: 'completed_at', hiddenByDefault: true },
];
const DEF = new Map(COLUMNS.map((c) => [c.id, c]));
export const MAX_WIDTH = 900;
export const FLEX_MIN = 220;

export interface StoredColumn {
  id: ColumnId;
  w?: number; // custom width in px
  hidden?: boolean;
}

export type ViewMode = 'list' | 'cards';

/** The preference key a device class + view mode's column choices are saved under (see the module doc comment). */
function prefKey(cls: DeviceClass, mode: ViewMode): string {
  return cls === 'desktop' ? `${mode}.desktop` : `list.${cls}`;
}
const PREF_KEYS = ['list.phone', 'list.tablet', 'list.desktop', 'cards.desktop'];

export const layout = $state({
  prefs: {} as Partial<Record<string, StoredColumn[]>>, // keyed by prefKey(cls, mode)
  modes: {} as Partial<Record<DeviceClass, ViewMode>>,
  viewport: typeof window === 'undefined' ? 1280 : window.innerWidth,
});

export const hooks = { onSaveError: (_message: string) => {} };

export function classFor(width: number): DeviceClass {
  return width <= 700 ? 'phone' : width <= 1100 ? 'tablet' : 'desktop';
}
export const deviceLabel: Record<DeviceClass, string> = { phone: 'Phone', tablet: 'Tablet', desktop: 'Desktop' };
export const currentClass = (): DeviceClass => classFor(layout.viewport);

if (typeof window !== 'undefined') {
  let raf = 0;
  window.addEventListener('resize', () => {
    cancelAnimationFrame(raf);
    raf = requestAnimationFrame(() => (layout.viewport = window.innerWidth));
  });
}

/** Turn whatever was stored into a complete, valid list: unknown ids dropped, new ones appended, widths clamped. */
export function sanitize(raw: unknown): StoredColumn[] {
  const seen = new Set<ColumnId>();
  const out: StoredColumn[] = [];
  if (Array.isArray(raw)) {
    for (const item of raw) {
      const id = (item as StoredColumn)?.id;
      const def = DEF.get(id);
      if (!def || seen.has(id)) continue;
      seen.add(id);
      const col: StoredColumn = { id };
      const w = Number((item as StoredColumn).w);
      if (Number.isFinite(w) && w > 0) col.w = Math.round(Math.min(MAX_WIDTH, Math.max(def.min, w)));
      if ((item as StoredColumn).hidden === true) col.hidden = true;
      out.push(col);
    }
  }
  for (const def of COLUMNS) if (!seen.has(def.id)) out.push(def.hiddenByDefault ? { id: def.id, hidden: true } : { id: def.id });
  // A task must always be openable: keep at least the ID or the Title visible.
  if (out.find((c) => c.id === 'ref')?.hidden && out.find((c) => c.id === 'title')?.hidden) delete out.find((c) => c.id === 'title')!.hidden;
  return out;
}

export function initLayout(prefs: Record<string, unknown>) {
  for (const cls of ['phone', 'tablet', 'desktop'] as DeviceClass[]) {
    const mode = prefs[`view.${cls}`];
    if (mode === 'list' || mode === 'cards') layout.modes[cls] = mode;
    else delete layout.modes[cls];
  }
  for (const key of PREF_KEYS) {
    const stored = (prefs[key] as { cols?: unknown } | undefined)?.cols;
    if (stored) layout.prefs[key] = sanitize(stored);
    else delete layout.prefs[key];
  }
}

export function resetLayoutState() {
  layout.prefs = {};
  layout.modes = {};
}

/** Table or cards. Phones default to cards (a wide table needs sideways scrolling there); larger screens to the table. */
export function currentMode(): ViewMode {
  const cls = currentClass();
  return layout.modes[cls] ?? (cls === 'phone' ? 'cards' : 'list');
}

/** The full ordered column list (hidden ones included) for a device class + view mode. */
export function ordered(cls: DeviceClass = currentClass(), mode: ViewMode = currentMode()): StoredColumn[] {
  return layout.prefs[prefKey(cls, mode)] ?? sanitize(undefined);
}

export interface ResolvedColumn extends ColumnDef {
  /** px width in effect, or null for the flexible column */
  current: number | null;
}

/** The visible columns, in order, with their widths, for the current device class + view mode. */
export function visibleColumns(showProject: boolean, mode: ViewMode = currentMode()): ResolvedColumn[] {
  const out: ResolvedColumn[] = [];
  for (const c of ordered(currentClass(), mode)) {
    const def = DEF.get(c.id)!;
    if (c.hidden || (c.id === 'project' && !showProject)) continue;
    out.push({ ...def, current: c.w ?? def.width });
  }
  // If everything visible was project-only, fall back to the defaults rather than an empty table.
  return out.length ? out : COLUMNS.filter((d) => d.id !== 'project').map((d) => ({ ...d, current: d.width }));
}

export function canHide(id: ColumnId, cls: DeviceClass = currentClass(), mode: ViewMode = currentMode()): boolean {
  const list = ordered(cls, mode);
  if (id !== 'ref' && id !== 'title') return true;
  const other = list.find((c) => c.id === (id === 'ref' ? 'title' : 'ref'));
  return !!other && !other.hidden;
}

let timer: ReturnType<typeof setTimeout> | undefined;
let dirty = new Set<string>(); // preference keys waiting to be saved: list.<class>, cards.desktop and view.<class>

function bodyFor(keys: string[]): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  for (const key of keys) {
    if (key.startsWith('view.')) {
      const cls = key.split('.')[1] as DeviceClass;
      body[key] = layout.modes[cls] ?? null;
    } else {
      body[key] = layout.prefs[key] ? { cols: $state.snapshot(layout.prefs[key]) } : null;
    }
  }
  return body;
}

function save() {
  clearTimeout(timer);
  timer = setTimeout(async () => {
    const keys = [...dirty];
    dirty = new Set();
    if (!keys.length) return;
    try {
      await api('PATCH', '/api/v1/me/preferences', bodyFor(keys));
    } catch (e) {
      hooks.onSaveError(e instanceof Error ? e.message : 'Could not save the column layout');
    }
  }, 500);
}

/** The save above is debounced 500ms; a pending change would otherwise be lost if the page is closed or
 * navigated away from before that timer fires (this bit the desktop Columns/Fields reset in practice, not
 * just in theory: real timing in the app can outrun a 500ms window). Mirrors flushPendingDeletes in store.svelte.ts. */
function flushPending() {
  if (!dirty.size) return;
  clearTimeout(timer);
  const keys = [...dirty];
  dirty = new Set();
  void fetch('/api/v1/me/preferences', {
    method: 'PATCH', keepalive: true, credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-LB-CSRF': '1', 'X-LB-Client': clientId },
    body: JSON.stringify(bodyFor(keys)),
  }).catch(() => {});
}
if (typeof window !== 'undefined') window.addEventListener('pagehide', flushPending);

function change(cls: DeviceClass, mode: ViewMode, fn: (cols: StoredColumn[]) => void) {
  const key = prefKey(cls, mode);
  const cols = $state.snapshot(ordered(cls, mode)) as StoredColumn[];
  fn(cols);
  layout.prefs[key] = sanitize(cols);
  dirty.add(key);
  save();
}

export function setWidth(id: ColumnId, width: number, cls: DeviceClass = currentClass(), mode: ViewMode = currentMode()) {
  const def = DEF.get(id)!;
  const w = Math.round(Math.min(MAX_WIDTH, Math.max(def.min, width)));
  change(cls, mode, (cols) => (cols.find((c) => c.id === id)!.w = w));
}

export function resetWidth(id: ColumnId, cls: DeviceClass = currentClass(), mode: ViewMode = currentMode()) {
  change(cls, mode, (cols) => delete cols.find((c) => c.id === id)!.w);
}

export function toggleColumn(id: ColumnId, cls: DeviceClass = currentClass(), mode: ViewMode = currentMode()) {
  if (!canHide(id, cls, mode) && !ordered(cls, mode).find((c) => c.id === id)?.hidden) return;
  change(cls, mode, (cols) => {
    const c = cols.find((x) => x.id === id)!;
    if (c.hidden) delete c.hidden;
    else c.hidden = true;
  });
}

/** `skip` marks ids that are hidden from the menu doing the reordering (e.g. "Project" in a project's own Columns
 * menu) so a move steps over them instead of swapping into a slot the user can't see. */
export function moveColumn(id: ColumnId, dir: -1 | 1, cls: DeviceClass = currentClass(), mode: ViewMode = currentMode(), skip?: (id: ColumnId) => boolean) {
  change(cls, mode, (cols) => {
    const i = cols.findIndex((c) => c.id === id);
    if (i < 0) return;
    let j = i + dir;
    while (j >= 0 && j < cols.length && skip?.(cols[j].id)) j += dir;
    if (j < 0 || j >= cols.length) return;
    [cols[i], cols[j]] = [cols[j], cols[i]];
  });
}

/** Move a column to an absolute position among the *visible* (non-skipped) columns (used when it is dragged by
 * its handle in the Columns menu); skipped columns keep their place relative to their nearest visible neighbour. */
export function moveColumnTo(id: ColumnId, index: number, cls: DeviceClass = currentClass(), mode: ViewMode = currentMode(), skip?: (id: ColumnId) => boolean) {
  change(cls, mode, (cols) => {
    const visible = (arr: StoredColumn[]) => arr.map((c, i) => ({ c, i })).filter(({ c }) => !skip?.(c.id));
    const before = visible(cols);
    const from = before.findIndex(({ c }) => c.id === id);
    if (from < 0) return;
    const to = Math.min(before.length - 1, Math.max(0, index));
    if (from === to) return;
    const [item] = cols.splice(before[from].i, 1);
    const to2 = to > from ? to - 1 : to;
    const after = visible(cols);
    const insertAt = to2 >= after.length ? cols.length : after[to2].i;
    cols.splice(insertAt, 0, item);
  });
}

export function resetLayout(cls: DeviceClass = currentClass(), mode: ViewMode = currentMode()) {
  const key = prefKey(cls, mode);
  delete layout.prefs[key];
  dirty.add(key);
  save();
}

export function setMode(mode: ViewMode, cls: DeviceClass = currentClass()) {
  layout.modes[cls] = mode;
  dirty.add(`view.${cls}`);
  save();
}

export function isCustomised(cls: DeviceClass = currentClass(), mode: ViewMode = currentMode()): boolean {
  return !!layout.prefs[prefKey(cls, mode)];
}
