import { useEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowDownTrayIcon, ArrowPathIcon, ArrowRightIcon, CheckCircleIcon, CircleStackIcon, CloudArrowDownIcon, DocumentArrowUpIcon,
  EyeIcon, EyeSlashIcon, LinkIcon, SignalIcon, XCircleIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, FormField, Notice, SegmentedControl, Select, Status, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { CopyButton } from '../resource/shared';
import { dbFlow, engineIds, engineNames } from './flows';
import './Databases.css';

/*
 * Database data: importing into a new or existing database (upload, URL, S3 or a live database),
 * registering an external database, and the Connection card on a database's Overview.
 */

const SOURCES = [
  { value: 'upload', label: 'Upload' }, { value: 'url', label: 'URL' }, { value: 's3', label: 'S3' }, { value: 'live', label: 'Live database' },
];
const ENGINE_COLOR = { postgresql: '#336791', mysql: '#00758f', mariadb: '#c0765a', mongodb: '#47a248', redis: '#dc382d', dragonfly: '#7b61ff', clickhouse: '#e8b400' };
const bytes = n => n >= 1 << 30 ? `${(n / (1 << 30)).toFixed(1)} GB` : n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`;
const genPassword = () => Array.from(crypto.getRandomValues(new Uint32Array(24)), n => 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'[n % 56]).join('');
const sleep = ms => new Promise(r => setTimeout(r, ms));

export function engineOf(r) {
  if (r.engine) return r.engine;
  const img = (r.image || '').toLowerCase();
  return Object.keys(engineNames).find(id => img.includes(id === 'postgresql' ? 'postgres' : id === 'mongodb' ? 'mongo' : id)) ?? '';
}

function useEngines() {
  const { data } = useQuery(() => api.databaseEngines(), []);
  return data?.engines ?? [];
}

// Upload with progress (fetch can't report upload progress).
function upload(resourceId, file, replace, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `/api/resources/${resourceId}/imports`);
    xhr.withCredentials = true;
    xhr.upload.onprogress = e => e.lengthComputable && onProgress(e.loaded / e.total);
    xhr.onload = () => {
      let body = null;
      try { body = JSON.parse(xhr.responseText); } catch { /* not JSON */ }
      if (xhr.status >= 200 && xhr.status < 300) resolve(body);
      else reject(Object.assign(new Error(body?.error?.message ?? `Upload failed (${xhr.status})`), { fields: body?.error?.fields ?? {} }));
    };
    xhr.onerror = () => reject(new Error('The upload was interrupted. Check your connection and try again.'));
    const form = new FormData();
    form.append('replace', replace ? 'true' : 'false');
    form.append('file', file);
    xhr.send(form);
  });
}

/* --------------------------------- Import --------------------------------- */

export function ImportDataDialog({ project, environmentId, databases, source: initialSource = 'upload', target: presetTarget, onClose, onDone, onOpen }) {
  const engines = useEngines();
  const existing = databases.filter(d => d.sourceType !== 'external' && engineOf(d));
  const [into, setInto] = useState(presetTarget || existing.length ? 'existing' : 'new');
  const [targetId, setTargetId] = useState(presetTarget?.id ?? existing[0]?.id ?? '');
  const [engine, setEngine] = useState('postgresql');
  const [name, setName] = useState('');
  const [source, setSource] = useState(initialSource);
  const [file, setFile] = useState(null);
  const [dragging, setDragging] = useState(false);
  const [url, setUrl] = useState('');
  const [s3, setS3] = useState({ endpoint: '', region: '', bucket: '', key: '', accessKey: '', secretKey: '' });
  const [replace, setReplace] = useState(false);
  const [check, setCheck] = useState(null);
  const [errors, setErrors] = useState({});
  const [phase, setPhase] = useState(null); // {step, progress, text}
  const [importId, setImportId] = useState(null);
  const [failure, setFailure] = useState('');
  const fileInput = useRef();

  const target = existing.find(d => d.id === targetId);
  const engineId = into === 'existing' ? (target ? engineOf(target) : '') : engine;
  const eng = engines.find(e => e.id === engineId);
  const takenNames = databases.map(d => d.name);
  useEffect(() => { if (into === 'new' && !name) setName(suggestName(engine, takenNames)); }, [into]); // eslint-disable-line react-hooks/exhaustive-deps

  if (importId) return <ImportProgress importId={importId} onClose={() => { onDone?.(); onClose(); }} onOpen={onOpen} />;

  const busy = !!phase;
  const set = (setter, key) => v => { setter(v); setErrors(({ [key]: _, ...rest }) => rest); setFailure(''); };

  async function checkLive() {
    setCheck({ state: 'running' });
    try { setCheck(await api.checkSource(engineId, url)); } catch (e) { setCheck({ ok: false, error: e.message }); }
  }

  async function start() {
    const f = {};
    if (into === 'existing' && !target) f.target = 'Pick the database to import into.';
    if (into === 'new' && !/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(name)) f.name = 'Use lowercase letters, numbers and dashes.';
    if (into === 'new' && takenNames.includes(name)) f.name = 'A resource with this name already exists.';
    if (source === 'upload' && !file) f.file = 'Choose a dump file.';
    if ((source === 'url' || source === 'live') && !url.trim()) f.url = source === 'url' ? 'Enter the dump\'s URL.' : 'Enter the source database\'s connection URL.';
    if (source === 's3' && (!s3.bucket.trim() || !s3.key.trim())) f.s3 = 'Enter the bucket and the object key.';
    setErrors(f);
    if (Object.keys(f).length) return;
    setFailure('');
    try {
      let dbId = target?.id;
      if (into === 'new') {
        setPhase({ text: `Creating ${engineNames[engine]} database ${name}` });
        const flow = dbFlow(engineNames[engine]);
        const values = { name, version: flow.fields.find(x => x.key === 'version').default, database: 'app', user: 'ignition', password: genPassword(), publicAccess: 'private' };
        const { resource } = await api.createResource(project.id, { ...flow.build(values), environmentId });
        dbId = resource.id;
        setPhase({ text: `Waiting for ${name} to start` });
        for (let i = 0; i < 120; i++) {
          const { resource: r } = await api.resource(dbId);
          if (r.status === 'running') break;
          if (r.status === 'failed') throw new Error(`${name} didn't start. Open it to see why; nothing was imported.`);
          await sleep(2000);
        }
      }
      let out;
      if (source === 'upload') {
        setPhase({ text: `Uploading ${file.name}`, progress: 0 });
        out = await upload(dbId, file, replace, p => setPhase({ text: `Uploading ${file.name}`, progress: p }));
      } else {
        setPhase({ text: 'Starting the import' });
        out = await api.createImport(dbId, source === 's3' ? { source, s3, replace } : { source, url, replace });
      }
      setImportId(out.import.id);
    } catch (e) {
      setPhase(null);
      setErrors(e.fields ?? {});
      setFailure(Object.keys(e.fields ?? {}).length ? Object.values(e.fields)[0] : e.message);
    }
  }

  return <Dialog open onClose={busy ? () => {} : onClose} className="db-dialog" title="Import existing data"
    description="Load a dump, a file in object storage or a live database into a database here."
    footer={<>
      {failure && <span className="db-footer-error" role="alert">{failure}</span>}
      <Button onClick={onClose} disabled={busy}>Cancel</Button>
      <Button variant="primary" icon={ArrowDownTrayIcon} disabled={busy || !eng} onClick={start}>{busy ? 'Working…' : into === 'new' ? 'Create and import' : 'Import'}</Button>
    </>}>
    {busy ? <div className="db-busy">
      <ArrowPathIcon className="db-spin" />
      <strong>{phase.text}…</strong>
      {phase.progress != null && <div className="db-bar" role="progressbar" aria-valuenow={Math.round(phase.progress * 100)}><span style={{ width: `${phase.progress * 100}%` }} /></div>}
      {phase.progress != null && <small>{Math.round(phase.progress * 100)}% of {bytes(file.size)}. Keep this tab open until the upload finishes.</small>}
    </div> : <div className="db-form">
      <section className="db-step">
        <h3><span>1</span>Into</h3>
        <SegmentedControl label="Into" value={into} onChange={setInto} options={[{ value: 'existing', label: 'A database here' }, { value: 'new', label: 'A new database' }]} />
        {into === 'existing'
          ? existing.length
            ? <Select label="Database" value={targetId} onChange={set(setTargetId, 'target')} options={existing.map(d => ({ value: d.id, label: d.name, description: `${engineNames[engineOf(d)]} · ${d.status}`, dot: ENGINE_COLOR[engineOf(d)] }))} />
            : <p className="db-muted">No databases in this environment yet. Import into a new one instead.</p>
          : <div className="db-row">
            <label className="db-label">Engine<Select label="Engine" value={engine} onChange={v => { setEngine(v); setName(suggestName(v, takenNames)); setCheck(null); }}
              options={Object.entries(engineNames).map(([value, label]) => ({ value, label, dot: ENGINE_COLOR[value] }))} /></label>
            <label className="db-label">Name<TextInput className="mono" value={name} onChange={e => set(setName, 'name')(e.target.value)} aria-invalid={!!errors.name} /></label>
          </div>}
        {errors.target && <p className="db-error">{errors.target}</p>}
        {errors.name && <p className="db-error">{errors.name}</p>}
        {into === 'new' && <p className="db-muted">Created with a generated password and a private address, then the data is loaded once it's ready.</p>}
      </section>

      <section className="db-step">
        <h3><span>2</span>From</h3>
        <SegmentedControl label="Source" value={source} onChange={v => { setSource(v); setErrors({}); setCheck(null); }} options={SOURCES} />
        {source === 'upload' && <>
          <button type="button" className={cx('db-drop', dragging && 'over', file && 'chosen')} onClick={() => fileInput.current.click()}
            onDragOver={e => { e.preventDefault(); setDragging(true); }} onDragLeave={() => setDragging(false)}
            onDrop={e => { e.preventDefault(); setDragging(false); if (e.dataTransfer.files[0]) set(setFile, 'file')(e.dataTransfer.files[0]); }}>
            <DocumentArrowUpIcon />
            {file ? <><strong>{file.name}</strong><span>{bytes(file.size)} · click to choose another</span></>
              : <><strong>Drop a dump file here, or click to choose</strong><span>Up to 20 GB{eng ? ` · ${eng.extensions.join(' ')}` : ''}</span></>}
          </button>
          <input ref={fileInput} type="file" hidden accept={eng?.extensions.join(',')} onChange={e => e.target.files[0] && set(setFile, 'file')(e.target.files[0])} />
          {errors.file && <p className="db-error">{errors.file}</p>}
        </>}
        {source === 'url' && <label className="db-label">Download URL<TextInput className="mono" value={url} placeholder="https://… (a presigned link works)" onChange={e => set(setUrl, 'url')(e.target.value)} aria-invalid={!!errors.url} spellCheck={false} />
          <small className="db-muted">Downloaded by Ignition, then loaded. Nothing is stored after the import.</small></label>}
        {source === 's3' && <div className="db-grid">
          <label className="db-label">Bucket<TextInput value={s3.bucket} onChange={e => setS3(v => ({ ...v, bucket: e.target.value }))} placeholder="backups" /></label>
          <label className="db-label">Object key<TextInput className="mono" value={s3.key} onChange={e => setS3(v => ({ ...v, key: e.target.value }))} placeholder="nightly/app.dump" spellCheck={false} /></label>
          <label className="db-label">Endpoint <small>empty for AWS</small><TextInput className="mono" value={s3.endpoint} onChange={e => setS3(v => ({ ...v, endpoint: e.target.value }))} placeholder="https://<account>.r2.cloudflarestorage.com" spellCheck={false} /></label>
          <label className="db-label">Region<TextInput value={s3.region} onChange={e => setS3(v => ({ ...v, region: e.target.value }))} placeholder="us-east-1 (auto for R2)" /></label>
          <label className="db-label">Access key ID<TextInput className="mono" value={s3.accessKey} onChange={e => setS3(v => ({ ...v, accessKey: e.target.value }))} autoComplete="off" spellCheck={false} /></label>
          <label className="db-label">Secret access key<SecretField value={s3.secretKey} onChange={v => setS3(x => ({ ...x, secretKey: v }))} /></label>
          {errors.s3 && <p className="db-error db-span">{errors.s3}</p>}
          <p className="db-muted db-span">Works with AWS S3, Cloudflare R2, MinIO, Backblaze B2 and other S3-compatible stores. Leave the keys empty for a public bucket. Keys are stored encrypted for this import only.</p>
        </div>}
        {source === 'live' && <>
          <div className="db-live">
            <TextInput className="mono" value={url} placeholder={eng ? `${eng.scheme[0]}://user:password@host:${eng.defaultPort}/database` : 'connection URL'} aria-label="Source connection URL"
              onChange={e => { set(setUrl, 'url')(e.target.value); setCheck(null); }} aria-invalid={!!errors.url} spellCheck={false} autoComplete="off" />
            <Button icon={SignalIcon} disabled={!url.trim() || !eng || check?.state === 'running'} onClick={checkLive}>{check?.state === 'running' ? 'Checking…' : 'Check'}</Button>
          </div>
          {(() => {
            // The URL says which engine it is; offer a matching target instead of failing later.
            const scheme = (url.match(/^([a-z+]+):\/\//i)?.[1] ?? '').toLowerCase().replace(/s$/, '');
            const urlEngine = { postgres: 'postgresql', postgresql: 'postgresql', mysql: 'mysql', mariadb: 'mariadb', mongodb: 'mongodb', 'mongodb+srv': 'mongodb', redi: 'redis', redis: 'redis', clickhouse: 'clickhouse' }[scheme];
            if (!urlEngine || !engineId || urlEngine === engineId || (urlEngine === 'mysql' && engineId === 'mariadb') || (urlEngine === 'redis' && engineId === 'dragonfly')) return null;
            const match = existing.find(d => engineOf(d) === urlEngine);
            return <p className="db-check bad"><XCircleIcon /><span>That's a {engineNames[urlEngine]} URL, but {into === 'new' ? 'the new database' : target?.name} is {engineNames[engineId]}.{' '}
              {match && into === 'existing' ? <button type="button" className="inline-link" onClick={() => { setTargetId(match.id); setCheck(null); }}>Import into {match.name}</button>
                : <button type="button" className="inline-link" onClick={() => { setInto('new'); setEngine(urlEngine); setName(suggestName(urlEngine, takenNames)); setCheck(null); }}>Import into a new {engineNames[urlEngine]} database</button>}</span></p>;
          })()}
          {check && check.state !== 'running' && <p className={cx('db-check', check.ok ? 'ok' : 'bad')}>{check.ok ? <CheckCircleIcon /> : <XCircleIcon />}
            {check.ok ? `${check.address} answers (${check.latencyMs} ms). Credentials are checked when the import starts.` : check.error}</p>}
          {eng && <p className="db-muted">{eng.live}</p>}
        </>}
        {errors.url && <p className="db-error">{errors.url}</p>}
        {eng && source !== 'live' && <p className="db-muted">{eng.label} accepts: {eng.formats.join(' · ')}.</p>}
      </section>

      {eng && (into === 'existing' || source === 'live') && <label className="check-row db-replace"><Checkbox label="Replace existing data" checked={replace} onChange={setReplace} />
        <span><strong>Replace existing data</strong> · {eng.replace}</span></label>}
      {replace && into === 'existing' && <Notice tone="warning" title={`This removes data from ${target?.name}`}>There's no undo. Take a backup first if you might need what's there now.</Notice>}
    </div>}
  </Dialog>;
}

function suggestName(engine, taken) {
  for (let i = 1; i < 50; i++) {
    const n = i === 1 ? engine : `${engine}-${i}`;
    if (!taken.includes(n)) return n;
  }
  return `${engine}-${Date.now() % 1000}`;
}

function SecretField({ value, onChange, placeholder }) {
  const [shown, setShown] = useState(false);
  return <TextInput className="mono" type={shown ? 'text' : 'password'} value={value} placeholder={placeholder} onChange={e => onChange(e.target.value)} autoComplete="new-password" spellCheck={false}
    suffix={<button type="button" className="db-eye" aria-label={shown ? 'Hide' : 'Show'} onClick={() => setShown(s => !s)}>{shown ? <EyeSlashIcon /> : <EyeIcon />}</button>} />;
}

export function ImportProgress({ importId, onClose, onOpen }) {
  const [logs, setLogs] = useState([]);
  const [imp, setImp] = useState(null);
  const after = useRef(0);
  const pre = useRef();
  useEffect(() => {
    let stop = false;
    async function tick() {
      try {
        const out = await api.dataImport(importId, after.current);
        if (out.logs.length) { after.current = out.logs.at(-1).id; setLogs(l => [...l, ...out.logs].slice(-2000)); }
        setImp(out.import);
        if (['queued', 'running'].includes(out.import.status) && !stop) setTimeout(tick, 1500);
      } catch { if (!stop) setTimeout(tick, 3000); }
    }
    tick();
    return () => { stop = true; };
  }, [importId]);
  useEffect(() => { if (pre.current) pre.current.scrollTop = pre.current.scrollHeight; }, [logs]);
  const active = !imp || ['queued', 'running'].includes(imp.status);
  const tone = { succeeded: 'success', failed: 'danger', cancelled: 'neutral', running: 'progress', queued: 'neutral' }[imp?.status] ?? 'neutral';
  return <Dialog open onClose={onClose} className="db-dialog" title={active ? 'Importing…' : imp.status === 'succeeded' ? 'Import finished' : imp.status === 'cancelled' ? 'Import cancelled' : 'Import failed'}
    description={imp ? `${imp.label}${imp.sizeBytes ? ` · ${bytes(imp.sizeBytes)}` : ''}` : ''}
    footer={<>
      {active && imp && <Button onClick={() => api.cancelImport(importId).catch(() => {})}>Cancel import</Button>}
      {!active && onOpen && <Button icon={ArrowRightIcon} onClick={() => onOpen(imp.resourceId)}>Open database</Button>}
      <Button variant={active ? undefined : 'primary'} onClick={onClose}>{active ? 'Run in background' : 'Done'}</Button>
    </>}>
    <div className="db-progress-head"><Status tone={tone}>{imp ? imp.status[0].toUpperCase() + imp.status.slice(1) : 'Queued'}</Status>{active && <ArrowPathIcon className="db-spin" />}
      {imp?.summary && <span>{imp.summary}</span>}</div>
    <pre ref={pre} className="db-log">{logs.length ? logs.map(l => <span key={l.id} className={cx(l.level === 'error' && 'err', l.message.startsWith('==> ') && 'step')}>{l.message}{'\n'}</span>) : 'Waiting for the worker…'}</pre>
    {active && <p className="db-muted">You can close this; the import keeps running and shows under the database.</p>}
  </Dialog>;
}

/* ------------------------------ External database ------------------------------ */

export function ExternalDatabaseDialog({ project, environmentId, takenNames, onClose, onCreated }) {
  const engines = useEngines();
  const [engine, setEngine] = useState('postgresql');
  const [mode, setMode] = useState('url');
  const [v, setV] = useState({ name: 'postgresql-external', url: '', host: '', port: '', database: '', user: '', password: '', tls: false });
  const [nameTouched, setNameTouched] = useState(false);
  const [errors, setErrors] = useState({});
  const [failure, setFailure] = useState('');
  const [pending, setPending] = useState(false);
  const [check, setCheck] = useState(null);
  const eng = engines.find(e => e.id === engine);
  const set = key => value => { setV(x => ({ ...x, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); setCheck(null); };

  async function checkIt() {
    setCheck({ state: 'running' });
    const u = mode === 'url' ? v.url : `${eng.scheme[0]}://${encodeURIComponent(v.user)}:${encodeURIComponent(v.password)}@${v.host}:${v.port || eng.defaultPort}/${v.database || (engine === 'mysql' || engine === 'mariadb' || engine === 'clickhouse' ? 'x' : '')}`;
    try { setCheck(await api.checkSource(engine, u)); } catch (e) { setCheck({ ok: false, error: e.message }); }
  }
  async function save() {
    setPending(true);
    setFailure('');
    try {
      const body = { environmentId, name: v.name, engine, tls: v.tls, ...(mode === 'url' ? { url: v.url } : { host: v.host, port: Number(v.port) || 0, database: v.database, user: v.user, password: v.password }) };
      const { resource } = await api.createExternalDatabase(project.id, body);
      onCreated(resource);
    } catch (e) {
      setErrors(e.fields ?? {});
      setFailure(Object.keys(e.fields ?? {}).length ? Object.values(e.fields)[0] : e.message);
      setPending(false);
    }
  }
  return <Dialog open onClose={onClose} className="db-dialog" title="Connect an external database"
    description="Register a database that runs elsewhere (a managed service or another server). Ignition keeps its credentials encrypted, checks it's reachable, and hands its connection URL to your apps. It doesn't run or back it up."
    footer={<>
      {failure && <span className="db-footer-error" role="alert">{failure}</span>}
      <Button icon={SignalIcon} onClick={checkIt} disabled={!eng || check?.state === 'running' || (mode === 'url' ? !v.url.trim() : !v.host.trim())}>Check</Button>
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" icon={LinkIcon} disabled={pending} onClick={save}>{pending ? 'Connecting…' : 'Connect'}</Button>
    </>}>
    <div className="db-form">
      <div className="db-row">
        <label className="db-label">Engine<Select label="Engine" value={engine} onChange={e => { setEngine(e); if (!nameTouched) setV(x => ({ ...x, name: suggestName(`${e}-external`, takenNames) })); setCheck(null); }}
          options={Object.entries(engineNames).map(([value, label]) => ({ value, label, dot: ENGINE_COLOR[value] }))} /></label>
        <label className="db-label">Name<TextInput className="mono" value={v.name} onChange={e => { setNameTouched(true); set('name')(e.target.value); }} aria-invalid={!!errors.name} /></label>
      </div>
      <SegmentedControl label="How" value={mode} onChange={setMode} options={[{ value: 'url', label: 'Connection URL' }, { value: 'fields', label: 'Host and credentials' }]} />
      {mode === 'url'
        ? <label className="db-label">Connection URL<SecretField value={v.url} onChange={set('url')} placeholder={eng ? `${eng.scheme[0]}://user:password@db.example.com:${eng.defaultPort}/app` : ''} /></label>
        : <div className="db-grid">
          <label className="db-label">Host<TextInput className="mono" value={v.host} onChange={e => set('host')(e.target.value)} placeholder="db.example.com" aria-invalid={!!errors.host} spellCheck={false} /></label>
          <label className="db-label">Port<TextInput inputMode="numeric" value={v.port} onChange={e => set('port')(e.target.value.replace(/\D/g, ''))} placeholder={String(eng?.defaultPort ?? '')} /></label>
          {engine !== 'redis' && engine !== 'dragonfly' && <label className="db-label">Database<TextInput className="mono" value={v.database} onChange={e => set('database')(e.target.value)} placeholder="app" /></label>}
          <label className="db-label">User<TextInput className="mono" value={v.user} onChange={e => set('user')(e.target.value)} autoComplete="off" /></label>
          <label className="db-label db-span">Password<SecretField value={v.password} onChange={set('password')} /></label>
        </div>}
      <label className="check-row"><Switch label="TLS" checked={v.tls} onChange={set('tls')} />Require TLS (most managed databases do)</label>
      {check && check.state !== 'running' && <p className={cx('db-check', check.ok ? 'ok' : 'bad')}>{check.ok ? <CheckCircleIcon /> : <XCircleIcon />}
        {check.ok ? `${check.address} answers from this machine (${check.latencyMs} ms).` : check.error}</p>}
      <p className="db-muted">Apps on this machine reach it at its own address, so it must allow connections from this machine's IP. For a database running on this machine itself, use <code>host.docker.internal</code>.</p>
    </div>
  </Dialog>;
}

/* ------------------------------ Connection card ------------------------------ */

const TABS = [{ value: 'url', label: 'URL' }, { value: 'env', label: 'Variables' }, { value: 'cli', label: 'CLI' }, { value: 'code', label: 'Code' }];

// What apps and people need to connect, for each engine. `p` is the connection: host, port, user,
// password (or a placeholder), database, url, tls.
const snippets = {
  postgresql: p => ({
    env: [['DATABASE_URL', p.url], ['PGHOST', p.host], ['PGPORT', p.port], ['PGUSER', p.user], ['PGPASSWORD', p.password], ['PGDATABASE', p.database]],
    cli: `psql "${p.url}"`, terminal: `psql -U ${p.user || 'postgres'} -d ${p.database || 'postgres'}`,
    node: `import pg from 'pg';\nconst pool = new pg.Pool({ connectionString: process.env.DATABASE_URL });\nconst { rows } = await pool.query('select now()');`,
    python: `import os, psycopg\nwith psycopg.connect(os.environ["DATABASE_URL"]) as conn:\n    print(conn.execute("select now()").fetchone())`,
  }),
  mysql: p => ({
    env: [['DATABASE_URL', p.url], ['MYSQL_HOST', p.host], ['MYSQL_PORT', p.port], ['MYSQL_USER', p.user], ['MYSQL_PASSWORD', p.password], ['MYSQL_DATABASE', p.database]],
    cli: `mysql -h ${p.host} -P ${p.port} -u ${p.user} -p ${p.database}`, terminal: `mysql -u ${p.user || 'root'} -p ${p.database}`,
    node: `import mysql from 'mysql2/promise';\nconst db = await mysql.createConnection(process.env.DATABASE_URL);\nconst [rows] = await db.query('select now()');`,
    python: `import os, pymysql\nfrom urllib.parse import urlparse\nu = urlparse(os.environ["DATABASE_URL"])\nconn = pymysql.connect(host=u.hostname, port=u.port, user=u.username, password=u.password, database=u.path[1:])`,
  }),
  mongodb: p => ({
    env: [['MONGODB_URI', p.url]],
    cli: `mongosh "${p.url}"`, terminal: `mongosh -u ${p.user} -p --authenticationDatabase admin`,
    node: `import { MongoClient } from 'mongodb';\nconst client = await MongoClient.connect(process.env.MONGODB_URI);\nconst db = client.db('${p.database || 'app'}');`,
    python: `import os\nfrom pymongo import MongoClient\nclient = MongoClient(os.environ["MONGODB_URI"])\ndb = client["${p.database || 'app'}"]`,
  }),
  redis: p => ({
    env: [['REDIS_URL', p.url]],
    cli: `redis-cli -u "${p.url}"`, terminal: 'redis-cli -a "$REDIS_PASSWORD" --no-auth-warning',
    node: `import Redis from 'ioredis';\nconst redis = new Redis(process.env.REDIS_URL);\nawait redis.set('hello', 'world');`,
    python: `import os, redis\nr = redis.from_url(os.environ["REDIS_URL"])\nr.set("hello", "world")`,
  }),
  clickhouse: p => ({
    env: [['CLICKHOUSE_URL', `http${p.tls ? 's' : ''}://${p.host}:${p.httpPort ?? p.port}`], ['CLICKHOUSE_USER', p.user], ['CLICKHOUSE_PASSWORD', p.password], ['CLICKHOUSE_DB', p.database]],
    cli: `clickhouse-client --host ${p.host} --port 9000 --user ${p.user} --password '${p.password}' -d ${p.database}`, terminal: `clickhouse-client --user ${p.user} --password "$CLICKHOUSE_PASSWORD" -d ${p.database}`,
    node: `import { createClient } from '@clickhouse/client';\nconst ch = createClient({ url: process.env.CLICKHOUSE_URL, username: process.env.CLICKHOUSE_USER, password: process.env.CLICKHOUSE_PASSWORD, database: process.env.CLICKHOUSE_DB });`,
    python: `import os, clickhouse_connect\nch = clickhouse_connect.get_client(dsn=os.environ["CLICKHOUSE_URL"], username=os.environ["CLICKHOUSE_USER"], password=os.environ["CLICKHOUSE_PASSWORD"])`,
  }),
};
snippets.mariadb = p => ({ ...snippets.mysql(p), cli: `mariadb -h ${p.host} -P ${p.port} -u ${p.user} -p ${p.database}`, terminal: `mariadb -u ${p.user || 'root'} -p ${p.database}` });
snippets.dragonfly = p => ({ ...snippets.redis(p), terminal: null });

function withHost(url, host, port) {
  try { const u = new URL(url); u.hostname = host; u.port = String(port); return u.toString().replace(/\/$/, ''); } catch { return url; }
}

export function ConnectionCard({ resource, onImport, onNavigate }) {
  const conn = useQuery(() => api.connection(resource.id), [resource.id, resource.status]);
  const project = useQuery(() => api.resources(resource.projectId, { environment: resource.environmentId }), [resource.projectId]);
  const [tab, setTab] = useState('url');
  const [lang, setLang] = useState('node');
  const [from, setFrom] = useState('inside');
  const [shown, setShown] = useState(false);
  const [sharing, setSharing] = useState(false);
  const [test, setTest] = useState(null);
  const c = conn.data;
  const apps = useMemo(() => (project.data?.resources ?? []).filter(r => r.id !== resource.id && r.type !== 'database'), [project.data, resource.id]);
  if (!c) return <section className="ov-card db-conn"><header><h2>Connection details</h2></header><p className="db-muted db-pad db-padded">Loading…</p></section>;

  const outside = from === 'outside' && resource.hostPort;
  const host = outside ? 'localhost' : c.host, port = outside ? resource.hostPort : c.port;
  const password = c.url ? (() => { try { return decodeURIComponent(new URL(c.url).password); } catch { return ''; } })() : '';
  const fullUrl = c.url ? (outside ? withHost(c.url, host, port) : c.url) : null;
  const maskedUrl = outside ? withHost(c.maskedUrl, host, port) : c.maskedUrl;
  const reveal = shown && fullUrl;
  const p = { host, port, user: c.user, database: c.database, tls: c.tls, password: reveal ? password : '<password>', url: reveal ? fullUrl : maskedUrl.replace(/@/, c.hasPassword ? ':<password>@' : '@') };
  const s = (snippets[c.engine] ?? snippets.postgresql)(p);
  const envText = s.env.filter(([, v]) => v !== '' && v != null).map(([k, v]) => `${k}=${v}`).join('\n');
  const copyText = { url: p.url, env: envText, cli: s.cli, code: s[lang] }[tab];

  return <section className="ov-card db-conn">
    <header><h2>Connection details</h2>
      {!c.external && onImport && <button type="button" className="ov-link" onClick={onImport}>Import data<ArrowRightIcon /></button>}</header>
    <div className="db-conn-top">
      {!c.external && resource.hostPort ? <SegmentedControl label="Connect from" value={from} onChange={setFrom}
        options={[{ value: 'inside', label: 'Inside the project' }, { value: 'outside', label: 'From this computer' }]} />
        : <span className="db-muted">{c.external ? 'Wherever it runs; it must accept connections from this machine.' : `Private: only ${resource.environment} reaches it, as ${c.host}.`}</span>}
      {c.url && <button type="button" className="db-reveal" onClick={() => setShown(v => !v)} aria-pressed={shown}>{shown ? <EyeSlashIcon /> : <EyeIcon />}{shown ? 'Hide password' : 'Show password'}</button>}
    </div>
    <dl className="db-conn-facts">
      <div><dt>Host</dt><dd className="mono">{host}</dd></div>
      <div><dt>Port</dt><dd className="mono">{port}</dd></div>
      {c.user && <div><dt>User</dt><dd className="mono">{c.user}</dd></div>}
      {c.database && <div><dt>Database</dt><dd className="mono">{c.database}</dd></div>}
      <div><dt>Password</dt><dd className="mono">{reveal ? password : c.hasPassword ? '••••••••' : 'none'}</dd></div>
      {c.tls && <div><dt>TLS</dt><dd>Required</dd></div>}
    </dl>
    <div className="db-conn-tabs">
      <SegmentedControl label="Show" value={tab} onChange={setTab} options={TABS} />
      {tab === 'code' && <SegmentedControl label="Language" value={lang} onChange={setLang} options={[{ value: 'node', label: 'Node.js' }, { value: 'python', label: 'Python' }]} />}
      <span className="db-conn-copy"><CopyButton value={copyText} label="Copy" /></span>
    </div>
    <pre className="db-snippet">{copyText}</pre>
    <p className="db-muted db-pad">
      {tab === 'url' && (c.url ? 'Paste it where a connection string goes. The password is shown only to admins.' : 'An admin can show the full URL with its password; the password is also under Environment Variables.')}
      {tab === 'env' && 'The names most libraries read by default. "Use in an app" adds the URL to another resource as a secret.'}
      {tab === 'cli' && (outside || c.external ? 'Run it from this computer with the client installed.' : <>From another container in {resource.environment}, or inside this one: {s.terminal && <><code>{s.terminal}</code>{onNavigate && <> in the <button type="button" className="inline-link" onClick={() => onNavigate('Terminal')}>Terminal</button></>}</>}.</>)}
      {tab === 'code' && 'Reads the variable from the Variables tab, so the secret stays out of your code.'}
    </p>
    <div className="db-conn-actions">
      {apps.length > 0 && <Button icon={LinkIcon} onClick={() => setSharing(true)}>Use in an app</Button>}
      {onNavigate && <Button onClick={() => onNavigate('Environment Variables')}>{c.external ? 'Edit credentials' : 'All settings'}</Button>}
      {c.external && <Button icon={SignalIcon} disabled={test?.state === 'running'} onClick={async () => { setTest({ state: 'running' }); try { setTest(await api.testConnection(resource.id)); } catch (e) { setTest({ ok: false, error: e.message }); } conn.refetch(); }}>
        {test?.state === 'running' ? 'Testing…' : 'Test connection'}</Button>}
    </div>
    {test && test.state !== 'running' && <p className={cx('db-check db-pad db-padded', test.ok ? 'ok' : 'bad')}>{test.ok ? <CheckCircleIcon /> : <XCircleIcon />}{test.ok ? `${test.address} answered in ${test.latencyMs} ms.` : test.error}</p>}
    {sharing && <ShareDialog resource={resource} apps={apps} onClose={() => setSharing(false)} />}
  </section>;
}

function ShareDialog({ resource, apps, onClose }) {
  const [target, setTarget] = useState(apps[0]?.id ?? '');
  const [key, setKey] = useState('DATABASE_URL');
  const [done, setDone] = useState(null);
  const [failure, setFailure] = useState('');
  return <Dialog open onClose={onClose} title={`Use ${resource.name} in an app`} description="Saves the connection URL (with its password) as a secret variable on the app."
    footer={done ? <Button variant="primary" onClick={onClose}>Done</Button> : <>
      {failure && <span className="db-footer-error">{failure}</span>}
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" disabled={!target} onClick={async () => { try { setDone(await api.shareConnection(resource.id, target, key)); } catch (e) { setFailure(Object.values(e.fields ?? {})[0] ?? e.message); } }}>Add variable</Button>
    </>}>
    {done ? <Notice tone="success" title={`${done.key} added to ${done.target}`}>{done.requiresRedeploy ? 'Redeploy it to pick the variable up.' : 'It\'s used on the first deploy.'}</Notice>
      : <div className="db-form">
        <label className="db-label">App<Select label="App" value={target} onChange={setTarget} options={apps.map(a => ({ value: a.id, label: a.name, description: a.type }))} /></label>
        <label className="db-label">Variable name<TextInput className="mono" value={key} onChange={e => setKey(e.target.value.toUpperCase())} spellCheck={false} /></label>
      </div>}
  </Dialog>;
}

export function ImportHistory({ resource }) {
  const { data } = useQuery(() => api.imports(resource.id), [resource.id], { pollMs: 10000 });
  const [open, setOpen] = useState(null);
  const list = data?.imports ?? [];
  if (!list.length) return null;
  return <section className="ov-card">
    <header><h2>Imports</h2></header>
    <ol className="db-history">{list.slice(0, 5).map(i => <li key={i.id}><button type="button" onClick={() => setOpen(i.id)}>
      <Status tone={{ succeeded: 'success', failed: 'danger', running: 'progress' }[i.status] ?? 'neutral'} label={i.status} />
      <span>{i.label || i.source}</span><time>{timeAgo(i.createdAt)}</time>
    </button></li>)}</ol>
    {open && <ImportProgress importId={open} onClose={() => setOpen(null)} />}
  </section>;
}

export const ImportIcon = CloudArrowDownIcon;
export const DatabaseIcon = CircleStackIcon;
