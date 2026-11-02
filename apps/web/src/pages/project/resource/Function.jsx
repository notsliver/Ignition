import { Suspense, lazy, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowPathIcon, CheckCircleIcon, ExclamationTriangleIcon, PaperAirplaneIcon, RocketLaunchIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Notice, SegmentedControl, Select, Status, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { publicUrl } from '../Resources/Resources';
import { CopyButton } from './shared';
import './Function.css';

/*
 * Functions: one handler file, run by a small built-in server in the official Node.js or Python image.
 * The Code page edits the handler, deploys it, and sends test requests from inside the private network.
 */

const RUNTIMES = [{ value: 'node', label: 'Node.js 22' }, { value: 'bun', label: 'Bun 1.2' }, { value: 'python', label: 'Python 3.12' }];
const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => ({ value: m, label: m }));

const Monaco = lazy(() => import('./files/CodeEditor'));
let editorCount = 0;

/**
 * The same editor as Files (VS Code's Monaco), sized for a page or dialog. path picks the language;
 * a value changed from outside (a new starter template) replaces the text.
 */
export function CodeEditor({ value, onChange, path = 'handler.js', label, height = 360, invalid, readOnly, onSave }) {
  const [base] = useState(() => `inline-${++editorCount}`);
  const key = `${base}:${path}`;
  const emitted = useRef(value);
  useEffect(() => () => { import('./files/CodeEditor').then(m => m.disposeModel(key)); }, [key]);
  useEffect(() => {
    if (value !== emitted.current) { emitted.current = value; import('./files/CodeEditor').then(m => m.resetModel(key, value)); }
  }, [value, key]);
  return <div className={cx('fn-editor', invalid && 'invalid')} style={{ height }} role="group" aria-label={label}>
    <Suspense fallback={<p className="fn-editor-loading">Loading editor…</p>}>
      <Monaco modelKey={key} path={path} value={value} readOnly={readOnly} minimap={false} onSave={onSave}
        onChange={v => { emitted.current = v; onChange?.(v); }} />
    </Suspense>
  </div>;
}

