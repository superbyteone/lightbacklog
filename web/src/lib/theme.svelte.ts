/** Colour theme: follow the system (auto) or force light/dark. Remembered on this device. */
export type Theme = 'auto' | 'light' | 'dark';

function read(): Theme {
  try {
    const t = localStorage.getItem('lb.theme');
    if (t === 'light' || t === 'dark') return t;
  } catch {
    /* storage unavailable */
  }
  return 'auto';
}

export const theme = $state({ value: read() });

export function setTheme(t: Theme) {
  theme.value = t;
  const root = document.documentElement;
  if (t === 'auto') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', t);
  try {
    if (t === 'auto') localStorage.removeItem('lb.theme');
    else localStorage.setItem('lb.theme', t);
  } catch {
    /* storage unavailable: the choice lasts until reload */
  }
}

export function cycleTheme() {
  setTheme(theme.value === 'auto' ? 'light' : theme.value === 'light' ? 'dark' : 'auto');
}

export const themeLabel: Record<Theme, string> = { auto: 'Auto', light: 'Light', dark: 'Dark' };
