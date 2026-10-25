import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { AdjustmentsHorizontalIcon, ArrowDownIcon, ArrowDownTrayIcon, ArrowUturnLeftIcon, ChevronRightIcon, ClipboardDocumentIcon, LinkIcon, PauseIcon, PlayIcon } from '@heroicons/react/16/solid';
import { Menu, SearchField, Select, cx } from '../../../ui';
import { CopyButton } from './shared';
import { api } from '../../../api/client';
import './Logs.css';

/*
 * Log viewer modelled on Vercel/Railway/Better Stack: one dense row per line,
 * click a row for its attributes, and a "new lines"
 * pill instead of yanking the scroll position while you read.
 */

const MAX_LINES = 1000;
const POLL_MS = 2000;

// Docker gives raw stdout/stderr text. Infer a level from common markers; everything else is info.
function levelOf(message) {
  if (/\b(fatal|panic|error|err|exception|traceback)\b/i.test(message)) return 'error';
  if (/\b(warn|warning|deprecated)\b/i.test(message)) return 'warn';
  if (/\b(debug|trace)\b/i.test(message)) return 'debug';
  return 'info';
}

function toLine(raw, i, replica) {
  const time = new Date(raw.time);
  return { id: `${raw.time}#${i}`, time, replica, level: levelOf(raw.message), stream: raw.stream, deployment: '', kind: 'event', message: raw.message,
    attrs: { stream: raw.stream, container: replica } };
}

