import { ApiError, api, clientId, get, post, setUnauthorizedHandler } from './api';
import { hooks, initLayout, resetLayoutState } from './layout.svelte';
import type { Label, Me, Priority, Project, Status, Task, TaskPatch, TaskType } from './types';

export const app = $state({
  ready: false,
  me: null as Me | null,
  projects: [] as Project[],
  statuses: [] as Status[],
  priorities: [] as Priority[],
  types: [] as TaskType[],
  labels: [] as Label[],
});

export interface Toast {
  id: number;
  kind: 'info' | 'error' | 'success';
  text: string;
  action?: { label: string; run: () => void };
}
export const toasts = $state<Toast[]>([]);
let toastSeq = 0;

export function toast(text: string, kind: Toast['kind'] = 'info', action?: Toast['action'], ms?: number) {
  const t: Toast = { id: ++toastSeq, kind, text, action };
  toasts.push(t);
  setTimeout(() => dismissToast(t.id), ms ?? (kind === 'error' || action ? 8000 : 3500));
  return t.id;
}

hooks.onSaveError = (m) => toast(`Couldn't save your column layout: ${m}`, 'error');

export function dismissToast(id: number) {
  const i = toasts.findIndex((t) => t.id === id);
  if (i >= 0) toasts.splice(i, 1);
}

setUnauthorizedHandler(() => {
  app.me = null;
  disconnectEvents();
});

// --- bootstrap ------------------------------------------------------------------------

export async function bootstrap() {
  try {
    const me = await get<Me>('/api/v1/me');
    app.me = me.data;
    await loadReferenceData();
    connectEvents();
  } catch (e) {
    if (!(e instanceof ApiError && e.status === 401)) toast(errorText(e), 'error');
    app.me = null;
  } finally {
    app.ready = true;
  }
}

export async function loadReferenceData() {
  const [meta, labels, prefs] = await Promise.all([
    get<{ statuses: Status[]; priorities: Priority[]; types: TaskType[] }>('/api/v1/meta'),
    get<Label[]>('/api/v1/labels'),
    get<Record<string, unknown>>('/api/v1/me/preferences').catch(() => ({ data: {} as Record<string, unknown> })),
  ]);
  initLayout(prefs.data);
  app.statuses = meta.data.statuses;
  app.priorities = meta.data.priorities;
  app.types = meta.data.types ?? [];
  app.labels = labels.data;
  await refreshProjects();
}

export async function refreshProjects() {
  const r = await get<Project[]>('/api/v1/projects');
  app.projects = r.data;
}

/** Stars or unstars a project for the caller; a personal preference, so any member may set it. */
export async function setProjectFavorite(project: Project, favorite: boolean) {
  const r = await post<Project>(`/api/v1/projects/${project.id}/${favorite ? 'favorite' : 'unfavorite'}`);
  const i = app.projects.findIndex((p) => p.id === project.id);
  if (i >= 0) app.projects[i] = r.data;
}

export async function refreshLabels() {
  app.labels = (await get<Label[]>('/api/v1/labels')).data;
}

export async function login(username: string, password: string) {
  const r = await api<Me>('POST', '/api/v1/auth/login', { username, password });
  app.me = { ...r.data, auth: 'session' };
  await loadReferenceData();
  connectEvents();
}

export async function logout() {
  disconnectEvents();
  try {
    await post('/api/v1/auth/logout');
  } finally {
    app.me = null;
    app.projects = [];
    resetLayoutState();
    for (const k of Object.keys(cache)) delete cache[k];
  }
}

export function errorText(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.fields.length) return e.fields.map((f) => `${f.field}: ${f.message}`).join('; ');
    return e.message;
  }
  return e instanceof Error ? e.message : String(e);
}

// --- lookups --------------------------------------------------------------------------

export const statusBySlug = (slug: string) => app.statuses.find((s) => s.slug === slug);
export const priorityBySlug = (slug: string) => app.priorities.find((p) => p.slug === slug);
export const projectByKey = (k: string) => app.projects.find((p) => p.key.toLowerCase() === k.toLowerCase());
export const projectById = (id: string) => app.projects.find((p) => p.id === id);

