const COLLAPSED_KEY = 'lb.sidebarCollapsed';
/** A per-device convenience only, so storage that is missing or blocked just means the sidebar starts expanded. */
function readCollapsed(): boolean {
  try {
    return localStorage.getItem(COLLAPSED_KEY) === '1';
  } catch {
    return false;
  }
}

/** Global UI state: overlays that can be opened from anywhere. */
export const ui = $state({
  create: null as null | { title?: string },
  palette: false,
  newProject: false,
  projectSettings: null as string | null, // project key
  sidebar: false, // mobile drawer
  sidebarCollapsed: readCollapsed(), // desktop and tablet: the permanent sidebar folded down to a narrow strip
  panelPushed: false, // the task panel was opened by an in-app navigation (so "close" can go back)
});

export function setSidebarCollapsed(on: boolean) {
  ui.sidebarCollapsed = on;
  try {
    if (on) localStorage.setItem(COLLAPSED_KEY, '1');
    else localStorage.removeItem(COLLAPSED_KEY);
  } catch {
    /* storage unavailable */
  }
}

export function openCreate(opts: { title?: string } = {}) {
  ui.palette = false;
  ui.create = opts;
}

export function closeCreate() {
  ui.create = null;
}

// Remembered "recent projects" and "last project" were removed; drop what earlier versions stored.
try {
  localStorage.removeItem('lb.recentProjects');
  localStorage.removeItem('lb.lastProject');
} catch {
  /* storage unavailable */
}
