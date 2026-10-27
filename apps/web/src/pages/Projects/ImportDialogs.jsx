import { useEffect, useRef, useState } from 'react';
import {
  ArrowPathIcon, CheckCircleIcon, ChevronDownIcon, ChevronRightIcon, CircleStackIcon, CodeBracketIcon, CogIcon, CubeIcon,
  DocumentDuplicateIcon, DocumentTextIcon, ExclamationTriangleIcon, FolderPlusIcon, MagnifyingGlassIcon,
} from '@heroicons/react/16/solid';
import { BranchIcon, Button, Checkbox, Dialog, Notice, Select, Switch, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { SourceSelect } from '../project/Resources/AddResourceDialog';
import './ImportDialogs.css';

/*
 * New project › Import from Git repository, From a Docker Compose file, Duplicate an existing project.
 * Each shows exactly what it will create before anything is created.
 */

const TYPE_ICON = { application: CubeIcon, database: CircleStackIcon, worker: CogIcon };
const slug = s => s.toLowerCase().replace(/\.git$/, '').split(/[/:]/).pop().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '') || 'project';
const PROVIDERS = {
  github: { host: 'github.com', label: 'GitHub', placeholder: 'owner/repository' },
  gitlab: { host: 'gitlab.com', label: 'GitLab', placeholder: 'group/project' },
  other: { host: '', label: 'Git', placeholder: 'https://git.example.com/team/app.git' },
};

function ServicePlan({ plan, selected, onToggle, needsGit }) {
  const [open, setOpen] = useState(null);
  return <div className="pi-services">
    {plan.warnings?.length > 0 && <Notice tone="warning" title="Before you import">{plan.warnings.join(' ')}</Notice>}
    <ul>
      {plan.services.map(s => {
        const Icon = TYPE_ICON[s.type] ?? CubeIcon;
        const blocked = needsGit && s.build && !s.image;
        const on = selected.includes(s.name) && !blocked;
        return <li key={s.name} className={cx(!on && 'off')}>
          <div className="pi-svc">
            <Checkbox label={`Import ${s.name}`} checked={on} onChange={() => !blocked && onToggle(s.name)} />
            <Icon className="pi-svc-icon" />
            <span className="pi-svc-main"><strong>{s.name}</strong>
              <span>{s.type}{s.port ? ` · port ${s.port}` : ''}{s.build ? ` · builds ${s.build.context === '.' ? 'the repository' : s.build.context}` : ` · ${s.image}`}{s.volumes.length ? ` · ${s.volumes.length} volume${s.volumes.length === 1 ? '' : 's'}` : ''}{s.env.length ? ` · ${s.env.length} variable${s.env.length === 1 ? '' : 's'}` : ''}</span></span>
            {s.warnings.length > 0 && <button type="button" className="pi-warn" onClick={() => setOpen(open === s.name ? null : s.name)} aria-expanded={open === s.name}>
              <ExclamationTriangleIcon />{s.warnings.length}{open === s.name ? <ChevronDownIcon /> : <ChevronRightIcon />}</button>}
          </div>
          {blocked && <p className="pi-svc-note">Builds from source, which needs its repository. Import from Git instead, or leave it out.</p>}
          {open === s.name && <ul className="pi-svc-warnings">{s.warnings.map(w => <li key={w}>{w}</li>)}</ul>}
        </li>;
      })}
    </ul>
    <p className="pi-muted">Each service becomes a resource on the project's private network and answers to its name, so services keep finding each other like they did under Compose.</p>
  </div>;
}

/* ---------------------------------- Git ---------------------------------- */

