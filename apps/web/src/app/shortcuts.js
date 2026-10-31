import { useEffect } from 'react';
import { getPreferences } from './preferences';

/*
 * Keyboard shortcuts (Settings › Preferences turns them off). "/" focuses the page's search,
 * "g" then a letter goes somewhere, "[" shows or hides the sidebar. Never while typing.
 */

export const SHORTCUTS = [
  { keys: ['/'], label: 'Search this page' },
  { keys: ['g', 'd'], label: 'Go to Dashboard', page: 'Dashboard' },
  { keys: ['g', 'p'], label: 'Go to Projects', page: 'Projects' },
  { keys: ['g', 's'], label: 'Go to Servers', page: 'Servers' },
  { keys: ['g', 'm'], label: 'Go to Monitor', page: 'Monitor' },
  { keys: ['g', 'o'], label: 'Go to Domains', page: 'Domains' },
  { keys: ['g', 'n'], label: 'Go to Networking', page: 'Networking' },
  { keys: ['g', ','], label: 'Go to Preferences', page: 'Preferences' },
  { keys: ['['], label: 'Show or hide the sidebar' },
];

const typing = el => el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName));

export function useShortcuts({ navigate, closeProject, toggleSidebar }) {
  useEffect(() => {
    let pending = null;
    let timer;
    function onKey(e) {
      if (getPreferences().shortcuts === 'off' || e.metaKey || e.ctrlKey || e.altKey || typing(e.target) || document.querySelector('dialog[open]')) return;
      if (pending === 'g') {
        pending = null;
        clearTimeout(timer);
        const hit = SHORTCUTS.find(s => s.keys[0] === 'g' && s.keys[1] === e.key);
        if (hit) { e.preventDefault(); closeProject(); navigate(hit.page); }
        return;
      }
      if (e.key === 'g') { pending = 'g'; timer = setTimeout(() => { pending = null; }, 1200); return; }
      if (e.key === '/') {
        const search = document.querySelector('main .search-field input, main input[type="search"]');
        if (search) { e.preventDefault(); search.focus(); search.select?.(); }
      }
      if (e.key === '[') { e.preventDefault(); toggleSidebar(); }
    }
    addEventListener('keydown', onKey);
    return () => { removeEventListener('keydown', onKey); clearTimeout(timer); };
  }, [navigate, closeProject, toggleSidebar]);
}
