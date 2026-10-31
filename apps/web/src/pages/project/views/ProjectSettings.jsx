import { useEffect, useState } from 'react';
import { ArchiveBoxIcon, ArrowDownIcon, ArrowUpIcon, DocumentDuplicateIcon, PencilSquareIcon, PlusIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, Status, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { can, useSession } from '../../../app/AuthGate';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import '../../settings/settings.css';
import '../resource/resource.css';
import './views.css';

/*
 * Project settings: name and description, environments (add, rename, reorder; the first is the
 * default), recent activity, and the danger zone (archive, duplicate, delete). Deleting refuses
 * while resources exist, so nothing is left running.
 */

const describe = e => {
  const d = e.detail ?? {};
  const label = {
    'project.create': 'Created the project', 'project.update': 'Changed project settings', 'project.archive': 'Archived the project', 'project.unarchive': 'Restored the project',
    'environment.create': `Added environment ${d.name}`, 'environment.rename': `Renamed environment ${d.from} to ${d.to}`, 'environment.delete': `Deleted environment ${d.name}`,
    'environment.reorder': 'Reordered environments', 'resource.create': 'Created a resource', 'resource.delete': 'Deleted a resource', 'resource.deploy': 'Started a deployment',
    'resource.env.reveal': `Revealed ${d.key}`, 'domain.create': `Added ${d.hostname ?? 'a domain'}`, 'domain.delete': `Removed ${d.hostname ?? 'a domain'}`,
  }[e.action];
  return label ?? e.action.replaceAll('.', ' ').replaceAll('_', ' ');
};

export function ProjectSettings({ project, onProjectChange, onDeleted }) {
  const { user } = useSession();
  const manage = can(user, 'projects.update');
  const saved = { name: project.name, description: project.description ?? '' };
  const [form, setForm] = useState(saved);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [dialog, setDialog] = useState(null);
  useEffect(() => setForm({ name: project.name, description: project.description ?? '' }), [project.id, project.name, project.description]);
  const envs = useQuery(() => api.environments(project.id), [project.id]);
  const activity = useQuery(() => api.projectActivity(project.id), [project.id], { enabled: can(user, 'audit.view') });
  const dirty = form.name !== saved.name || form.description !== saved.description;

  async function refresh(title, text) {
    const { project: p } = await api.project(project.id);
    onProjectChange(p);
    envs.refetch();
    activity.refetch?.();
    if (title) setFeedback({ tone: 'success', title, text });
  }
  async function act(fn, title) {
    setFeedback(null);
    try { await fn(); await refresh(title); } catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t do that', text: e.message }); }
  }
  async function save(e) {
    e.preventDefault();
    try { await api.updateProject(project.id, { name: form.name, description: form.description }); setErrors({}); await refresh('Project saved'); }
    catch (err) { setErrors(err.fields ?? {}); setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: Object.keys(err.fields ?? {}).length ? '' : err.message }); }
  }
  const list = envs.data?.environments ?? [];
  const move = (i, d) => { const ids = list.map(e => e.id); [ids[i], ids[i + d]] = [ids[i + d], ids[i]]; act(() => api.orderEnvironments(project.id, ids), 'Environments reordered'); };

  return <form className="settings-page pv-settings" onSubmit={save}>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    {!manage && <Notice tone="info" title="Read-only">Changing project settings needs the "Update projects" permission.</Notice>}

    <SettingsGroup title="General">
      <SettingsField label="Name" hint={errors.name ?? 'Shown everywhere in Ignition. Must be unique.'} htmlFor="p-name">
        <TextInput id="p-name" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} disabled={!manage} maxLength={60} />
      </SettingsField>
      <SettingsField label="Description" hint={errors.description ?? 'What this project is, for your team.'} htmlFor="p-desc">
        <TextInput id="p-desc" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} disabled={!manage} maxLength={500} placeholder="Storefront, API and their databases" />
      </SettingsField>
      <SettingsField label="Project ID" hint="For the API and support."><span className="readonly-value mono">{project.id}</span></SettingsField>
      <SettingsField label="Created"><span className="readonly-value">{new Date(project.createdAt).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' })}</span></SettingsField>
    </SettingsGroup>

    <section className="settings-group">
      <div className="group-head pv-group-head">
        <div><h2>Environments</h2><p>Each has its own resources and a private network. The first one opens by default.</p></div>
        {manage && <Button type="button" icon={PlusIcon} onClick={() => setDialog({ kind: 'add' })}>Add environment</Button>}
      </div>
      <div className="settings-card">
        <ul className="pv-envs">{list.map((e, i) => <li key={e.id}>
          <span className="pv-env-main"><strong>{e.name}{i === 0 && <span className="rp-chip accent">Default</span>}</strong>
            <small>{e.resources} resource{e.resources === 1 ? '' : 's'} · {e.running} running · {e.domains} domain{e.domains === 1 ? '' : 's'}</small></span>
          {manage && <span className="pv-env-actions">
            <button type="button" className="icon-action" aria-label={`Move ${e.name} up`} disabled={i === 0} onClick={() => move(i, -1)}><ArrowUpIcon /></button>
            <button type="button" className="icon-action" aria-label={`Move ${e.name} down`} disabled={i === list.length - 1} onClick={() => move(i, 1)}><ArrowDownIcon /></button>
            <button type="button" className="icon-action" aria-label={`Rename ${e.name}`} onClick={() => setDialog({ kind: 'rename', env: e })}><PencilSquareIcon /></button>
            <button type="button" className="icon-action danger" aria-label={`Delete ${e.name}`} disabled={e.resources > 0 || list.length === 1}
              title={e.resources > 0 ? 'Move or delete its resources first' : list.length === 1 ? 'A project keeps at least one environment' : 'Delete'} onClick={() => setDialog({ kind: 'delete-env', env: e })}><TrashIcon /></button>
          </span>}
        </li>)}</ul>
      </div>
    </section>

    {can(user, 'audit.view') && <section className="settings-group">
      <h2>Activity</h2>
      <p>The latest changes to this project and its resources.</p>
      <div className="settings-card">
        {(activity.data?.events ?? []).length ? <ul className="pv-activity">{activity.data.events.slice(0, 25).map(e => <li key={e.id}>
          <span><strong>{describe(e)}</strong><small>{e.actor}{e.detail?.name && e.targetType === 'resource' ? ` · ${e.detail.name}` : ''}</small></span>
          <time title={new Date(e.createdAt).toLocaleString()}>{timeAgo(e.createdAt)}</time>
        </li>)}</ul> : <p className="pv-empty">{activity.data ? 'No activity yet.' : 'Loading…'}</p>}
      </div>
    </section>}

    {can(user, 'projects.update') && <SettingsGroup title="Danger zone">
      <SettingsField label={project.archived ? 'Restore project' : 'Archive project'} hint={project.archived ? 'Show it in the project list again.' : 'Hide it from the project list. Resources keep running.'}>
        <Button type="button" icon={ArchiveBoxIcon} onClick={() => act(() => api.updateProject(project.id, { archived: !project.archived }), project.archived ? 'Project restored' : 'Project archived')}>{project.archived ? 'Restore' : 'Archive'}</Button>
      </SettingsField>
      {can(user, 'projects.create') && <SettingsField label="Duplicate project" hint="A copy with the same environments and resource settings, without data, domains or history.">
        <Button type="button" icon={DocumentDuplicateIcon} onClick={() => setDialog({ kind: 'duplicate' })}>Duplicate…</Button>
      </SettingsField>}
      {can(user, 'projects.delete') && <SettingsField label="Delete project" hint={project.resourceCount ? `Delete its ${project.resourceCount} resource${project.resourceCount === 1 ? '' : 's'} first, so nothing is left running.` : 'Permanently removes the project and its environments.'}>
        <Button type="button" className="danger" icon={TrashIcon} disabled={project.resourceCount > 0} onClick={() => setDialog({ kind: 'delete' })}>Delete…</Button>
      </SettingsField>}
    </SettingsGroup>}

    {manage && <SaveBar dirty={dirty} onDiscard={() => { setForm(saved); setErrors({}); }} />}
    {dialog?.kind === 'add' && <NameDialog title="Add an environment" label="Name" placeholder="Staging" submit="Add"
      onClose={() => setDialog(null)} onSubmit={async name => { await api.createEnvironment(project.id, name); setDialog(null); await refresh(`${name} added`); }} />}
    {dialog?.kind === 'rename' && <NameDialog title={`Rename ${dialog.env.name}`} label="Name" initial={dialog.env.name} submit="Rename"
      onClose={() => setDialog(null)} onSubmit={async name => { await api.renameEnvironment(dialog.env.id, name); setDialog(null); await refresh(`Renamed to ${name}`); }} />}
    {dialog?.kind === 'delete-env' && <Dialog open onClose={() => setDialog(null)} title={`Delete ${dialog.env.name}?`} description="It has no resources. Its private network goes with it."
      footer={<><Button type="button" onClick={() => setDialog(null)}>Cancel</Button><Button type="button" className="danger-solid" onClick={() => { const e = dialog.env; setDialog(null); act(() => api.deleteEnvironment(e.id), `${e.name} deleted`); }}>Delete</Button></>} />}
    {dialog?.kind === 'duplicate' && <NameDialog title={`Duplicate ${project.name}`} label="New project name" initial={`${project.name} copy`} submit="Duplicate"
      onClose={() => setDialog(null)} onSubmit={async name => { await api.duplicateProject(project.id, { name }); setDialog(null); setFeedback({ tone: 'success', title: `${name} created`, text: 'Find it under Projects.' }); }} />}
    {dialog?.kind === 'delete' && <DeleteDialog project={project} onClose={() => setDialog(null)} onDeleted={onDeleted} />}
  </form>;
}

