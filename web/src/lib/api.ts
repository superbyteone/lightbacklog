export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public fields: { field: string; message: string }[] = [],
    public current?: unknown,
  ) {
    super(message);
  }
}

export interface Envelope<T> {
  data: T;
  next_cursor?: string;
  total?: number;
}

/** Identifies this browser tab so it can ignore live-update events caused by its own changes. */
export const clientId = Math.random().toString(36).slice(2) + Date.now().toString(36);

let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

export async function api<T = unknown>(
  method: string,
  path: string,
  body?: unknown,
  init: { signal?: AbortSignal; headers?: Record<string, string> } = {},
): Promise<Envelope<T>> {
  const headers: Record<string, string> = { 'X-LB-CSRF': '1', 'X-LB-Client': clientId, ...init.headers };
  let payload: BodyInit | undefined;
  if (body instanceof FormData) {
    payload = body;
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  let res: Response;
  try {
    res = await fetch(path, { method, headers, body: payload, signal: init.signal, credentials: 'same-origin' });
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e;
    throw new ApiError(0, 'network_error', 'Cannot reach the server. Check your connection and retry.');
  }
  if (res.status === 204) return { data: undefined as T };
  const text = await res.text();
  let json: any = undefined;
  try {
    json = text ? JSON.parse(text) : undefined;
  } catch {
    /* non-JSON error page */
  }
  if (!res.ok) {
    if (res.status === 401 && path !== '/api/v1/auth/login') onUnauthorized();
    throw new ApiError(res.status, json?.code ?? 'error', json?.detail ?? `Request failed (${res.status})`, json?.errors ?? [], json?.current);
  }
  return json as Envelope<T>;
}

export const get = <T>(path: string, signal?: AbortSignal) => api<T>('GET', path, undefined, { signal });
export const post = <T>(path: string, body?: unknown) => api<T>('POST', path, body ?? {});
export const patch = <T>(path: string, body: unknown) => api<T>('PATCH', path, body);
export const del = (path: string) => api('DELETE', path);
