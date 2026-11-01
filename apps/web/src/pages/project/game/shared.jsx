import { useEffect, useState } from 'react';
import { ArrowPathIcon } from '@heroicons/react/16/solid';
import { Notice } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';

/* Shared bits of the game server pages. Every page reads /game for what this server is. */

export const bytes = n => n == null ? '—' : n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(0)} KB` : n < 1 << 30 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${(n / (1 << 30)).toFixed(2)} GB`;
export const plural = (n, one, many = `${one}s`) => `${n.toLocaleString()} ${n === 1 ? one : many}`;
export const compact = n => n >= 1e6 ? `${(n / 1e6).toFixed(1)}M` : n >= 1e3 ? `${(n / 1e3).toFixed(n >= 1e4 ? 0 : 1)}k` : String(n);

export function useGame(resource, pollMs = 0) {
  return useQuery(() => api.game(resource.id), [resource.id], { pollMs });
}

/** The page's loading and error states, so each page only renders its content. */
export function GamePage({ query, title, children, className = '' }) {
  if (query.error && !query.data) return <div className={`rp-page gm-page ${className}`}><Notice tone="danger" title={`Couldn't load ${title}`}>{query.error.message} <button type="button" className="inline-link" onClick={query.refetch}>Retry</button></Notice></div>;
  if (!query.data) return <div className={`rp-page gm-page ${className}`}><p className="gm-muted">Loading…</p></div>;
  return <div className={`rp-page gm-page ${className}`}>{children}</div>;
}

/** Shown after a change that only applies when the server restarts. */
export function RestartNotice({ resource, text = 'Restart the server to apply it.', onDone }) {
  const [state, setState] = useState('');
  useEffect(() => setState(''), [text]);
  async function restart() {
    setState('busy');
    try { await api.resourceAction(resource.id, 'restart'); setState('done'); onDone?.(); } catch (e) { setState(e.message); }
  }
  return <Notice tone={state === 'done' ? 'success' : 'info'} title={state === 'done' ? 'Restarting' : 'Saved'}>
    {state === 'done' ? 'The server is restarting with the change.' : text}{' '}
    {state !== 'done' && <button type="button" className="inline-link" disabled={state === 'busy'} onClick={restart}><ArrowPathIcon className="gm-inline-icon" />Restart now</button>}
    {state && state !== 'busy' && state !== 'done' && <span className="gm-error"> {state}</span>}
  </Notice>;
}

/** Download without leaving the page (the browser shows its own progress). */
export function download(url) {
  const a = Object.assign(document.createElement('a'), { href: url, download: '' });
  document.body.append(a);
  a.click();
  a.remove();
}

/** A hidden file input you open from a button. */
export function pickFile(accept, onPick) {
  const input = Object.assign(document.createElement('input'), { type: 'file', accept });
  input.onchange = () => input.files[0] && onPick(input.files[0]);
  input.click();
}