export function ImportGitDialog({ provider = 'github', onClose, onCreated }) {
  const p = PROVIDERS[provider] ?? PROVIDERS.other;
  const [repo, setRepo] = useState('');
  const [branch, setBranch] = useState('main');
  const [sourceId, setSourceId] = useState('');
  const [inspection, setInspection] = useState(null);
  const [inspecting, setInspecting] = useState(false);
  const [name, setName] = useState('');
  const [selected, setSelected] = useState([]);
  const [mode, setMode] = useState('compose');
  const [port, setPort] = useState('');
  const [deploy, setDeploy] = useState(true);
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  const repoUrl = p.host ? `https://${p.host}/${repo.trim().replace(/^\/+|\.git$/g, '')}` : repo.trim();

  async function inspect() {
    if (!repo.trim()) { setErrors({ repoUrl: 'Enter the repository.' }); return; }
    setInspecting(true);
    setErrors({});
    setInspection(null);
    try {
      const out = await api.inspectGitImport({ repoUrl, branch, sourceId });
      setInspection(out);
      setName(n => n || out.suggestedName);
      setSelected(out.plan?.services.map(s => s.name) ?? []);
      setMode(out.plan ? 'compose' : 'single');
      setPort(out.port ? String(out.port) : '');
    } catch (e) {
      setErrors(Object.keys(e.fields ?? {}).length ? e.fields : { repoUrl: e.message });
    }
    setInspecting(false);
  }
  async function create() {
    setPending(true);
    setErrors({});
    try {
      const body = { kind: 'git', name, repoUrl, branch, sourceId, deploy, ...(mode === 'compose' ? { services: selected } : { port: port ? Number(port) : null }) };
      const out = await api.importProject(body);
      onCreated(out.project);
    } catch (e) {
      setErrors(Object.keys(e.fields ?? {}).length ? e.fields : { form: e.message });
      setPending(false);
    }
  }
  const firstError = Object.values(errors)[0];
  const ins = inspection;
  return <Dialog open onClose={onClose} className="pi-dialog" title={`Import from ${p.label}`}
    description="IGNITION looks at the repository first: a compose file becomes one resource per service; otherwise the repository becomes one application built from its Dockerfile or detected stack."
    footer={<>
      {firstError && <span className="pi-error" role="alert">{firstError}</span>}
      <Button onClick={onClose}>Cancel</Button>
      {ins ? <Button variant="primary" icon={FolderPlusIcon} disabled={pending || !name.trim() || (mode === 'compose' && !selected.length)} onClick={create}>{pending ? 'Creating…' : 'Create project'}</Button>
        : <Button variant="primary" icon={inspecting ? ArrowPathIcon : MagnifyingGlassIcon} disabled={inspecting} onClick={inspect}>{inspecting ? 'Looking at the repository…' : 'Continue'}</Button>}
    </>}>
    <div className="pi-form">
      <div className="pi-row">
        <label className="pi-label pi-grow">Repository<TextInput className="mono" prefix={p.host ? `${p.host}/` : undefined} value={repo} placeholder={p.placeholder} autoFocus
          onChange={e => { setRepo(e.target.value); setInspection(null); setErrors({}); }} onKeyDown={e => e.key === 'Enter' && !ins && inspect()} aria-invalid={!!errors.repoUrl} spellCheck={false} /></label>
        <label className="pi-label pi-branch">Branch<TextInput className="mono" prefix={<BranchIcon />} value={branch} onChange={e => { setBranch(e.target.value); setInspection(null); }} spellCheck={false} /></label>
      </div>
      <label className="pi-label">Credentials<SourceSelect kind="git" value={sourceId} onChange={v => { setSourceId(v); setInspection(null); }} /></label>

      {ins && <div className="pi-found">
        <p className="pi-found-head"><CheckCircleIcon />Checked out <code>{ins.commit.slice(0, 7)}</code> of {branch}.
          {ins.plan ? <> Found <code>{ins.composeFile}</code> with {ins.plan.services.length} service{ins.plan.services.length === 1 ? '' : 's'}.</>
            : ins.dockerfile ? ' Found a Dockerfile.' : ins.stack ? ` Detected ${ins.stack}.` : ''}</p>
        {ins.plan && (ins.dockerfile || ins.stack) && <div className="pi-choice">
          <label className="check-row"><input type="radio" checked={mode === 'compose'} onChange={() => setMode('compose')} />Use the compose file ({ins.plan.services.length} resources)</label>
          <label className="check-row"><input type="radio" checked={mode === 'single'} onChange={() => setMode('single')} />One application from the {ins.dockerfile ? 'Dockerfile' : 'detected stack'}</label>
        </div>}
        {mode === 'compose' && ins.plan
          ? <ServicePlan plan={ins.plan} selected={selected} onToggle={n => setSelected(s => s.includes(n) ? s.filter(x => x !== n) : [...s, n])} />
          : <div className="pi-single">
            <CubeIcon /><div><strong>{ins.suggestedName}</strong><span>{ins.dockerfile ? 'Built with the repository\'s Dockerfile' : ins.stack ? `${ins.stack}: ${ins.stackSummary}` : 'No Dockerfile or known stack'}</span></div>
            <label className="pi-label pi-port">Port<TextInput inputMode="numeric" value={port} placeholder="3000" onChange={e => setPort(e.target.value.replace(/\D/g, '').slice(0, 5))} /></label>
          </div>}
        {ins.hasEnvExample && <p className="pi-muted">Variables were filled from <code>.env.example</code>; review them on each resource before relying on them.</p>}
        <div className="pi-row">
          <label className="pi-label pi-grow">Project name<TextInput value={name} onChange={e => setName(e.target.value)} aria-invalid={!!errors.name} /></label>
          <label className="check-row pi-deploy"><Switch label="Deploy" checked={deploy} onChange={setDeploy} />Deploy right away</label>
        </div>
      </div>}
    </div>
  </Dialog>;
}