// --- task cache & updates -------------------------------------------------------------

/** Normalized tasks by id. Every view reads from here, so an edit in one view shows up in all others. */
export const cache: Record<string, Task> = $state({});

export function remember(tasks: Task[]) {
  for (const t of tasks) {
    const prev = cache[t.id];
    // List responses omit descriptions; keep one we already loaded if the version matches.
    if (t.description === undefined && prev?.description !== undefined && prev.version === t.version) t.description = prev.description;
    cache[t.id] = t;
  }
}

/** Views register a callback so they can refresh after creates, deletes and moves. */
const listeners = new Set<() => void>();
export function onTasksChanged(fn: () => void) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}
export function notifyTasksChanged() {
  listeners.forEach((fn) => fn());
  refreshProjects().catch(() => {});
}

function applyOptimistic(t: Task, patch: TaskPatch) {
  if (patch.title !== undefined) t.title = patch.title;
  if (patch.description !== undefined) t.description = patch.description;
  if (patch.status !== undefined) t.status = patch.status;
  if (patch.priority !== undefined) t.priority = patch.priority;
  if (patch.due_date !== undefined) t.due_date = patch.due_date;
  if (patch.position !== undefined) t.position = patch.position;
  if (patch.sequence !== undefined) t.sequence = patch.sequence;
  if (patch.type !== undefined) t.type = patch.type;
  if (patch.add_labels || patch.remove_labels) {
    const drop = new Set((patch.remove_labels ?? []).map((n) => n.toLowerCase()));
    const names = t.labels.map((l) => l.name).filter((n) => !drop.has(n.toLowerCase()));
    for (const n of patch.add_labels ?? []) if (!names.some((x) => x.toLowerCase() === n.toLowerCase())) names.push(n);
    patch = { ...patch, labels: names };
  }
  if (patch.labels !== undefined) {
    t.labels = patch.labels.map((n) => app.labels.find((l) => l.name.toLowerCase() === n.toLowerCase()) ?? { id: 'pending:' + n, name: n, color: '#6b7280' });
  }
  if (patch.project !== undefined) {
    const p = projectByKey(patch.project) ?? projectById(patch.project);
    if (p) t.project = { id: p.id, key: p.key, name: p.name, color: p.color };
  }
}

const queues = new Map<string, Promise<unknown>>();
const versions = new Map<string, number>(); // last version confirmed by the server

/**
 * Save changes to a task immediately: apply them to the UI at once, send them in order,
 * and on failure restore the previous values and offer a retry. Returns whether it saved.
 */
export function patchTask(id: string, patch: TaskPatch, opts: { silent?: boolean } = {}): Promise<boolean> {
  const t = cache[id];
  if (!t) return Promise.resolve(false);
  const before = $state.snapshot(t) as Task;
  applyOptimistic(t, patch);
  const run = async (): Promise<boolean> => {
    const cur = cache[id];
    try {
      const body: Record<string, unknown> = { ...patch };
      const v = versions.get(id) ?? before.version;
      body.version = v;
      const r = await api<Task>('PATCH', `/api/v1/tasks/${id}`, body);
      const keepDesc = r.data.description === undefined ? cache[id]?.description : undefined;
      cache[id] = r.data;
      if (keepDesc !== undefined) cache[id].description = keepDesc;
      versions.set(id, r.data.version);
      const named = [...(patch.labels ?? []), ...(patch.add_labels ?? [])];
      if (named.some((n) => !app.labels.some((l) => l.name.toLowerCase() === n.toLowerCase()))) refreshLabels().catch(() => {});
      if (patch.project !== undefined || patch.status !== undefined || patch.due_date !== undefined) refreshProjects().catch(() => {});
      return true;
    } catch (e) {
      if (e instanceof ApiError && e.code === 'version_conflict' && e.current) {
        cache[id] = e.current as Task;
        versions.set(id, (e.current as Task).version);
        if (!opts.silent) toast('This task was changed elsewhere. It has been reloaded; please redo your edit.', 'error');
        return false;
      }
      if (cur) {
        const rollback: TaskPatch = {};
        for (const k of Object.keys(patch) as (keyof TaskPatch)[]) {
          if (k === 'labels' || k === 'add_labels' || k === 'remove_labels') rollback.labels = before.labels.map((l) => l.name);
          else (rollback as any)[k] = k === 'project' ? before.project.id : (before as any)[k];
        }
        applyOptimistic(cur, rollback);
      }
      if (!opts.silent) toast(`Couldn't save: ${errorText(e)}`, 'error', { label: 'Retry', run: () => void patchTask(id, patch) });
      return false;
    }
  };
  const next = (queues.get(id) ?? Promise.resolve()).then(run, run);
  queues.set(id, next);
  return next as Promise<boolean>;
}

