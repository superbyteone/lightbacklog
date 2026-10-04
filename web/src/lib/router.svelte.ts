/** Minimal History-API router. Filter state lives in the query string so it survives reloads and back/forward. */
export const loc = $state({ path: location.pathname, search: location.search });

function sync() {
  loc.path = location.pathname;
  loc.search = location.search;
}

window.addEventListener('popstate', sync);

export function navigate(to: string, opts: { replace?: boolean } = {}) {
  if (to === location.pathname + location.search) return;
  if (opts.replace) history.replaceState({}, '', to);
  else history.pushState({}, '', to);
  sync();
}

/** Change query parameters on the current path without adding history entries by default. */
export function setParams(mutate: (p: URLSearchParams) => void, opts: { replace?: boolean; path?: string } = { replace: true }) {
  const p = new URLSearchParams(location.search);
  mutate(p);
  const s = p.toString();
  navigate((opts.path ?? location.pathname) + (s ? '?' + s : ''), { replace: opts.replace ?? true });
}

/** Intercept plain left-clicks on same-origin anchors so navigation stays in-app. */
export function link(node: HTMLAnchorElement) {
  const handler = (e: MouseEvent) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const url = new URL(node.href, location.href);
    if (url.origin !== location.origin) return;
    e.preventDefault();
    navigate(url.pathname + url.search);
  };
  node.addEventListener('click', handler);
  return { destroy: () => node.removeEventListener('click', handler) };
}
