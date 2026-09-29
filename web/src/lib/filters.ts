import type { Status } from './types';

/** Filter and sort state, serialized in the URL query string. */
export interface Filters {
  project: string[];
  status: string[];
  priority: string[];
  type: string[];
  label: string[];
  q: string;
  done: boolean;
  sort: string;
}

export const DEFAULT_SORT = '-updated_at';

export function parseFilters(p: URLSearchParams): Filters {
  const all = (k: string) => p.getAll(k).flatMap((v) => v.split(',')).map((v) => v.trim()).filter(Boolean);
  return {
    project: all('project'),
    status: all('status'),
    priority: all('priority'),
    type: all('type'),
    label: all('label'),
    q: p.get('q') ?? '',
    done: p.get('done') === '1',
    sort: p.get('sort') || DEFAULT_SORT,
  };
}

/** Write filters into a URLSearchParams, leaving unrelated params (like `task` or `view`) alone. */
export function writeFilters(p: URLSearchParams, f: Partial<Filters>) {
  const setMulti = (k: string, v?: string[]) => {
    if (v === undefined) return;
    p.delete(k);
    if (v.length) p.set(k, v.join(','));
  };
  setMulti('project', f.project);
  setMulti('status', f.status);
  setMulti('priority', f.priority);
  setMulti('type', f.type);
  setMulti('label', f.label);
  if (f.q !== undefined) f.q ? p.set('q', f.q) : p.delete('q');
  if (f.done !== undefined) f.done ? p.set('done', '1') : p.delete('done');
  if (f.sort !== undefined) f.sort && f.sort !== DEFAULT_SORT ? p.set('sort', f.sort) : p.delete('sort');
}

export function isFiltered(f: Filters, lockedProject = false): boolean {
  return (!lockedProject && f.project.length > 0) || f.status.length > 0 || f.priority.length > 0 || f.type.length > 0 || f.label.length > 0 || f.q !== '' || f.done;
}

/** Translate UI filters to API query parameters. Done tasks are hidden unless asked for. */
export function toApiParams(f: Filters, statuses: Status[], lockedProject?: string): string {
  const q = new URLSearchParams();
  const projects = lockedProject ? [lockedProject] : f.project;
  if (projects.length) q.set('project', projects.join(','));
  if (f.status.length) q.set('status', f.status.join(','));
  else if (!f.done) {
    const open = statuses.filter((s) => !s.is_done).map((s) => s.slug);
    if (open.length) q.set('status', open.join(','));
  }
  if (f.priority.length) q.set('priority', f.priority.join(','));
  if (f.type.length) q.set('type', f.type.join(','));
  if (f.label.length) q.set('label', f.label.join(','));
  if (f.q.trim()) q.set('q', f.q.trim());
  q.set('sort', f.q.trim() && f.sort === DEFAULT_SORT ? 'relevance' : f.sort);
  return q.toString();
}

export function formatDue(d: string | null): string {
  if (!d) return '';
  const [y, m, day] = d.split('-').map(Number);
  const date = new Date(y, m - 1, day);
  const opts: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric', year: 'numeric' };
  return date.toLocaleDateString(undefined, opts);
}

export function todayISO(offsetDays = 0): string {
  const d = new Date();
  d.setDate(d.getDate() + offsetDays);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}