export async function loadTask(ref: string): Promise<Task> {
  const r = await get<Task>(`/api/v1/tasks/${encodeURIComponent(ref)}`);
  remember([r.data]);
  versions.set(r.data.id, r.data.version);
  return cache[r.data.id];
}

export interface NewTask {
  project: string;
  title: string;
  description?: string;
  status?: string;
  priority?: string;
  due_date?: string | null;
  labels?: string[];
  type?: string | null;
}

export async function createTask(input: NewTask): Promise<Task> {
  const r = await post<Task>('/api/v1/tasks', input);
  remember([r.data]);
  versions.set(r.data.id, r.data.version);
  if (input.labels?.length) refreshLabels().catch(() => {});
  notifyTasksChanged();
  return r.data;
}

// --- bulk edits -----------------------------------------------------------------------

/** Can the current user change this task (not a viewer, project not archived)? */
export function canEditTask(t: Task): boolean {
  const p = projectById(t.project.id);
  return !!p && p.role !== 'viewer' && !p.archived;
}

function inverseOf(t: Task, patch: TaskPatch): TaskPatch {
  const inv: TaskPatch = {};
  if ('status' in patch) inv.status = t.status;
  if ('priority' in patch) inv.priority = t.priority;
  if ('due_date' in patch) inv.due_date = t.due_date;
  if ('sequence' in patch) inv.sequence = t.sequence;
  if ('type' in patch) inv.type = t.type;
  if ('project' in patch) inv.project = t.project.id;
  if ('labels' in patch || 'add_labels' in patch || 'remove_labels' in patch) inv.labels = t.labels.map((l) => l.name);
  return inv;
}

/**
 * Apply one change to many tasks. They update on screen at once, failures are counted instead of
 * producing one error each, and a single Undo puts every changed task back.
 */
export async function bulkPatch(tasks: Task[], patch: TaskPatch, what: string): Promise<void> {
  const editable = tasks.filter(canEditTask);
  const skipped = tasks.length - editable.length;
  const before = editable.map((t) => ({ id: t.id, inverse: inverseOf($state.snapshot(t) as Task, patch) }));
  const results = await Promise.all(editable.map((t) => patchTask(t.id, patch, { silent: true })));
  const done = before.filter((_, i) => results[i]);
  const failed = editable.length - done.length;
  const n = (x: number) => `${x} task${x === 1 ? '' : 's'}`;
  let text = `${what}: ${n(done.length)} updated`;
  if (failed) text += `, ${failed} failed`;
  if (skipped) text += `, ${skipped} skipped (view-only)`;
  if (!done.length) return void toast(text, 'error');
  toast(text, failed ? 'error' : 'success', {
    label: 'Undo',
    run: async () => {
      const back = await Promise.all(done.map((d) => patchTask(d.id, d.inverse, { silent: true })));
      toast(back.every(Boolean) ? `Undone: ${n(done.length)} restored` : 'Some changes could not be undone', back.every(Boolean) ? 'success' : 'error');
    },
  }, UNDO_MS);
}

// --- deleting with Undo ---------------------------------------------------------------

/** How long a delete can be undone. The request is only sent after this, so Undo needs no server support. */
export const UNDO_MS = 10_000;

const pendingIds = new Set<string>();
const batches = new Map<number, { tasks: Task[]; timer: ReturnType<typeof setTimeout>; toastId: number }>();
let batchSeq = 0;

