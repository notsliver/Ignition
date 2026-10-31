import { useEffect, useMemo, useRef, useState } from 'react';
import { ArrowDownTrayIcon, ArrowDownIcon, CommandLineIcon, PauseIcon, PlayIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Notice, SearchField, SegmentedControl, Switch, cx } from '../../../ui';
import { api } from '../../../api/client';
import { getPreferences } from '../../../app/preferences';
import './views.css';

/*
 * One stream for the whole environment: the runtime output of every running resource, merged by
 * time and coloured by resource. Live mode asks only for lines newer than the last one shown and
 * pauses itself while you scroll back. Secret values are redacted by the API.
 */

const MAX_LINES = 5000;
const levels = [{ value: 'all', label: 'All' }, { value: 'warn', label: 'Warnings +' }, { value: 'error', label: 'Errors' }];
const palette = ['#3c82f6', '#16a34a', '#d97706', '#db2777', '#7c3aed', '#0891b2', '#dc2626', '#65a30d', '#c026d3', '#0d9488'];
const colorOf = (id, ids) => palette[ids.indexOf(id) % palette.length];

export function ProjectLogs({ project, environment, onOpenResource }) {
  const [lines, setLines] = useState([]);
  const [sources, setSources] = useState([]);
  const [failed, setFailed] = useState({});
  const [hidden, setHidden] = useState(() => new Set());
  const [level, setLevel] = useState('all');
  const [query, setQuery] = useState('');
  const [live, setLive] = useState(true);
  const [wrap, setWrap] = useState(true);
  const [stuck, setStuck] = useState(true); // following the bottom
  const [error, setError] = useState('');
  const [loaded, setLoaded] = useState(false);
  const box = useRef(null);
  const last = useRef(''); // the newest line's exact timestamp

  // Load the recent history, then poll for newer lines while live.
  useEffect(() => {
    let stop = false;
    setLines([]); setLoaded(false); last.current = '';
    async function load(initial) {
      try {
        const out = await api.projectLogs(project.id, { env: environment, tail: initial ? 500 : 1000, since: initial ? undefined : last.current || undefined });
        if (stop) return;
        setSources(out.sources); setFailed(out.failed); setError('');
        if (out.lines.length) {
          last.current = out.lines.at(-1).time;
          setLines(cur => (initial ? out.lines : [...cur, ...out.lines]).slice(-MAX_LINES));
        }
        setLoaded(true);
      } catch (e) { if (!stop) setError(e.message); }
    }
    load(true);
    const t = setInterval(() => { if (live && document.visibilityState === 'visible') load(false); }, Math.max(1500, 2500 * ({ live: 0.5, normal: 1, relaxed: 3 }[getPreferences().refresh] ?? 1)));
    return () => { stop = true; clearInterval(t); };
  }, [project.id, environment, live]);

  const ids = useMemo(() => sources.map(s => s.id), [sources]);
  const q = query.trim().toLowerCase();
  const shown = useMemo(() => lines.filter(l => !hidden.has(l.resourceId) && (level === 'all' || (level === 'error' ? l.level === 'error' : l.level !== 'info'))
    && (!q || l.message.toLowerCase().includes(q) || l.resource.toLowerCase().includes(q))), [lines, hidden, level, q]);

  useEffect(() => { if (stuck && box.current) box.current.scrollTop = box.current.scrollHeight; }, [shown, stuck]);
  const onScroll = () => { const el = box.current; setStuck(el.scrollHeight - el.scrollTop - el.clientHeight < 24); };
  const toggle = id => setHidden(h => { const n = new Set(h); n.has(id) ? n.delete(id) : n.add(id); return n; });
  const only = id => setHidden(new Set(ids.filter(x => x !== id)));

  function download() {
    const text = shown.map(l => `${l.time} [${l.resource}] ${l.stream === 'stderr' ? 'ERR ' : ''}${l.message}`).join('\n');
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
    Object.assign(document.createElement('a'), { href: url, download: `${project.name}-${environment}-logs.txt` }).click();
    URL.revokeObjectURL(url);
  }
  const counts = useMemo(() => Object.fromEntries(ids.map(id => [id, lines.filter(l => l.resourceId === id).length])), [lines, ids]);
  const running = sources.filter(s => s.running);
  const highlight = msg => {
    if (!q) return msg;
    const i = msg.toLowerCase().indexOf(q);
    return i < 0 ? msg : <>{msg.slice(0, i)}<mark>{msg.slice(i, i + q.length)}</mark>{msg.slice(i + q.length)}</>;
  };

  return <div className="pv-page pv-logs-page">
    <div className="pv-toolbar">
      <SegmentedControl label="Level" options={levels} value={level} onChange={setLevel} className="auto-width" />
      <SearchField value={query} onChange={setQuery} placeholder="Filter lines" className="pv-log-search" />
      <span className="pv-spacer" />
      <label className="pv-switch"><Switch label="Wrap lines" checked={wrap} onChange={setWrap} />Wrap</label>
      <Button icon={live ? PauseIcon : PlayIcon} onClick={() => setLive(l => !l)}>{live ? 'Live' : 'Paused'}</Button>
      <Button icon={TrashIcon} onClick={() => setLines([])} title="Clear the view (new lines keep coming)">Clear</Button>
      <Button icon={ArrowDownTrayIcon} onClick={download} disabled={!shown.length}>Download</Button>
    </div>

    <div className="pv-sources">
      {sources.map(s => <button key={s.id} type="button" className={cx('pv-source', hidden.has(s.id) && 'off', !s.running && 'stopped')} style={{ '--src': colorOf(s.id, ids) }}
        onClick={e => (e.altKey ? only(s.id) : toggle(s.id))} title={s.running ? `${counts[s.id] ?? 0} lines · click to hide, Alt+click to show only this` : `${s.name} isn't running (${s.status})`} disabled={!s.running}>
        <i />{s.name}{s.running ? <small>{counts[s.id] ?? 0}</small> : <small>{s.status}</small>}
      </button>)}
      {hidden.size > 0 && <button type="button" className="pv-source-reset" onClick={() => setHidden(new Set())}>Show all</button>}
    </div>
    {error && <Notice tone="danger" title="Couldn't read logs">{error}</Notice>}
    {Object.keys(failed).length > 0 && <Notice tone="warning" title="Some logs couldn't be read">{Object.entries(failed).map(([n, e]) => `${n}: ${e}`).join('; ')}</Notice>}

    <div className="pv-log-wrap">
      <div ref={box} className={cx('pv-log', wrap && 'wrap')} onScroll={onScroll} role="log" aria-live={live ? 'polite' : 'off'}>
        {shown.map((l, i) => <div key={i} className={cx('pv-line', l.level)}>
          <time title={new Date(l.time).toLocaleString()}>{new Date(l.time).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })}</time>
          <button type="button" className="pv-line-src" style={{ color: colorOf(l.resourceId, ids) }} onClick={() => onOpenResource({ id: l.resourceId, projectId: project.id }, 'Logs')}>{l.resource}</button>
          <span className="pv-line-msg">{highlight(l.message)}</span>
        </div>)}
        {!shown.length && <div className="pv-log-empty"><CommandLineIcon />
          {!loaded ? 'Reading logs…' : !running.length ? `Nothing is running in ${environment}.` : lines.length ? 'No lines match these filters.' : 'No output yet. New lines appear here as they\'re written.'}</div>}
      </div>
      {!stuck && <button type="button" className="pv-jump" onClick={() => { setStuck(true); box.current.scrollTop = box.current.scrollHeight; }}><ArrowDownIcon />Jump to newest</button>}
    </div>
    <p className="pv-meta">{shown.length} of {lines.length} lines · {running.length} of {sources.length} resources running{lines.length >= MAX_LINES ? ` · keeping the newest ${MAX_LINES}` : ''}</p>
  </div>;
}