/* -------------------------------- Compose -------------------------------- */

export function ImportComposeDialog({ onClose, onCreated }) {
  const [yaml, setYaml] = useState('');
  const [env, setEnv] = useState('');
  const [showEnv, setShowEnv] = useState(false);
  const [plan, setPlan] = useState(null);
  const [parseError, setParseError] = useState('');
  const [selected, setSelected] = useState([]);
  const [name, setName] = useState('');
  const [deploy, setDeploy] = useState(true);
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState('');
  const fileInput = useRef();
  const timer = useRef();

  useEffect(() => {
    clearTimeout(timer.current);
    if (!yaml.trim()) { setPlan(null); setParseError(''); return undefined; }
    timer.current = setTimeout(async () => {
      try {
        const out = await api.previewComposeImport({ compose: yaml, env });
        setPlan(out.plan);
        setParseError('');
        setName(n => n || 'my-stack');
        setSelected(sel => {
          const names = out.plan.services.filter(s => s.image).map(s => s.name);
          return sel.length ? sel.filter(n => names.includes(n)).concat(names.filter(n => !plan?.services.some(s => s.name === n))) : names;
        });
      } catch (e) {
        setPlan(null);
        setParseError(Object.values(e.fields ?? {})[0] ?? e.message);
      }
    }, 400);
    return () => clearTimeout(timer.current);
  }, [yaml, env]); // eslint-disable-line react-hooks/exhaustive-deps

  async function loadFile(f, into) {
    const text = await f.text();
    if (into === 'env') { setEnv(text); setShowEnv(true); } else { setYaml(text); }
  }
  async function create() {
    setPending(true);
    setFailure('');
    try {
      const out = await api.importProject({ kind: 'compose', name, compose: yaml, env, services: selected, deploy });
      onCreated(out.project);
    } catch (e) {
      setFailure(Object.values(e.fields ?? {})[0] ?? e.message);
      setPending(false);
    }
  }
  return <Dialog open onClose={onClose} className="pi-dialog" title="From a Docker Compose file"
    description="Paste or upload docker-compose.yml. Services with an image are created as they are; services that build from source need the repository (use Import from Git)."
    footer={<>
      {failure && <span className="pi-error" role="alert">{failure}</span>}
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" icon={FolderPlusIcon} disabled={pending || !plan || !selected.length || !name.trim()} onClick={create}>{pending ? 'Creating…' : `Create project${selected.length ? ` with ${selected.length} resource${selected.length === 1 ? '' : 's'}` : ''}`}</Button>
    </>}>
    <div className="pi-form">
      <div className={cx('pi-yaml', parseError && 'invalid')}
        onDragOver={e => e.preventDefault()} onDrop={e => { e.preventDefault(); const f = e.dataTransfer.files[0]; if (f) loadFile(f, /\.env/.test(f.name) ? 'env' : 'yaml'); }}>
        <div className="pi-yaml-bar"><DocumentTextIcon /><span>docker-compose.yml</span><button type="button" className="inline-link" onClick={() => fileInput.current.click()}>Open a file…</button></div>
        <textarea value={yaml} onChange={e => setYaml(e.target.value)} placeholder={'services:\n  web:\n    image: ghcr.io/acme/web:1.4\n    ports: ["3000:3000"]\n    environment:\n      DATABASE_URL: postgres://app:${DB_PASSWORD}@db:5432/app\n  db:\n    image: postgres:16\n    volumes: [pgdata:/var/lib/postgresql/data]'}
          rows={12} spellCheck={false} aria-label="Compose file" autoFocus />
        <input ref={fileInput} type="file" accept=".yml,.yaml" hidden onChange={e => e.target.files[0] && loadFile(e.target.files[0], 'yaml')} />
      </div>
      {parseError && <p className="pi-error-line"><ExclamationTriangleIcon />{parseError}</p>}
      <button type="button" className="pi-toggle" onClick={() => setShowEnv(s => !s)} aria-expanded={showEnv}>{showEnv ? <ChevronDownIcon /> : <ChevronRightIcon />}Values for ${'{VARIABLES}'} (.env){env.trim() && ` · ${env.trim().split('\n').filter(l => l.includes('=')).length} set`}</button>
      {showEnv && <textarea className="pi-env mono" value={env} onChange={e => setEnv(e.target.value)} rows={4} placeholder={'DB_PASSWORD=change-me\nAPI_KEY=…'} spellCheck={false} aria-label=".env values" />}
      {plan && <>
        <ServicePlan plan={plan} selected={selected} needsGit onToggle={n => setSelected(s => s.includes(n) ? s.filter(x => x !== n) : [...s, n])} />
        <div className="pi-row">
          <label className="pi-label pi-grow">Project name<TextInput value={name} placeholder="my-stack" onChange={e => setName(e.target.value)} /></label>
          <label className="check-row pi-deploy"><Switch label="Deploy" checked={deploy} onChange={setDeploy} />Deploy right away</label>
        </div>
      </>}
    </div>
  </Dialog>;
}

