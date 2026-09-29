export type Role = 'viewer' | 'editor' | 'owner';

export interface Me {
  id: string;
  username: string;
  email: string;
  is_admin: boolean;
  auth: 'session' | 'token';
}

export interface Project {
  id: string;
  key: string;
  name: string;
  description: string;
  color: string;
  archived: boolean;
  role: Role;
  favorite: boolean;
  open_tasks: number;
  total_tasks: number;
  created_at: string;
  updated_at: string;
}

export interface TaskType {
  slug: string;
  name: string;
  position: number;
  color: string;
}

export interface Label {
  id: string;
  name: string;
  color: string;
}

export interface Status {
  slug: string;
  name: string;
  position: number;
  color: string;
  is_done: boolean;
}

export interface Priority {
  slug: string;
  name: string;
  position: number;
  color: string;
}

export interface TaskProject {
  id: string;
  key: string;
  name: string;
  color: string;
}

export interface Task {
  id: string;
  ref: string;
  number: number;
  project: TaskProject;
  title: string;
  description?: string;
  status: string;
  priority: string;
  due_date: string | null;
  labels: Label[];
  external_ref: string | null;
  version: number;
  position: number;
  /** Optional work order: 1 is tackled first. */
  sequence: number | null;
  /** Optional kind of task: improvement, feature or bug. */
  type: string | null;
  created_at: string;
  updated_at: string;
  /** When the task last arrived in a done status; null while it is not done. */
  completed_at: string | null;
}

/** Fields the UI can change on a task; labels are names, project is a key or id. */
export interface TaskPatch {
  title?: string;
  description?: string;
  status?: string;
  priority?: string;
  due_date?: string | null;
  labels?: string[];
  /** Add or remove single labels without replacing the whole set (used by bulk edits). */
  add_labels?: string[];
  remove_labels?: string[];
  project?: string;
  position?: number;
  sequence?: number | null;
  type?: string | null;
}

export interface ApiToken {
  id: string;
  name: string;
  prefix: string;
  scope: 'read' | 'write';
  project_ids: string[];
  created_at: string;
  last_used_at?: string;
  revoked_at?: string;
}

export interface Attachment {
  id: string;
  filename: string;
  mime: string;
  size: number;
  inline: boolean;
  url: string;
  markdown: string;
}