export const isPendingDelete = (id: string) => pendingIds.has(id);

/**
 * Delete tasks with a 10-second Undo. They vanish from every view at once; the DELETE requests are
 * sent when the time is up (or when the page is closed) unless Undo was pressed.
 */
export function deleteTasksWithUndo(tasks: Task[]) {
  if (!tasks.length) return;
  const snapshot = tasks.map((t) => $state.snapshot(t) as Task); // copy first: the cache entries are removed below
  const ids = new Set(snapshot.map((t) => t.id));
  for (const id of ids) {
    pendingIds.add(id);
    delete cache[id];
  }
  allLists.forEach((l) => l.removeIds(ids));
  const key = ++batchSeq;
  const label = snapshot.length === 1 ? `Deleted ${snapshot[0].ref}` : `Deleted ${snapshot.length} tasks`;
  const toastId = toast(label, 'info', { label: 'Undo', run: () => undoDelete(key) }, UNDO_MS);
  batches.set(key, { tasks: snapshot, timer: setTimeout(() => void commitDelete(key), UNDO_MS), toastId });
}

function undoDelete(key: number) {
  const b = batches.get(key);
  if (!b) return;
  clearTimeout(b.timer);
  batches.delete(key);
  for (const t of b.tasks) pendingIds.delete(t.id);
  remember(b.tasks);
  for (const t of b.tasks) versions.set(t.id, t.version);
  notifyTasksChanged();
  toast(b.tasks.length === 1 ? `Restored ${b.tasks[0].ref}` : `Restored ${b.tasks.length} tasks`, 'success');
}

async function commitDelete(key: number) {
  const b = batches.get(key);
  if (!b) return;
  batches.delete(key);
  const failed: Task[] = [];
  let firstError: unknown;
  for (const t of b.tasks) {
    try {
      await api('DELETE', `/api/v1/tasks/${t.id}`);
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 404)) {
        failed.push(t);
        firstError ??= e;
      }
    }
    pendingIds.delete(t.id);
  }
  if (failed.length) {
    remember(failed); // the delete did not happen: show the tasks again
    toast(`Couldn't delete ${failed.length === 1 ? failed[0].ref : failed.length + ' tasks'}: ${errorText(firstError)}`, 'error');
  }
  notifyTasksChanged();
}

/** When the page is closed or hidden, send any deletes that are still waiting so they aren't lost. */
export function flushPendingDeletes() {
  for (const [key, b] of [...batches]) {
    clearTimeout(b.timer);
    batches.delete(key);
    for (const t of b.tasks) {
      pendingIds.delete(t.id);
      void fetch(`/api/v1/tasks/${t.id}`, { method: 'DELETE', keepalive: true, credentials: 'same-origin', headers: { 'X-LB-CSRF': '1', 'X-LB-Client': clientId } }).catch(() => {});
    }
  }
}
if (typeof window !== 'undefined') window.addEventListener('pagehide', flushPendingDeletes);

// --- task lists -----------------------------------------------------------------------

/** A server-side filtered, sorted, paginated list of task ids that loads pages on demand. */
const allLists = new Set<TaskList>();

export class TaskList {
  ids = $state<string[]>([]);
  total = $state<number | null>(null);
  loading = $state(false);
  error = $state<string | null>(null);
  hasMore = $state(false);
  private next: string | undefined;
  private params = '';
  private ctl: AbortController | null = null;
  private generation = 0;

  constructor(private pageSize = 100) {
    allLists.add(this);
  }

  /** Drop ids from this list immediately (used when tasks are deleted with Undo). */
  removeIds(ids: Set<string>) {
    const before = this.ids.length;
    this.ids = this.ids.filter((id) => !ids.has(id));
    if (this.total !== null) this.total = Math.max(0, this.total - (before - this.ids.length));
  }

  async reset(params: string) {
    this.params = params;
    this.next = undefined;
    await this.fetchPage(true);
  }

  refresh() {
    return this.reset(this.params);
  }