export function FunctionDialog({ projectName, environment, takenNames, onClose, onCreate }) {
  const engines = useQuery(() => api.databaseEngines(), []);
  const runtimes = engines.data?.runtimes ?? [];
  const [runtime, setRuntime] = useState('node');
  const [name, setName] = useState(() => ['hello', 'api', 'webhook', 'fn'].find(n => !takenNames.includes(n)) ?? 'fn');
  const [code, setCode] = useState('');
  const [touched, setTouched] = useState(false);
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  const rt = runtimes.find(r => r.id === runtime);
  useEffect(() => { if (rt && !touched) setCode(rt.template); }, [rt, touched]);

  async function create() {
    const f = {};
    if (!/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(name)) f.name = 'Use lowercase letters, numbers and dashes.';
    else if (takenNames.includes(name)) f.name = 'A resource with this name already exists.';
    setErrors(f);
    if (Object.keys(f).length) return;
    setPending(true);
    try {
      await onCreate({ name, type: 'function', engine: runtime, code, memoryMb: 256, cpus: 0.5 });
    } catch (e) {
      setErrors(Object.keys(e.fields ?? {}).length ? e.fields : { form: e.message });
      setPending(false);
    }
  }
  return <Dialog open onClose={onClose} className="fn-dialog" title="New function" description="A single handler that answers HTTP requests. No Dockerfile or build: it runs in the official runtime image and redeploys in seconds."
    footer={<>
      {Object.values(errors)[0] && <span className="fn-error">{Object.values(errors)[0]}</span>}
      <span className="fn-target">{projectName} / {environment}</span>
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" icon={RocketLaunchIcon} disabled={pending || !rt} onClick={create}>{pending ? 'Deploying…' : 'Deploy function'}</Button>
    </>}>
    <div className="fn-form">
      <div className="fn-row">
        <label className="fn-label">Name<TextInput className="mono" value={name} onChange={e => { setName(e.target.value); setErrors({}); }} aria-invalid={!!errors.name} /></label>
        <label className="fn-label">Runtime<SegmentedControl label="Runtime" value={runtime} onChange={setRuntime} options={RUNTIMES} /></label>
      </div>
      {touched && rt && !code.includes(rt.id === 'python' ? 'def handler' : 'export') && <p className="fn-muted">This code doesn't look like a {rt.label} handler. <button type="button" className="inline-link" onClick={() => { setCode(rt.template); setTouched(false); }}>Use the {rt.label} starter</button></p>}
      <div className="fn-label">{rt?.file ?? 'Handler'}<CodeEditor label="Handler code" path={rt?.file ?? { python: 'handler.py', bun: 'handler.ts' }[runtime] ?? 'handler.js'} value={code} onChange={v => { setCode(v); setTouched(true); }} height={320} /></div>
      <p className="fn-muted">Listens on port 8080 on the project's private network as <code>{name || 'name'}:8080</code>. Add a domain afterwards to make it public. Logs show every request.</p>
    </div>
  </Dialog>;
}

export function FunctionCode({ resource, onNavigate, onResourceChange }) {
  const { user } = useSession();
  const canEdit = can(user, 'resources.update');
  const { data, error, refetch } = useQuery(() => api.fn(resource.id), [resource.id]);
  const [code, setCode] = useState(null);
  const [saving, setSaving] = useState(false);
  const [feedback, setFeedback] = useState(null);
  useEffect(() => { if (data && code === null) setCode(data.code || data.template); }, [data]); // eslint-disable-line react-hooks/exhaustive-deps
  const dirty = data && code !== null && code !== (data.code || data.template);
  const size = useMemo(() => new Blob([code ?? '']).size, [code]);

  useEffect(() => {
    const save = e => { if ((e.metaKey || e.ctrlKey) && e.key === 's') { e.preventDefault(); if (dirty && canEdit) deploy(); } };
    window.addEventListener('keydown', save);
    return () => window.removeEventListener('keydown', save);
  });

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load the function">{error.message}</Notice></div>;
  if (!data || code === null) return <div className="rp-page"><p className="fn-muted">Loading…</p></div>;

  async function deploy() {
    setSaving(true);
    setFeedback(null);
    try {
      await api.putFunction(resource.id, { code, deploy: true });
      await refetch();
      setFeedback({ tone: 'success', title: 'Saved and deploying', text: 'It takes over once the new version starts. Follow it under Deployments.' });
      const { resource: r } = await api.resource(resource.id);
      onResourceChange?.(r);
    } catch (e) {
      setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: Object.values(e.fields ?? {})[0] ?? e.message });
    }
    setSaving(false);
  }
  const url = publicUrl(resource);

  return <div className="rp-page fn-page">
    <div className="fn-bar">
      <span className="fn-file mono">{data.file}</span>
      <span className="fn-muted">{data.runtimeLabel} · {Math.round(size / 1024 * 10) / 10} KB of {data.maxBytes / 1024} KB</span>
      <Status tone={{ running: 'success', deploying: 'progress', failed: 'danger' }[resource.status] ?? 'neutral'}>{resource.status[0].toUpperCase() + resource.status.slice(1)}</Status>
      {canEdit && <Button variant="primary" icon={RocketLaunchIcon} disabled={!dirty || saving || size > data.maxBytes} onClick={deploy} title="Ctrl/⌘+S">{saving ? 'Deploying…' : dirty ? 'Save and deploy' : 'Saved'}</Button>}
    </div>
    {feedback && <Notice tone={feedback.tone} title={feedback.title} action={feedback.tone === 'success' ? 'Deployments' : undefined} onAction={() => onNavigate?.('Deployments')} onDismiss={() => setFeedback(null)}>{feedback.text}</Notice>}
    <CodeEditor label="Handler code" path={data.file} value={code} onChange={setCode} readOnly={!canEdit} height="min(62vh, 560px)" invalid={size > data.maxBytes} onSave={() => { if (dirty && canEdit && size <= data.maxBytes) deploy(); }} />
    <div className="fn-addr">
      <span>Private: <code>{resource.name}:8080</code><CopyButton value={`http://${resource.name}:8080`} label="Copy private URL" /></span>
      {url ? <span>Public: <a href={url} target="_blank" rel="noreferrer">{url.replace(/^https?:\/\//, '')}</a><CopyButton value={url} label="Copy URL" /></span>
        : <span className="fn-muted">Not public yet. <button type="button" className="inline-link" onClick={() => onNavigate?.('Domains')}>Add a domain</button></span>}
    </div>
    <TestPanel resource={resource} disabled={!canEdit} />
  </div>;
}

function TestPanel({ resource, disabled }) {
  const [method, setMethod] = useState('GET');
  const [path, setPath] = useState('/?name=ignition');
  const [body, setBody] = useState('{\n  "hello": "world"\n}');
  const [result, setResult] = useState(null);
  const [busy, setBusy] = useState(false);
  const [showHeaders, setShowHeaders] = useState(false);
  async function send() {
    setBusy(true);
    try {
      const { result: r } = await api.invokeFunction(resource.id, { method, path, body: method === 'GET' || method === 'DELETE' ? '' : body });
      setResult(r);
    } catch (e) { setResult({ error: e.message }); }
    setBusy(false);
  }
  const pretty = useMemo(() => {
    if (!result?.body) return '';
    try { return JSON.stringify(JSON.parse(result.body), null, 2); } catch { return result.body; }
  }, [result]);
  const ok = result?.status >= 200 && result?.status < 400;
  return <section className="fn-test">
    <h2>Test</h2>
    <div className="fn-request">
      <div className="fn-method-wrap"><Select label="Method" value={method} onChange={setMethod} options={METHODS} /></div>
      <TextInput className="mono" value={path} onChange={e => setPath(e.target.value)} aria-label="Path" onKeyDown={e => e.key === 'Enter' && send()} spellCheck={false} />
      <Button icon={busy ? ArrowPathIcon : PaperAirplaneIcon} disabled={busy || disabled || resource.status !== 'running'} onClick={send} title={resource.status !== 'running' ? 'Deploy it first' : undefined}>{busy ? 'Sending…' : 'Send'}</Button>
    </div>
    {method !== 'GET' && method !== 'DELETE' && <CodeEditor label="Request body" path="body.json" value={body} onChange={setBody} height={120} />}
    {result && (result.error
      ? <p className="fn-result bad"><ExclamationTriangleIcon />{result.error}</p>
      : <div className="fn-response">
        <div className="fn-response-head">
          <span className={cx('fn-status', ok ? 'ok' : 'bad')}>{ok ? <CheckCircleIcon /> : <ExclamationTriangleIcon />}{result.status}</span>
          <span className="fn-muted">{result.durationMs} ms · {result.headers['content-type'] ?? 'no content type'}</span>
          <button type="button" className="inline-link" onClick={() => setShowHeaders(s => !s)}>{showHeaders ? 'Hide headers' : 'Headers'}</button>
        </div>
        {showHeaders && <pre className="fn-headers">{Object.entries(result.headers).map(([k, v]) => `${k}: ${v}`).join('\n')}</pre>}
        <pre className="fn-out">{pretty || '(empty body)'}{result.truncated && '\n… cut at 64 KB'}</pre>
      </div>)}
    <p className="fn-muted">Sent from inside the private network, the same way other resources call it. Errors in your handler come back as 500 with the message; the full stack is in Logs.</p>
  </section>;
}