const lineText = l => l.message;
const exportLine = l => `${l.time.toISOString()} ${l.level.toUpperCase().padEnd(5)} [${l.replica}] [${l.stream}] ${lineText(l)}`;
const pad = (n, w = 2) => String(n).padStart(w, '0');
const clock = (d, utc) => utc
  ? `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}.${pad(d.getUTCMilliseconds(), 3)}`
  : `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;

const levelLabel = { debug: 'DBG', info: 'INF', warn: 'WRN', error: 'ERR' };
const levelOptions = [
  { value: 'all', label: 'All levels' },
  { value: 'info', label: 'Info and above' },
  { value: 'warn', label: 'Warnings and errors' },
  { value: 'error', label: 'Errors only' },
];
const rank = { debug: 0, info: 1, warn: 2, error: 3 };
const rangeOptions = [
  { value: 'live', label: 'Live' },
  { value: '15m', label: 'Last 15 minutes' },
  { value: '1h', label: 'Last hour' },
  { value: '24h', label: 'Last 24 hours' },
];
const rangeSeconds = { '15m': 900, '1h': 3600, '24h': 86400 };

// Supports plain text plus `level:error` and `stream:stderr` filters.
function matches(line, query) {
  for (const token of query.toLowerCase().split(/\s+/).filter(Boolean)) {
    const [key, value] = token.includes(':') ? token.split(/:(.*)/) : [null, token];
    if (key === 'level') {
      if (line.level !== value) return false;
    } else if (key === 'stream') {
      if (line.stream !== value) return false;
    } else if (!lineText(line).toLowerCase().includes(token)) return false; // e.g. "http://…" is plain text
  }
  return true;
}

function Highlight({ text, query }) {
  const terms = query.toLowerCase().split(/\s+/).filter(t => t && !t.includes(':'));
  if (!terms.length) return text;
  const term = terms.find(t => text.toLowerCase().includes(t));
  if (!term) return text;
  const i = text.toLowerCase().indexOf(term);
  return <>{text.slice(0, i)}<mark>{text.slice(i, i + term.length)}</mark>{text.slice(i + term.length)}</>;
}

function Message({ line, query }) {
  if (line.kind === 'event') return <Highlight text={line.message} query={query} />;
  return <>
    <span className="log-method">{line.method}</span> <Highlight text={line.path} query={query} />{' '}
    <span className={cx('log-status', `s${String(line.status)[0]}`)}>{line.status}</span>{' '}
    <span className={cx('log-duration', line.duration > 1000 && 'slow')}>{line.duration >= 1000 ? `${(line.duration / 1000).toFixed(1)}s` : `${line.duration}ms`}</span>
  </>;
}

export function Logs({ resource }) {
  const replicas = useMemo(() => [resource.name], [resource.name]);
  const [lines, setLines] = useState([]);
  const [availability, setAvailability] = useState({ loading: true });
  const initial = useMemo(() => Object.fromEntries(new URLSearchParams(typeof location === 'undefined' ? '' : location.search)), []);
  const [range, setRange] = useState('live');
  const [level, setLevel] = useState(initial.level ?? 'all');
  const [replica, setReplica] = useState('all');
  const [stream, setStream] = useState(initial.stream ?? 'all');
  const [query, setQuery] = useState(initial.q ?? '');
  const [utc, setUtc] = useState(false);
  const [wrap, setWrap] = useState(false);
  const [context, setContext] = useState(null);
  const [notice, setNotice] = useState('');
  const [open, setOpen] = useState(null);
  const [atBottom, setAtBottom] = useState(true);
  const [unseen, setUnseen] = useState(0);
  const scrollRef = useRef(null);
  const live = range === 'live';

  useEffect(() => {
    let stopped = false;
    async function load() {
      try {
        const since = live ? undefined : Math.floor(Date.now() / 1000) - rangeSeconds[range];
        const res = await api.containerLogs(resource.id, { tail: MAX_LINES, since });
        if (stopped) return;
        setAvailability({ available: res.available, reason: res.reason, retention: res.retention });
        setLines(res.lines.map((l, i) => toLine(l, i, resource.name)));
      } catch (e) {
        if (!stopped) setAvailability({ available: false, reason: e.message });
      }
    }
    load();
    if (!live) return () => { stopped = true; };
    const id = setInterval(() => document.visibilityState === 'visible' && load(), POLL_MS);
    return () => { stopped = true; clearInterval(id); };
  }, [live, range, resource.id, resource.name]);

  const visible = useMemo(() => {
    if (context != null) {
      // Context mode ignores filters and shows what happened around one line.
      const i = lines.findIndex(l => l.id === context);
      return i < 0 ? [] : lines.slice(Math.max(0, i - 8), i + 9);
    }
    return lines.filter(l => rank[l.level] >= (level === 'all' ? 0 : rank[level]) && (replica === 'all' || l.replica === replica)
      && (stream === 'all' || l.stream === stream) && matches(l, query));
  }, [lines, level, replica, stream, query, context]);
  const filtered = level !== 'all' || replica !== 'all' || stream !== 'all' || query;

  function flash(text) {
    setNotice(text);
    setTimeout(() => setNotice(''), 1800);
  }
  function resetFilters() {
    setLevel('all'); setReplica('all'); setStream('all'); setQuery(''); setContext(null);
  }
  function copyLink() {
    const params = new URLSearchParams({ ...(query && { q: query }), ...(level !== 'all' && { level }), ...(stream !== 'all' && { stream }) });
    navigator.clipboard?.writeText(`${location.origin}${location.pathname}?${params}`);
    flash('Link to this view copied');
  }

  // Stick to the bottom only while the reader is already there.
  const previousCount = useRef(visible.length);
  useLayoutEffect(() => {
    const el = scrollRef.current;
    const added = visible.length - previousCount.current;
    previousCount.current = visible.length;
    if (!el) return;
    if (atBottom) el.scrollTop = el.scrollHeight;
    else if (added > 0) setUnseen(n => n + added);
  }, [visible, atBottom]);

  function onScroll() {
    const el = scrollRef.current;
    const bottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
    setAtBottom(bottom);
    if (bottom) setUnseen(0);
  }
  function jumpToBottom() {
    const el = scrollRef.current;
    el.scrollTo({ top: el.scrollHeight, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
    setUnseen(0);
  }
  function download() {
    const url = URL.createObjectURL(new Blob([visible.map(exportLine).join('\n')], { type: 'text/plain' }));
    Object.assign(document.createElement('a'), { href: url, download: `${resource.name}-${new Date().toISOString().slice(0, 19)}.log` }).click();
    URL.revokeObjectURL(url);
  }

  const errorCount = visible.filter(l => l.level === 'error').length;

  return <div className="rp-page logs-page">
    <div className="logs-toolbar">
      <SearchField value={query} onChange={setQuery} placeholder="Filter, e.g. timeout or level:error" label="Filter logs" className="logs-search" />
      <Select label="Level" options={levelOptions} value={level} onChange={setLevel} className="logs-select" />
      <Select label="Stream" options={[{ value: 'all', label: 'stdout + stderr' }, { value: 'stdout', label: 'stdout' }, { value: 'stderr', label: 'stderr' }]} value={stream} onChange={setStream} className="logs-select" />
      {replicas.length > 1 && <Select label="Replica" options={[{ value: 'all', label: 'All replicas' }, ...replicas.map(r => ({ value: r, label: r }))]} value={replica} onChange={setReplica} className="logs-select" />}
      <Select label="Time range" options={rangeOptions} value={range} onChange={setRange} className="logs-select" />
      <button type="button" className={cx('logs-live', live && 'on')} aria-pressed={live} onClick={() => setRange(live ? '15m' : 'live')} title={live ? 'Pause live tail' : 'Resume live tail'}>
        {live ? <PauseIcon /> : <PlayIcon />}{live ? 'Live' : 'Paused'}
      </button>
      <Menu trigger={AdjustmentsHorizontalIcon} label="View options" items={[
        { label: wrap ? 'Stop wrapping lines' : 'Wrap long lines', icon: AdjustmentsHorizontalIcon, onSelect: () => setWrap(w => !w) },
        { label: utc ? 'Show local time' : 'Show UTC time', icon: AdjustmentsHorizontalIcon, onSelect: () => setUtc(u => !u) },
        'separator',
        { label: `Copy ${visible.length} visible lines`, icon: ClipboardDocumentIcon, onSelect: () => { navigator.clipboard?.writeText(visible.map(exportLine).join('\n')); flash(`${visible.length} lines copied`); } },
        { label: 'Copy link to this view', icon: LinkIcon, onSelect: copyLink },
        { label: 'Download visible lines', icon: ArrowDownTrayIcon, onSelect: download },
        'separator',
        { label: 'Reset filters', icon: ArrowUturnLeftIcon, onSelect: resetFilters },
      ]} />
    </div>

    {context != null && <div className="logs-context-bar">Showing 8 lines before and after the selected line. Filters are paused.<button type="button" onClick={() => setContext(null)}>Back to results</button></div>}
    <div className={cx('logs-view', wrap && 'wrap')}>
      <div className="logs-head" aria-hidden="true"><span />{utc ? 'Time (UTC)' : 'Time'}<span>Level</span><span>Replica</span><span>Message</span></div>
      <div className="logs-body" ref={scrollRef} onScroll={onScroll} role="log" aria-live="off" aria-label={`${resource.name} logs`}>
        {visible.map(l => {
          const expanded = open === l.id;
          return <div key={l.id} className={cx('log', l.level, expanded && 'expanded', context === l.id && 'focus')}>
            <button type="button" className="log-line" aria-expanded={expanded} onClick={() => setOpen(expanded ? null : l.id)}>
              <ChevronRightIcon className="log-caret" />
              <time dateTime={l.time.toISOString()}>{clock(l.time, utc)}</time>
              <span className="log-level">{levelLabel[l.level]}</span>
              <span className="log-replica">{l.replica}</span>
              <span className="log-message"><Message line={l} query={query} /></span>
            </button>
            {expanded && <div className="log-attrs">
              <dl>
                <div><dt>timestamp</dt><dd>{l.time.toISOString()}</dd></div>
                <div><dt>level</dt><dd>{l.level}</dd></div>
                <div><dt>replica</dt><dd>{l.replica}</dd></div>
                <div><dt>stream</dt><dd>{l.stream}</dd></div>
                
                {Object.entries(l.attrs).map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{String(v)}</dd></div>)}
              </dl>
              <div className="log-attr-actions">
                <CopyButton value={exportLine(l)} label="Copy line" />
                {context !== l.id && <button type="button" className="log-filter-btn" onClick={() => { setContext(l.id); setRange('15m'); }}>Show context</button>}
              </div>
            </div>}
          </div>;
        })}
        {!visible.length && <div className="logs-empty">{lines.length ? <>No lines match these filters. <button type="button" onClick={resetFilters}>Reset filters</button></> : availability.loading ? 'Loading logs…' : availability.available === false ? availability.reason : 'No log lines yet. They appear here as soon as the container writes to stdout or stderr.'}</div>}
      </div>
      {unseen > 0 && <button type="button" className="logs-new" onClick={jumpToBottom}><ArrowDownIcon />{unseen} new {unseen === 1 ? 'line' : 'lines'}</button>}
    </div>

    <div className="logs-footer">
      <span>{visible.length.toLocaleString()} lines{visible.length !== lines.length && ` of ${lines.length.toLocaleString()}`}</span>
      {errorCount > 0 && <button type="button" className="logs-errors" onClick={() => setLevel('error')}>{errorCount} {errorCount === 1 ? 'error' : 'errors'}</button>}
      {filtered && context == null && <button type="button" className="logs-reset" onClick={resetFilters}>Reset filters</button>}
      {notice && <span className="logs-notice" role="status">{notice}</span>}
      <span className="spacer" />
      <span>{availability.retention ?? 'Logs come from Docker'} · last {MAX_LINES.toLocaleString()} lines · {utc ? 'UTC' : 'local time'}</span>
    </div>
  </div>;
}
