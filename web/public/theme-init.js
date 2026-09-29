// Applies the saved colour theme before first paint (avoids a flash of the wrong theme).
// Kept as an external file because the page's Content-Security-Policy forbids inline scripts.
try {
  var t = localStorage.getItem('lb.theme');
  if (t === 'light' || t === 'dark') document.documentElement.setAttribute('data-theme', t);
} catch (e) { /* storage unavailable: follow the system setting */ }