  async more() {
    if (this.loading || !this.hasMore) return;
    await this.fetchPage(false);
  }

  private async fetchPage(replace: boolean) {
    this.ctl?.abort();
    const ctl = (this.ctl = new AbortController());
    const gen = ++this.generation;
    this.loading = true;
    this.error = null;
    const q = new URLSearchParams(this.params);
    q.set('limit', String(this.pageSize));
    if (replace) q.set('include_total', 'true');
    else if (this.next) q.set('cursor', this.next);
    try {
      const r = await get<Task[]>('/api/v1/tasks?' + q.toString(), ctl.signal);
      if (gen !== this.generation) return;
      const rows = r.data.filter((t) => !pendingIds.has(t.id)); // tasks waiting for their delete stay hidden
      remember(rows);
      for (const t of rows) versions.set(t.id, t.version);
      const ids = rows.map((t) => t.id);
      this.ids = replace ? ids : [...this.ids, ...ids.filter((id) => !this.ids.includes(id))];
      if (r.total !== undefined) this.total = r.total;
      this.next = r.next_cursor;
      this.hasMore = !!r.next_cursor;
    } catch (e) {
      if ((e as Error).name === 'AbortError') return;
      this.error = errorText(e);
    } finally {
      if (gen === this.generation) this.loading = false;
    }
  }

  destroy() {
    allLists.delete(this);
    this.ctl?.abort();
    this.generation++;
  }
}

// --- live updates ---------------------------------------------------------------------

interface LiveEvent {
  type: string;
  task_id?: string;
  origin?: string;
}

let source: EventSource | null = null;
let connectedBefore = false;
let liveTimer: ReturnType<typeof setTimeout> | undefined;
const liveChanged = new Set<string>();
const liveDeleted = new Set<string>();
let liveResync = false;

/** Listen for changes made elsewhere (other tabs, users, AI agents) and refresh what is on screen. */
export function connectEvents() {
  if (source || typeof EventSource === 'undefined') return;
  source = new EventSource('/api/v1/events');
  const onEvent = (e: MessageEvent) => {
    let ev: LiveEvent;
    try {
      ev = JSON.parse(e.data);
    } catch {
      return;
    }
    if (ev.origin === clientId) return; // our own change; already applied optimistically
    if (ev.type === 'task.updated' && ev.task_id) liveChanged.add(ev.task_id);
    else if (ev.type === 'task.deleted' && ev.task_id) liveDeleted.add(ev.task_id);
    else if (ev.type === 'resync' || ev.type === 'project.changed') liveResync = true;
    clearTimeout(liveTimer);
    liveTimer = setTimeout(applyLiveChanges, 250);
  };
  for (const t of ['task.created', 'task.updated', 'task.deleted', 'project.changed', 'resync']) source.addEventListener(t, onEvent);
  source.onopen = () => {
    // After a dropped connection we may have missed events: reload what is displayed.
    if (connectedBefore) {
      liveResync = true;
      clearTimeout(liveTimer);
      liveTimer = setTimeout(applyLiveChanges, 250);
    }
    connectedBefore = true;
  };
}

export function disconnectEvents() {
  source?.close();
  source = null;
  connectedBefore = false;
  clearTimeout(liveTimer);
  liveChanged.clear();
  liveDeleted.clear();
}

async function applyLiveChanges() {
  const changed = [...liveChanged].filter((id) => cache[id]);
  const deleted = [...liveDeleted];
  const resync = liveResync;
  liveChanged.clear();
  liveDeleted.clear();
  liveResync = false;
  for (const id of deleted) delete cache[id];
  await Promise.all(
    changed.map(async (id) => {
      try {
        const r = await get<Task>(`/api/v1/tasks/${id}`);
        if (!cache[id] || r.data.version > (versions.get(id) ?? 0)) {
          cache[id] = r.data;
          versions.set(id, r.data.version);
        }
      } catch {
        delete cache[id]; // gone or no longer accessible
      }
    }),
  );
  if (resync) {
    await Promise.all([refreshLabels(), refreshProjects()]).catch(() => {});
  }
  notifyTasksChanged();
}
