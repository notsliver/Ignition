import { useSyncExternalStore } from 'react';

/*
 * Interface preferences: stored on the account (so they follow you), cached in this browser (so
 * they apply before the first paint), and applied by setting attributes and variables on <html>.
 * Time display, refresh rates and the clock read them through the helpers below.
 */

export const DEFAULTS = {
  theme: 'system', preset: 'ignition', accent: '', contrast: 'normal', size: '100', motion: 'system',
  timeStyle: 'relative', clock: 'auto', dateFormat: 'auto', timezone: 'browser',
  startPage: 'dashboard', sidebar: 'expanded', refresh: 'normal', shortcuts: 'on', tableDensity: 'compact',
};

// Each preset is a whole palette, tuned separately for light and dark: page background, surfaces,
// borders, hover, text and accent. Status colours (success, warning, danger) stay the same in
// every preset so their meaning never changes. Ignition is the stylesheet's own palette.
const palette = (bg, surface, border, line, hover, text, text2, muted, icon, badge, ring, accent) =>
  ({ bg, surface, border, line, hover, text, 'text-2': text2, muted, icon, badge, 'active-ring': ring, accent });

export const PRESETS = {
  ignition: { label: 'Ignition', light: palette('#f7f6f4', '#ffffff', '#e6e5e2', '#e2e1dd', '#efeeeb', '#1d1f22', '#4b4e53', '#8d9095', '#5d6066', '#f1f0ee', '#e8e7e3', '#3c55cf'),
    dark: palette('#141517', '#1b1c1f', '#2a2b2f', '#2d2e32', '#202124', '#ececee', '#b4b6bb', '#7d8086', '#9a9ca2', '#26272b', '#303136', '#8fa2ff'), native: true },
  graphite: { label: 'Graphite', light: palette('#f3f4f6', '#ffffff', '#e0e2e7', '#dadde3', '#eaecef', '#1a1c20', '#464a52', '#848992', '#5a5f68', '#eceef1', '#e3e5e9', '#3f444c'),
    dark: palette('#0f1012', '#17181b', '#27292d', '#2b2d32', '#1e2023', '#e8e9ec', '#b0b3ba', '#7b7f88', '#9a9da5', '#222428', '#2c2e33', '#c3c7cf') },
  midnight: { label: 'Midnight', light: palette('#f2f4f9', '#ffffff', '#dde2ee', '#d7dcea', '#e8ecf5', '#141a2b', '#424b63', '#7d869e', '#5a647e', '#e9edf6', '#e0e5f0', '#2b4bb8'),
    dark: palette('#0a0e1a', '#10172a', '#1e2842', '#222d49', '#162036', '#e6ebf7', '#a9b4cf', '#6e7a99', '#8c98b8', '#162036', '#232e4a', '#7aa2ff') },
  ocean: { label: 'Ocean', light: palette('#f1f7f9', '#ffffff', '#d8e6eb', '#d1e0e6', '#e5eff3', '#13222a', '#3e535d', '#798e97', '#55707b', '#e6f0f4', '#dae7ed', '#0b7a9b'),
    dark: palette('#0b1518', '#111f24', '#1e3138', '#223740', '#17282e', '#e3eff3', '#a7bdc6', '#6e8a95', '#8da9b4', '#17282e', '#223740', '#5cc8e8') },
  forest: { label: 'Forest', light: palette('#f2f6f1', '#ffffff', '#dce5d9', '#d5dfd2', '#e8eee6', '#17211a', '#425044', '#7e8b7f', '#59675b', '#e9efe7', '#dfe6dc', '#2f7d4f'),
    dark: palette('#0e1410', '#141c16', '#232f25', '#273429', '#1a241c', '#e4ece5', '#adbbaf', '#748579', '#92a496', '#1a241c', '#273429', '#6fd39a') },
  sand: { label: 'Sand', light: palette('#faf6f0', '#fffdf9', '#ebe1d4', '#e6dbcd', '#f3ebe0', '#2a2018', '#59493c', '#98887a', '#76675a', '#f2eadf', '#ece3d7', '#9a6a2f'),
    dark: palette('#16130f', '#1d1915', '#322a21', '#372e24', '#241f19', '#f0e9e0', '#c3b4a4', '#8c7d6e', '#aa9888', '#241f19', '#372e24', '#e0b06f') },
  sunset: { label: 'Sunset', light: palette('#fbf5f0', '#fffcf9', '#efdfd2', '#eadacc', '#f6eadf', '#2b1d15', '#5d473a', '#9c8576', '#7a6356', '#f5e9de', '#efe2d6', '#c25a14'),
    dark: palette('#17110d', '#1f1712', '#35271e', '#3a2b21', '#271d16', '#f2e7de', '#c6b1a2', '#8f7a6c', '#ad9686', '#271d16', '#3a2b21', '#f5a05c') },
  rose: { label: 'Rose', light: palette('#fbf4f5', '#ffffff', '#eedee1', '#e9d8dc', '#f5e8eb', '#2a191d', '#5c444a', '#9a8187', '#7a6167', '#f6e9ec', '#f0e2e5', '#c0365a'),
    dark: palette('#160f11', '#1e1518', '#342429', '#39282d', '#251a1e', '#f3e5e8', '#c9b0b6', '#93777e', '#b0949b', '#251a1e', '#39282d', '#f38aa6') },
  violet: { label: 'Violet', light: palette('#f6f4fb', '#ffffff', '#e3dfef', '#ded9eb', '#eeeaf7', '#1d192b', '#4b4460', '#89829d', '#676080', '#eeeaf7', '#e6e2f2', '#7046c9'),
    dark: palette('#121018', '#191621', '#2b2638', '#302a3f', '#201c2a', '#ebe7f5', '#b9b1cf', '#7e7796', '#9c94b6', '#201c2a', '#302a3f', '#b79bff') },
};
const PALETTE_KEYS = Object.keys(PRESETS.ignition.light);

