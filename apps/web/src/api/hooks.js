import { formatWhen, getPreferences, refreshFactor } from '../app/preferences';
import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Loads data and keeps it fresh. `pollMs` re-fetches on an interval (pass 0 to stop),
 * which is how deployments, statuses and logs stay live without a websocket.
 */
export function useQuery(fetcher, deps, { pollMs = 0, enabled = true } = {}) {
  const [state, setState] = useState({ data: null, error: null, loading: enabled });
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  const refetch = useCallback(async () => {
    try {
      const data = await fetcherRef.current();
      setState({ data, error: null, loading: false });
      return data;
    } catch (error) {
      setState(s => ({ data: s.data, error, loading: false }));
    }
  }, []);

  useEffect(() => {
    if (!enabled) return;
    setState(s => ({ ...s, loading: true }));
    refetch();
  }, [...deps, enabled]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!enabled || !pollMs) return;
    // Settings › Preferences › Refresh stretches or shortens every page's polling.
    const id = setInterval(() => { if (document.visibilityState === 'visible') refetch(); }, Math.max(2000, pollMs * refreshFactor()));
    return () => clearInterval(id);
  }, [pollMs, enabled, refetch]);

  return { ...state, refetch, setData: data => setState(s => ({ ...s, data: typeof data === 'function' ? data(s.data) : data })) };
}

/** Wraps a mutation with pending and error state. */
export function useMutation(fn) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState(null);
  const run = useCallback(async (...args) => {
    setPending(true);
    setError(null);
    try {
      return await fn(...args);
    } catch (e) {
      setError(e);
      throw e;
    } finally {
      setPending(false);
    }
  }, [fn]);
  return { run, pending, error, reset: () => setError(null) };
}

/** Human-friendly relative time for API timestamps. */
export function timeAgo(iso) {
  if (!iso) return '—';
  if (getPreferences().timeStyle === 'absolute') return formatWhen(iso);
  const seconds = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 45) return 'Just now';
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 7) return days === 1 ? 'Yesterday' : `${days}d ago`;
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

export function duration(start, end) {
  if (!start) return '—';
  const s = Math.max(0, Math.round(((end ? new Date(end) : new Date()) - new Date(start)) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
}