function NameDialog({ title, label, initial = '', placeholder, submit, onClose, onSubmit }) {
  const [value, setValue] = useState(initial);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  return <Dialog open onClose={onClose} title={title}
    footer={<><Button type="button" onClick={onClose}>Cancel</Button><Button type="button" variant="primary" disabled={pending || !value.trim()}
      onClick={async () => { setPending(true); try { await onSubmit(value.trim()); } catch (e) { setError(e.fields?.name ?? Object.values(e.fields ?? {})[0] ?? e.message); setPending(false); } }}>{pending ? 'Working…' : submit}</Button></>}>
    <FormField label={label} htmlFor="name-dialog" error={error}><TextInput id="name-dialog" value={value} onChange={e => { setValue(e.target.value); setError(''); }} placeholder={placeholder} autoFocus /></FormField>
  </Dialog>;
}

function DeleteDialog({ project, onClose, onDeleted }) {
  const [typed, setTyped] = useState('');
  const [error, setError] = useState('');
  return <Dialog open onClose={onClose} icon={TrashIcon} title={`Delete ${project.name}?`} description="The project and its environments are removed for good. This can't be undone."
    footer={<><Button type="button" onClick={onClose}>Cancel</Button><Button type="button" className="danger-solid" disabled={typed !== project.name}
      onClick={async () => { try { await api.deleteProject(project.id); onDeleted(); } catch (e) { setError(e.message); } }}>Delete project</Button></>}>
    {error && <Notice tone="danger" title={error} />}
    <FormField label={`Type ${project.name} to confirm`} htmlFor="confirm-delete"><TextInput id="confirm-delete" value={typed} onChange={e => setTyped(e.target.value)} autoFocus autoComplete="off" /></FormField>
  </Dialog>;
}

