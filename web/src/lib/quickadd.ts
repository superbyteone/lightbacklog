import type { Label, Priority, Project, Status } from './types';

export interface QuickAdd {
  title: string;
  project?: Project;
  priority?: string;
  status?: string;
  labels: string[];
  /** Tokens that were recognised, for showing what will be applied. */
  matched: { token: string; kind: 'project' | 'priority' | 'status' | 'label'; text: string }[];
}

/**
 * Pull `#project`, `!priority`, `/status` and `@label` shortcuts out of a title.
 * A token only counts when it matches something that exists, so ordinary text such as
 * "email @bob" or "issue #12" stays in the title.
 */
export function parseQuickAdd(raw: string, ctx: { projects: Project[]; priorities: Priority[]; statuses: Status[]; labels: Label[] }): QuickAdd {
  const out: QuickAdd = { title: '', labels: [], matched: [] };
  const kept: string[] = [];
  for (const word of raw.split(/\s+/).filter(Boolean)) {
    const sigil = word[0];
    const rest = word.slice(1).toLowerCase();
    let hit = false;
    if (rest && sigil === '#') {
      const p = ctx.projects.find((p) => !p.archived && p.role !== 'viewer' && (p.key.toLowerCase() === rest || p.name.toLowerCase().replace(/\s+/g, '-') === rest));
      if (p) (out.project = p), out.matched.push({ token: word, kind: 'project', text: p.name }), (hit = true);
    } else if (rest && sigil === '!') {
      const p = ctx.priorities.find((p) => p.slug === rest || p.name.toLowerCase() === rest || p.slug[0] === rest);
      if (p) (out.priority = p.slug), out.matched.push({ token: word, kind: 'priority', text: p.name }), (hit = true);
    } else if (rest && sigil === '/') {
      const norm = rest.replace(/-/g, '_');
      const s = ctx.statuses.find((s) => s.slug === norm || s.name.toLowerCase().replace(/\s+/g, '') === norm.replace(/_/g, '') || (norm === 'doing' && s.slug === 'in_progress'));
      if (s) (out.status = s.slug), out.matched.push({ token: word, kind: 'status', text: s.name }), (hit = true);
    } else if (rest && sigil === '@') {
      const l = ctx.labels.find((l) => l.name.toLowerCase().replace(/\s+/g, '-') === rest);
      if (l) {
        if (!out.labels.includes(l.name)) out.labels.push(l.name);
        out.matched.push({ token: word, kind: 'label', text: l.name });
        hit = true;
      }
    }
    if (!hit) kept.push(word);
  }
  out.title = kept.join(' ');
  return out;
}