const KEY = 'ignition:preferences';
let current = load();
const listeners = new Set();

function load() {
  try {
    const saved = JSON.parse(localStorage.getItem(KEY) || 'null');
    if (saved) return { ...DEFAULTS, ...saved };
    // Before preferences existed, the theme toggle saved just the theme.
    const legacy = localStorage.getItem('theme');
    if (legacy === 'light' || legacy === 'dark') return { ...DEFAULTS, theme: legacy };
  } catch { /* private mode */ }
  return { ...DEFAULTS };
}

export const getPreferences = () => current;

export function usePreferences() {
  return useSyncExternalStore(fn => { listeners.add(fn); return () => listeners.delete(fn); }, () => current);
}

const darkQuery = matchMedia('(prefers-color-scheme: dark)');
const motionQuery = matchMedia('(prefers-reduced-motion: reduce)');
export const resolvedTheme = (p = current) => p.theme === 'system' ? (darkQuery.matches ? 'dark' : 'light') : p.theme;

/** Puts preferences on the page: theme, accent, contrast, size, motion. */
export function applyPreferences(p = current) {
  const root = document.documentElement;
  const theme = resolvedTheme(p);
  root.dataset.theme = theme;
  root.dataset.contrast = p.contrast;
  root.dataset.density = p.tableDensity;
  root.dataset.motion = p.motion === 'system' ? (motionQuery.matches ? 'reduce' : 'full') : p.motion;
  const preset = PRESETS[p.preset] ?? PRESETS.ignition;
  const colors = preset[theme];
  // High contrast keeps its own stronger borders and text; the preset supplies the rest.
  const strong = new Set(p.contrast === 'high' ? ['border', 'line', 'muted', 'text-2'] : []);
  for (const key of PALETTE_KEYS) {
    if (preset.native || strong.has(key)) root.style.removeProperty(`--${key}`);
    else root.style.setProperty(`--${key}`, colors[key]);
  }
  const accent = p.accent || colors.accent;
  if (p.accent || !preset.native) root.style.setProperty('--accent', accent);
  else root.style.removeProperty('--accent');
  root.style.setProperty('--on-accent', readableOn(accent));
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', colors.bg);
  root.style.zoom = p.size === '100' ? '' : `${Number(p.size) / 100}`;
}

// White or near-black text, whichever reads better on the accent.
function readableOn(hex) {
  const n = parseInt(hex.slice(1), 16);
  const [r, g, b] = [n >> 16, (n >> 8) & 255, n & 255].map(v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.4 ? '#10131f' : '#fff';
}

darkQuery.addEventListener('change', () => current.theme === 'system' && (applyPreferences(), listeners.forEach(fn => fn())));
motionQuery.addEventListener('change', () => current.motion === 'system' && applyPreferences());

let saveTimer;
let saver = null;
/** The session registers how to save to the account (signed out, changes stay in this browser). */
export function setPreferenceSaver(fn) { saver = fn; }

/** Updates preferences now and saves them to the account shortly after (changes are batched). */
export function setPreferences(patch, { persist = true } = {}) {
  current = { ...current, ...patch };
  try { localStorage.setItem(KEY, JSON.stringify(current)); } catch { /* private mode */ }
  applyPreferences();
  listeners.forEach(fn => fn());
  if (persist && saver) {
    clearTimeout(saveTimer);
    saveTimer = setTimeout(() => saver(current), 400);
  }
}

/** Preferences from the server win over this browser's cache. */
export function adoptServerPreferences(p) {
  if (!p || !Object.keys(p).length) {
    if (saver && JSON.stringify(current) !== JSON.stringify(DEFAULTS)) saver(current); // first time: keep what this browser had
    return;
  }
  setPreferences({ ...DEFAULTS, ...p }, { persist: false });
}

/* ------------------------------ Time and dates ------------------------------ */

// Locales whose numeric dates have the order people pick ("auto" uses the browser's).
const dateLocales = { iso: 'sv-SE', dmy: 'en-GB', mdy: 'en-US' };

/** Options every date/time display should use, for the chosen clock and time zone. */
export function dateDefaults(p = current) {
  const o = {};
  if (p.clock !== 'auto') o.hour12 = p.clock === '12h';
  if (p.timezone && p.timezone !== 'browser') o.timeZone = p.timezone;
  return o;
}

// Date's own formatting follows the preferences too, so every existing toLocale…String call in the
// dashboard picks them up without each page having to know.
const native = {
  toLocaleString: Date.prototype.toLocaleString,
  toLocaleDateString: Date.prototype.toLocaleDateString,
  toLocaleTimeString: Date.prototype.toLocaleTimeString,
};
for (const name of Object.keys(native)) {
  Date.prototype[name] = function (locales, options) {
    const loc = locales ?? dateLocales[current.dateFormat];
    return native[name].call(this, loc, { ...dateDefaults(), ...options });
  };
}

/** "5 min ago", or the exact date and time when that's preferred. */
export function formatWhen(date) {
  const d = new Date(date);
  const sameYear = d.getFullYear() === new Date().getFullYear();
  return d.toLocaleString(undefined, { year: sameYear ? undefined : 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
}

/** How much to stretch polling intervals: live updates more often, relaxed less. */
export const refreshFactor = () => ({ live: 0.5, normal: 1, relaxed: 3 })[current.refresh] ?? 1;