/* ------------------------------- Duplicate ------------------------------- */

export function DuplicateProjectDialog({ projects, project: preset, onClose, onCreated }) {
  const [sourceId, setSourceId] = useState(preset?.id ?? projects[0]?.id ?? '');
  const source = projects.find(p => p.id === sourceId);
  const [name, setName] = useState(preset ? `${preset.name} copy` : projects[0] ? `${projects[0].name} copy` : '');
  const [touched, setTouched] = useState(!!preset);
  const [vars, setVars] = useState(false);
  const [deploy, setDeploy] = useState(false);
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState('');
  const [done, setDone] = useState(null);

  async function run() {
    setPending(true);
    setFailure('');
    try {
      setDone(await api.duplicateProject(sourceId, { name, includeVariables: vars, deploy }));
    } catch (e) {
      setFailure(Object.values(e.fields ?? {})[0] ?? e.message);
    }
    setPending(false);
  }
  if (done) return <Dialog open onClose={() => onCreated(done.project)} title={`${done.project.name} is ready`} icon={DocumentDuplicateIcon}
    description={`${done.resources} resource${done.resources === 1 ? '' : 's'} copied${done.deployed ? `, ${done.deployed} deploying` : ''}.`}
    footer={<Button variant="primary" onClick={() => onCreated(done.project)}>Open project</Button>}>
    <ul className="pi-notes">{done.notes.map(n => <li key={n}>{n}</li>)}</ul>
  </Dialog>;
  return <Dialog open onClose={onClose} title="Duplicate a project" description="Copies its environments and resources with their settings: images and repositories, build settings, limits, ports, networks, volumes (empty), health checks and variables."
    footer={<>
      {failure && <span className="pi-error" role="alert">{failure}</span>}
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" icon={DocumentDuplicateIcon} disabled={pending || !source || !name.trim()} onClick={run}>{pending ? 'Copying…' : 'Duplicate'}</Button>
    </>}>
    <div className="pi-form">
      {projects.length === 0 ? <p className="pi-muted">There's no project to duplicate yet.</p> : <>
        <label className="pi-label">Copy<Select label="Project" value={sourceId} onChange={id => { setSourceId(id); if (!touched) setName(`${projects.find(p => p.id === id)?.name} copy`); }}
          options={projects.map(p => ({ value: p.id, label: p.name, description: `${p.resourceCount} resource${p.resourceCount === 1 ? '' : 's'} · ${p.environments.length} environment${p.environments.length === 1 ? '' : 's'}` }))} searchable={8} /></label>
        <label className="pi-label">New name<TextInput value={name} onChange={e => { setName(e.target.value); setTouched(true); }} /></label>
        <label className="check-row"><Checkbox label="Copy secret values" checked={vars} onChange={setVars} /><span>Copy secret values too <span className="pi-muted">(otherwise they're created empty, so a copy can't talk to production by accident)</span></span></label>
        <label className="check-row"><Checkbox label="Deploy" checked={deploy} onChange={setDeploy} />Deploy every resource after copying</label>
        <p className="pi-muted">Not copied: the data inside volumes, domains (hostnames are unique), deployment history, tokens, hooks and automations. Fixed host ports become automatic so the copy doesn't clash with the original.</p>
      </>}
    </div>
  </Dialog>;
}

export { CodeBracketIcon };
