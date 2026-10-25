import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  AdjustmentsHorizontalIcon, ArrowDownIcon, ArrowDownTrayIcon, ArrowUturnLeftIcon, ChevronDownIcon, ChevronRightIcon, ChevronUpIcon, ClipboardDocumentIcon,
  LinkIcon, MinusIcon, PauseIcon, PlayIcon, PlusIcon, XMarkIcon,
} from '@heroicons/react/16/solid';
import { Menu, SearchField, Select, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { CopyButton } from './shared';
import { fieldToken, highlightRanges, matchesQuery, parseMessage, parseQuery } from './logQuery';
import './Logs.css';

/*
 * Runtime logs, one dense row per line. The running release is tailed live from Docker; earlier releases
 * come from the archive the worker keeps when it replaces a container. JSON and logfmt lines are parsed, so
 * their level, message and fields are real and filterable (status:500, user.id:42, ms:>1000).
 */

const POLL_MS = 2000;
const LEVELS = ['error', 'warn', 'info', 'debug'];
const LEVEL_NAMES = { error: 'Errors', warn: 'Warnings', info: 'Info', debug: 'Debug' };
const LEVEL_TAGS = { debug: 'DBG', info: 'INF', warn: 'WRN', error: 'ERR' };
const RANGES = [
  { value: 'tail', label: 'Latest lines' },
  { value: 'deploy', label: 'Since this release started' },
  { value: '5m', label: 'Last 5 minutes' },
  { value: '15m', label: 'Last 15 minutes' },
  { value: '1h', label: 'Last hour' },
  { value: '6h', label: 'Last 6 hours' },
  { value: '24h', label: 'Last 24 hours' },
  { value: '7d', label: 'Last 7 days' },
];
const RANGE_SECONDS = { '5m': 300, '15m': 900, '1h': 3600, '6h': 21600, '24h': 86400, '7d': 604800 };
const LIMITS = [500, 1000, 5000, 10000];
const BUCKETS = 60;

const pad = (n, w = 2) => String(n).padStart(w, '0');
const clock = (d, utc, ms = true) => {
  const [h, m, s, x] = utc ? [d.getUTCHours(), d.getUTCMinutes(), d.getUTCSeconds(), d.getUTCMilliseconds()] : [d.getHours(), d.getMinutes(), d.getSeconds(), d.getMilliseconds()];
  return `${pad(h)}:${pad(m)}:${pad(s)}${ms ? `.${pad(x, 3)}` : ''}`;
};
const exportLine = l => `${l.time.toISOString()} ${l.level.toUpperCase().padEnd(5)} [${l.stream}] ${l.raw}`;

function toLine(raw, i) {
  const parsed = parseMessage(raw.message, raw.stream);
  const fieldText = Object.entries(parsed.fields).map(([k, v]) => `${k}=${v}`).join(' ');
  return {
    id: `${raw.time}#${i}`, time: new Date(raw.time), stream: raw.stream, raw: raw.message, ...parsed,
    search: `${parsed.message} ${fieldText} ${raw.message}`.toLowerCase(),
  };
}

// URL ⇄ state, so a filtered view can be shared or reloaded.
function readUrl() {
  const p = new URLSearchParams(typeof location === 'undefined' ? '' : location.search);
  const levels = p.get('levels');
  return {
    q: p.get('q') ?? '', stream: p.get('stream') ?? 'all', range: p.get('range') ?? 'tail', deployment: p.get('deployment') ?? '',
    levels: levels ? new Set(levels.split(',').filter(l => LEVELS.includes(l))) : new Set(LEVELS), limit: Number(p.get('lines')) || 1000,
  };
}

function Highlighted({ text, terms }) {
  const ranges = useMemo(() => highlightRanges(text, terms), [text, terms]);
  if (!ranges.length) return text;
  const parts = [];
  let at = 0;
  ranges.forEach(([a, b], i) => { parts.push(text.slice(at, a), <mark key={i}>{text.slice(a, b)}</mark>); at = b; });
  parts.push(text.slice(at));
  return parts;
}

export function Logs({ resource }) {
  const initial = useMemo(readUrl, []);
  const [query, setQuery] = useState(initial.q);
  const [levels, setLevels] = useState(initial.levels);
  const [stream, setStream] = useState(initial.stream);
  const [range, setRange] = useState(initial.range);
  const [limit, setLimit] = useState(LIMITS.includes(initial.limit) ? initial.limit : 1000);
  const [deployment, setDeployment] = useState(initial.deployment);
  const [paused, setPaused] = useState(false);
  const [window_, setWindow] = useState(null); // [from, to] ms, from the histogram
  const [utc, setUtc] = useState(false);
  const [wrap, setWrap] = useState(false);
  const [lines, setLines] = useState([]);
  const [availability, setAvailability] = useState({ loading: true });
  const [context, setContext] = useState(null);
  const [open, setOpen] = useState(null);
  const [notice, setNotice] = useState('');
  const [atBottom, setAtBottom] = useState(true);
  const [unseen, setUnseen] = useState(0);
  const scrollRef = useRef(null);

  const sources = useQuery(() => api.logDeployments(resource.id), [resource.id], { pollMs: 30000 });
  const deployments = sources.data?.deployments ?? [];
  const current = deployments.find(d => d.current);
  // Default to the running release, or the newest archive when nothing runs.
  const selected = deployments.find(d => d.deploymentId === deployment) ?? current ?? deployments[0];
  const archived = selected && !selected.current;
  const live = !archived && !paused && range === 'tail';

  useEffect(() => {
    let stopped = false;
    async function load() {
      try {
        const now = Math.floor(Date.now() / 1000);
        const since = range === 'deploy' && selected?.startedAt ? Math.floor(new Date(selected.startedAt) / 1000) : RANGE_SECONDS[range] ? now - RANGE_SECONDS[range] : undefined;
        const res = await api.containerLogs(resource.id, { tail: limit, since, deployment: archived ? selected.deploymentId : undefined });
        if (stopped) return;
        setAvailability({ available: res.available, reason: res.reason, retention: res.retention, archived: res.archived });
        setLines(res.lines.map(toLine));
      } catch (e) {
        if (!stopped) setAvailability({ available: false, reason: e.message });
      }
    }
    if (!sources.data && !sources.error) return;
    setAvailability(a => ({ ...a, loading: true }));
    load();
    if (!live) return () => { stopped = true; };
    const id = setInterval(() => document.visibilityState === 'visible' && load(), POLL_MS);
    return () => { stopped = true; clearInterval(id); };
  }, [live, range, limit, resource.id, archived, selected?.deploymentId, !!sources.data]); // eslint-disable-line react-hooks/exhaustive-deps

  // Keep the URL in step (replaceState: filters shouldn't flood Back).
  useEffect(() => {
    const p = new URLSearchParams();
    if (query) p.set('q', query);
    if (levels.size !== LEVELS.length) p.set('levels', [...levels].join(','));
    if (stream !== 'all') p.set('stream', stream);
    if (range !== 'tail') p.set('range', range);
    if (limit !== 1000) p.set('lines', String(limit));
    if (archived) p.set('deployment', selected.deploymentId);
    const search = p.toString() ? `?${p}` : '';
    if (search !== location.search) history.replaceState(history.state, '', location.pathname + search);
  }, [query, levels, stream, range, limit, archived, selected?.deploymentId]);

  const terms = useMemo(() => parseQuery(query), [query]);
  // Everything except levels and the time window, so the level chips and histogram can show what's hidden.
  const base = useMemo(() => lines.filter(l => (stream === 'all' || l.stream === stream) && matchesQuery(l, terms)), [lines, stream, terms]);
  const levelCounts = useMemo(() => Object.fromEntries(LEVELS.map(lv => [lv, base.filter(l => l.level === lv).length])), [base]);
  const visible = useMemo(() => {
    if (context != null) {
      const i = lines.findIndex(l => l.id === context);
      return i < 0 ? [] : lines.slice(Math.max(0, i - 10), i + 11);
    }
    return base.filter(l => levels.has(l.level) && (!window_ || (l.time >= window_[0] && l.time <= window_[1])));
  }, [base, levels, window_, context, lines]);
  const filtered = query || stream !== 'all' || levels.size !== LEVELS.length || window_;
  const errors = visible.filter(l => l.level === 'error');

  function flash(text) { setNotice(text); setTimeout(() => setNotice(''), 1800); }
  function resetFilters() { setQuery(''); setLevels(new Set(LEVELS)); setStream('all'); setWindow(null); setContext(null); }
  function addToken(token) { setQuery(q => q.split(/\s+/).includes(token) ? q : `${q.trim()} ${token}`.trim()); }
  function toggleLevel(lv, only) {
    setLevels(cur => {
      if (only) return cur.size === 1 && cur.has(lv) ? new Set(LEVELS) : new Set([lv]);
      const next = new Set(cur);
      next.has(lv) ? next.delete(lv) : next.add(lv);
      return next.size ? next : new Set(LEVELS);
    });
  }

  // Stick to the bottom only while the reader is already there.
  const previousCount = useRef(visible.length);
  useLayoutEffect(() => {
    const el = scrollRef.current;
    const added = visible.length - previousCount.current;
    previousCount.current = visible.length;
    if (!el) return;
    if (atBottom) el.scrollTop = el.scrollHeight;
    else if (added > 0 && live) setUnseen(n => n + added);
  }, [visible, atBottom, live]);
  function onScroll() {
    const el = scrollRef.current;
    const bottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
    setAtBottom(bottom);
    if (bottom) setUnseen(0);
  }
  const smooth = () => matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth';
  function jumpToBottom() { scrollRef.current.scrollTo({ top: scrollRef.current.scrollHeight, behavior: smooth() }); setUnseen(0); }
  // Next/previous error relative to what's on screen.
  function jumpError(dir) {
    const el = scrollRef.current;
    const rows = [...el.querySelectorAll('.log.error')];
    if (!rows.length) return;
    const top = el.scrollTop, mid = top + el.clientHeight / 3;
    const target = dir > 0 ? rows.find(r => r.offsetTop > mid + 2) ?? rows[0] : [...rows].reverse().find(r => r.offsetTop < mid - 2) ?? rows.at(-1);
    el.scrollTo({ top: target.offsetTop - el.clientHeight / 3, behavior: smooth() });
    target.classList.add('flash');
    setTimeout(() => target.classList.remove('flash'), 900);
  }

  // Keyboard: "/" focuses the filter, Esc steps back out of context, the time window, then the query.
  useEffect(() => {
    function onKey(e) {
      const typing = /INPUT|TEXTAREA|SELECT/.test(document.activeElement?.tagName);
      if (e.key === '/' && !typing) { e.preventDefault(); document.querySelector('.logs-search input')?.focus(); }
      else if (e.key === 'Escape') {
        if (context != null) setContext(null);
        else if (window_) setWindow(null);
        else if (typing && query) setQuery('');
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [context, window_, query]);

  function download() {
    const url = URL.createObjectURL(new Blob([visible.map(exportLine).join('\n')], { type: 'text/plain' }));
    const tag = selected ? `-deploy${selected.number}` : '';
    Object.assign(document.createElement('a'), { href: url, download: `${resource.name}${tag}-${new Date().toISOString().slice(0, 19).replace(/:/g, '')}.log` }).click();
    URL.revokeObjectURL(url);
  }

  const deploymentOptions = deployments.map(d => ({
    value: d.deploymentId,
    label: `#${d.number}${d.current ? ' · running now' : ''}`,
    description: d.current ? `Live from Docker${d.commit ? ` · ${d.commit}` : ''}` : `${d.status === 'failed' ? 'Failed' : 'Replaced'} ${timeAgo(d.archivedAt)} · ${d.lines.toLocaleString()} lines${d.commit ? ` · ${d.commit}` : ''}`,
  }));

  return <div className="rp-page logs-page">
    <div className="logs-toolbar">
      <SearchField value={query} onChange={setQuery} placeholder='Filter: timeout, "exact phrase", status:>=500, -health, /regex/' label="Filter logs" className="logs-search" />
      {deploymentOptions.length > 0 && <Select label="Deployment" options={deploymentOptions} value={selected?.deploymentId} onChange={v => { setDeployment(v); setContext(null); setWindow(null); setPaused(false); }} className="logs-select logs-deploy" />}
      <Select label="Time range" options={RANGES} value={range} onChange={v => { setRange(v); setWindow(null); }} className="logs-select" />
      <Select label="Stream" options={[{ value: 'all', label: 'stdout + stderr' }, { value: 'stdout', label: 'stdout' }, { value: 'stderr', label: 'stderr' }]} value={stream} onChange={setStream} className="logs-select" />
      {archived ? <span className="logs-live archived" title="This release was replaced; its logs are a saved copy">Archived</span>
        : <button type="button" className={cx('logs-live', live && 'on')} aria-pressed={live} disabled={range !== 'tail'} onClick={() => setPaused(p => !p)} title={range !== 'tail' ? 'Live tail follows the latest lines' : live ? 'Pause live tail' : 'Resume live tail'}>
          {live ? <PauseIcon /> : <PlayIcon />}{live ? 'Live' : 'Paused'}
        </button>}
      <Menu trigger={AdjustmentsHorizontalIcon} label="View options" items={[
        { label: wrap ? 'Stop wrapping lines' : 'Wrap long lines', icon: AdjustmentsHorizontalIcon, onSelect: () => setWrap(w => !w) },
        { label: utc ? 'Show local time' : 'Show UTC time', icon: AdjustmentsHorizontalIcon, onSelect: () => setUtc(u => !u) },
        ...LIMITS.filter(n => n !== limit).map(n => ({ label: `Load the last ${n.toLocaleString()} lines`, icon: ArrowDownIcon, onSelect: () => setLimit(n) })),
        'separator',
        { label: `Copy ${visible.length.toLocaleString()} visible lines`, icon: ClipboardDocumentIcon, onSelect: () => { navigator.clipboard?.writeText(visible.map(exportLine).join('\n')); flash(`${visible.length} lines copied`); } },
        { label: 'Copy link to this view', icon: LinkIcon, onSelect: () => { navigator.clipboard?.writeText(location.href); flash('Link copied'); } },
        { label: 'Download visible lines', icon: ArrowDownTrayIcon, onSelect: download },
        'separator',
        { label: 'Reset filters', icon: ArrowUturnLeftIcon, onSelect: resetFilters },
      ]} />
    </div>

    <div className="logs-levels" role="group" aria-label="Levels">
      {LEVELS.map(lv => <button key={lv} type="button" className={cx('logs-level-chip', lv, levels.has(lv) && 'on')} aria-pressed={levels.has(lv)}
        title="Click to toggle · Alt-click to show only this level" onClick={e => toggleLevel(lv, e.altKey)}>
        <span className="dot" />{LEVEL_NAMES[lv]}<span className="count">{levelCounts[lv].toLocaleString()}</span>
      </button>)}
      {window_ && <span className="logs-window">{clock(new Date(window_[0]), utc, false)} – {clock(new Date(window_[1]), utc, false)}<button type="button" aria-label="Clear time window" onClick={() => setWindow(null)}><XMarkIcon /></button></span>}
      <span className="logs-hint">Press <kbd>/</kbd> to filter</span>
    </div>

    <Histogram lines={base.filter(l => levels.has(l.level))} window_={window_} onWindow={setWindow} utc={utc} />

    {context != null && <div className="logs-context-bar">Showing 10 lines before and after the selected line. Filters are paused.<button type="button" onClick={() => setContext(null)}>Back to results</button></div>}
    <div className={cx('logs-view', wrap && 'wrap')}>
      <div className="logs-head" aria-hidden="true"><span />{utc ? 'Time (UTC)' : 'Time'}<span>Level</span><span>Message</span></div>
      <div className="logs-body" ref={scrollRef} onScroll={onScroll} role="log" aria-live="off" aria-label={`${resource.name} logs`}>
        {visible.map(l => {
          const expanded = open === l.id;
          const fieldList = Object.entries(l.fields);
          return <div key={l.id} className={cx('log', l.level, expanded && 'expanded', context === l.id && 'focus')}>
            <button type="button" className="log-line" aria-expanded={expanded} onClick={() => setOpen(expanded ? null : l.id)}>
              <ChevronRightIcon className="log-caret" />
              <time dateTime={l.time.toISOString()}>{clock(l.time, utc)}</time>
              <span className="log-level">{LEVEL_TAGS[l.level]}</span>
              <span className="log-message">
                {l.stream === 'stderr' && <span className="log-tag">err</span>}
                <Highlighted text={l.message} terms={terms} />
                {fieldList.length > 0 && !expanded && <span className="log-fields">{fieldList.slice(0, 4).map(([k, v]) => <span key={k}> {k}=<b>{String(v)}</b></span>)}{fieldList.length > 4 && ` +${fieldList.length - 4}`}</span>}
              </span>
            </button>
            {expanded && <div className="log-attrs">
              <dl>
                <div><dt>time</dt><dd>{l.time.toISOString()}</dd></div>
                <div><dt>level</dt><dd>{l.level}<FieldActions onAdd={() => addToken(`level:${l.level}`)} onExclude={() => addToken(`-level:${l.level}`)} /></dd></div>
                <div><dt>stream</dt><dd>{l.stream}<FieldActions onAdd={() => setStream(l.stream)} onExclude={() => setStream(l.stream === 'stdout' ? 'stderr' : 'stdout')} /></dd></div>
                {selected && <div><dt>deployment</dt><dd>#{selected.number}{selected.commit && ` · ${selected.commit}`}</dd></div>}
                {fieldList.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{String(v)}<FieldActions onAdd={() => addToken(fieldToken(k, v))} onExclude={() => addToken(fieldToken(k, v, true))} /></dd></div>)}
                {l.format !== 'text' && <div><dt>raw ({l.format})</dt><dd className="log-raw">{l.raw}</dd></div>}
              </dl>
              <div className="log-attr-actions">
                <CopyButton value={l.raw} label="Copy raw line" />
                {context !== l.id && <button type="button" className="log-filter-btn" onClick={() => setContext(l.id)}>Show context</button>}
              </div>
            </div>}
          </div>;
        })}
        {!visible.length && <div className="logs-empty">{lines.length
          ? <>No lines match these filters. <button type="button" onClick={resetFilters}>Reset filters</button></>
          : availability.loading ? 'Loading logs…' : availability.available === false ? availability.reason
            : range === 'tail' ? 'No output yet. Lines appear as soon as the app writes to stdout or stderr.' : 'Nothing was logged in this time range.'}</div>}
      </div>
      {unseen > 0 && <button type="button" className="logs-new" onClick={jumpToBottom}><ArrowDownIcon />{unseen} new {unseen === 1 ? 'line' : 'lines'}</button>}
    </div>

    <div className="logs-footer">
      <span>{visible.length.toLocaleString()} lines{visible.length !== lines.length && ` of ${lines.length.toLocaleString()}`}</span>
      {errors.length > 0 && <span className="logs-errnav">
        <button type="button" className="logs-errors" onClick={() => jumpError(1)}>{errors.length} {errors.length === 1 ? 'error' : 'errors'}</button>
        <button type="button" aria-label="Previous error" onClick={() => jumpError(-1)}><ChevronUpIcon /></button>
        <button type="button" aria-label="Next error" onClick={() => jumpError(1)}><ChevronDownIcon /></button>
      </span>}
      {filtered && context == null && <button type="button" className="logs-reset" onClick={resetFilters}>Reset filters</button>}
      {notice && <span className="logs-notice" role="status">{notice}</span>}
      <span className="spacer" />
      <span className="logs-source">{archived ? `Saved copy of deployment #${selected.number}` : 'Live from Docker'} · last {limit.toLocaleString()} lines · {utc ? 'UTC' : 'local time'}</span>
    </div>
    {availability.retention && <p className="logs-retention">{availability.retention}</p>}
  </div>;
}

function FieldActions({ onAdd, onExclude }) {
  return <span className="log-field-actions">
    <button type="button" title="Show only lines with this value" aria-label="Filter by this value" onClick={onAdd}><PlusIcon /></button>
    <button type="button" title="Hide lines with this value" aria-label="Exclude this value" onClick={onExclude}><MinusIcon /></button>
  </span>;
}

// Log volume over time, errors on top. Drag across it (or click a bar) to zoom into that window.
function Histogram({ lines, window_, onWindow, utc }) {
  const [drag, setDrag] = useState(null);
  const ref = useRef(null);
  const data = useMemo(() => {
    if (lines.length < 2) return null;
    const from = lines[0].time.getTime(), to = lines.at(-1).time.getTime();
    const span = Math.max(to - from, 1000);
    const size = span / BUCKETS;
    const buckets = Array.from({ length: BUCKETS }, (_, i) => ({ from: from + i * size, to: from + (i + 1) * size, total: 0, errors: 0, warns: 0 }));
    for (const l of lines) {
      const b = buckets[Math.min(BUCKETS - 1, Math.floor((l.time.getTime() - from) / size))];
      b.total++;
      if (l.level === 'error') b.errors++; else if (l.level === 'warn') b.warns++;
    }
    return { buckets, max: Math.max(...buckets.map(b => b.total)), from, to };
  }, [lines]);
  if (!data) return null;
  const at = e => {
    const r = ref.current.getBoundingClientRect();
    return Math.max(0, Math.min(BUCKETS - 1, Math.floor(((e.clientX - r.left) / r.width) * BUCKETS)));
  };
  const finish = () => {
    if (!drag) return;
    const [a, b] = [Math.min(drag.start, drag.end), Math.max(drag.start, drag.end)];
    onWindow([data.buckets[a].from, data.buckets[b].to]);
    setDrag(null);
  };
  const sel = drag ? [Math.min(drag.start, drag.end), Math.max(drag.start, drag.end)] : null;
  return <div className="logs-histogram">
    <div ref={ref} className="logs-bars" role="img" aria-label={`Log volume from ${clock(new Date(data.from), utc, false)} to ${clock(new Date(data.to), utc, false)}. Drag to zoom.`}
      onPointerDown={e => { e.currentTarget.setPointerCapture(e.pointerId); const i = at(e); setDrag({ start: i, end: i }); }}
      onPointerMove={e => drag && setDrag(d => ({ ...d, end: at(e) }))} onPointerUp={finish} onPointerCancel={() => setDrag(null)}>
      {data.buckets.map((b, i) => {
        const inWindow = window_ ? b.to >= window_[0] && b.from <= window_[1] : true;
        const selecting = sel && i >= sel[0] && i <= sel[1];
        return <span key={i} className={cx('bar', !inWindow && 'dim', selecting && 'sel')} title={`${clock(new Date(b.from), utc, false)} · ${b.total} lines${b.errors ? ` · ${b.errors} errors` : ''}`}>
          <span className="fill" style={{ height: `${(b.total / data.max) * 100}%`, '--e': `${(b.errors / (b.total || 1)) * 100}%`, '--w': `${((b.errors + b.warns) / (b.total || 1)) * 100}%` }} />
        </span>;
      })}
    </div>
    <div className="logs-axis"><span>{clock(new Date(data.from), utc, false)}</span><span>Drag to zoom</span><span>{clock(new Date(data.to), utc, false)}</span></div>
  </div>;
}
