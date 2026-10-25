import { useState } from 'react';
import { ArrowPathIcon, ArrowTopRightOnSquareIcon, CheckCircleIcon, CodeBracketIcon, ExclamationTriangleIcon, RocketLaunchIcon, ShieldCheckIcon } from '@heroicons/react/16/solid';
import { Avatar, BranchIcon, Button, CommitIcon, Dialog, FormField, GitHubIcon, GitLabIcon, Notice, SegmentedControl, Select, Switch, TextInput } from '../../../ui';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import { PageIntro, randomId, useLater, useSettingsForm } from './shared';

/* Source decides what gets fetched (repository, ref, path or image). How it's built lives in Build & Deploy. */

const providerOf = source => source?.startsWith('gitlab.com') ? { name: 'GitLab', icon: GitLabIcon } : source?.startsWith('github.com') ? { name: 'GitHub', icon: GitHubIcon } : { name: 'Git', icon: CodeBracketIcon };
const opts = list => list.map(v => (Array.isArray(v) ? { value: v[0], label: v[1], description: v[2] } : { value: v, label: v }));
const branches = ['main', 'develop', 'release', 'staging', 'feat/core-update-rate-limits', 'feat/primary-flow'];
const tags = ['v2.4.1', 'v2.4.0', 'v2.3.9'];

export function Source({ resource }) {
  return resource.type === 'database' || !resource.branch ? <ImageSource resource={resource} /> : <GitSource resource={resource} />;
}

function GitSource({ resource }) {
  const later = useLater();
  const form = useSettingsForm({ refType: 'branch', branch: resource.branch, tag: tags[0], commit: '', rootDir: '', watchPaths: '', autoDeploy: true, previews: false });
  const { values, set, bind, bindInput } = form;
  const [repo, setRepo] = useState(resource.source);
  const [connection, setConnection] = useState('ok');
  const [access, setAccess] = useState(null);
  const [synced, setSynced] = useState('3 minutes ago');
  const [changeOpen, setChangeOpen] = useState(false);
  const [draftRepo, setDraftRepo] = useState(repo);
  const [commits, setCommits] = useState(() => [
    { sha: '9c41e0a', message: 'Tighten cart validation', author: 'lena', time: '8 min ago' },
    { sha: '3fd28b1', message: 'Add product variant sitemap entries', author: 'tom', time: '25 min ago' },
    { sha: 'e07a6c5', message: 'Bump stripe to 16.2', author: 'maya', time: '1h ago' },
    { sha: resource.commit ?? '4e0b7d1', message: 'Update pricing page copy', author: 'jonas', time: '3h ago', deployed: true },
    { sha: 'f03c6aa', message: 'Bump dependencies', author: 'admin', time: 'Yesterday' },
    { sha: '7b1a002', message: 'Fix hydration warning on dashboard', author: 'jonas', time: 'Yesterday' },
  ]);
  const [events, setEvents] = useState([
    { tone: 'ok', text: `Push to ${resource.branch} received (9c41e0a)`, time: '8 min ago' },
    { tone: 'ok', text: 'Repository metadata refreshed', time: '3 min ago' },
    { tone: 'ok', text: 'Webhook delivery succeeded (200 in 84ms)', time: '8 min ago' },
  ]);
  const provider = providerOf(repo);
  const ProviderIcon = provider.icon;
  const behind = commits.findIndex(c => c.deployed);
  const ref = values.refType === 'branch' ? values.branch : values.refType === 'tag' ? values.tag : values.commit || 'a commit';
  const log = (tone, text) => setEvents(e => [{ tone, text, time: 'Just now' }, ...e].slice(0, 6));

  function testAccess() {
    setAccess('checking');
    later(() => {
      if (connection === 'expired') { setAccess('failed'); log('bad', 'Access test failed: credentials expired'); return; }
      setAccess('ok');
      log('ok', `Access test passed: read access to ${repo}`);
    }, 1200);
  }
  function refresh() {
    setSynced('Refreshing…');
    later(() => {
      setSynced('Just now');
      setCommits(c => [{ sha: randomId(7).replace(/[^a-f0-9]/g, 'c'), message: 'Improve empty states', author: 'maya', time: 'Just now' }, ...c]);
      log('ok', 'Repository metadata refreshed · 1 new commit');
    }, 1000);
  }
  function deployLatest() {
    setCommits(c => c.map((x, i) => ({ ...x, deployed: i === 0 })));
    log('ok', `Deployment of ${commits[0].sha} started`);
  }

  return <form className="settings-page rp-settings" onSubmit={event => { event.preventDefault(); form.save(); }}>
    {connection === 'expired' && <Notice tone="danger" title={`${provider.name} credentials expired`}>New pushes won't deploy until you reconnect. <button type="button" className="inline-link" onClick={() => { setConnection('ok'); setAccess(null); log('ok', 'Credentials reconnected'); }}>Reconnect {provider.name}</button></Notice>}

    <section className="settings-group">
      <h2>Repository</h2>
      <div className="settings-card">
        <div className="source-repo">
          <ProviderIcon className="source-provider" />
          <div className="rp-row-main"><strong>{repo}</strong><span>{connection === 'ok' ? `Connected through the ${provider.name} app · synced ${synced}` : 'Credentials expired'}</span></div>
          <a className="button" href={`https://${repo}`} target="_blank" rel="noreferrer"><ArrowTopRightOnSquareIcon />Open</a>
          <Button type="button" onClick={() => { setDraftRepo(repo); setChangeOpen(true); }}>Change</Button>
        </div>
        <div className="source-tools">
          <Button type="button" icon={ShieldCheckIcon} onClick={testAccess} disabled={access === 'checking'}>{access === 'checking' ? 'Testing…' : 'Test access'}</Button>
          <Button type="button" icon={ArrowPathIcon} onClick={refresh} disabled={synced === 'Refreshing…'}>Refresh</Button>
          {access === 'ok' && <span className="source-result ok"><CheckCircleIcon />Read access confirmed · webhook active</span>}
          {access === 'failed' && <span className="source-result bad"><ExclamationTriangleIcon />Could not read the repository</span>}
        </div>
      </div>
    </section>

    <section className="settings-group">
      <h2>What's deployed</h2>
      <div className="settings-card source-drift">
        <div className="rp-row-main">
          <strong>{behind > 0 ? `${behind} new ${behind === 1 ? 'commit' : 'commits'} on ${values.branch}` : `Up to date with ${values.branch}`}</strong>
          <span>Running <code>{commits[behind]?.sha}</code> · latest is <code>{commits[0].sha}</code></span>
        </div>
        {behind > 0 && <Button type="button" variant="primary" icon={RocketLaunchIcon} onClick={deployLatest}>Deploy latest</Button>}
      </div>
    </section>

    <SettingsGroup title="Source reference">
      <SettingsField label="Deploy from" hint={`Currently fetching ${ref}.`}>
        <SegmentedControl label="Reference type" options={[{ value: 'branch', label: 'Branch' }, { value: 'tag', label: 'Tag' }, { value: 'commit', label: 'Commit' }]} value={values.refType} onChange={set('refType')} />
      </SettingsField>
      {values.refType === 'branch' && <SettingsField label="Branch" hint="Pushes to this branch can trigger deployments." htmlFor="branch"><Select {...bind('branch')} label="Branch" searchable={5} options={opts(branches)} /></SettingsField>}
      {values.refType === 'tag' && <SettingsField label="Tag" htmlFor="tag"><Select {...bind('tag')} label="Tag" options={opts(tags)} /></SettingsField>}
      {values.refType === 'commit' && <SettingsField label="Commit SHA" hint="Pins the resource to one commit. Auto-deploy is paused." htmlFor="commit"><TextInput className="mono" placeholder="a91f3c2" {...bindInput('commit')} /></SettingsField>}
      <SettingsField label="Root directory" hint="Build context for monorepos. Empty uses the repository root." htmlFor="rootDir"><TextInput className="mono" prefix="/" placeholder="apps/web" {...bindInput('rootDir')} /></SettingsField>
      <SettingsField label="Watch paths" hint="Only deploy when matching files change. Comma-separated globs." htmlFor="watchPaths"><TextInput className="mono" placeholder="src/**, package.json" {...bindInput('watchPaths')} /></SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Triggers">
      <SettingsField label="Auto-deploy on push" hint={values.refType === 'branch' ? `Deploy each push to ${values.branch}.` : 'Only available when deploying from a branch.'}>
        <Switch label="Auto-deploy on push" checked={values.autoDeploy && values.refType === 'branch'} onChange={set('autoDeploy')} disabled={values.refType !== 'branch'} />
      </SettingsField>
      <SettingsField label="Preview deployments" hint="Deploy each pull request to a temporary environment.">
        <Switch label="Preview deployments" checked={values.previews} onChange={set('previews')} />
      </SettingsField>
    </SettingsGroup>

    <section className="settings-group">
      <h2>Recent commits on {values.branch}</h2>
      <div className="settings-card">
        {commits.map((c, i) => <div key={c.sha} className="rp-row source-commit">
          <CommitIcon className="source-commit-icon" />
          <span className="rp-row-main"><strong>{c.message}</strong><span><code>{c.sha}</code> · {c.author} · {c.time}</span></span>
          {c.deployed ? <span className="rp-chip success">Deployed</span> : i < behind ? <span className="rp-chip accent">Not deployed</span> : null}
          <Avatar name={c.author} />
        </div>)}
      </div>
    </section>

    <section className="settings-group">
      <h2>Webhook and sync events</h2>
      <div className="settings-card">
        {events.map((e, i) => <div key={i} className="rp-row source-event">
          {e.tone === 'ok' ? <CheckCircleIcon className="ok" /> : <ExclamationTriangleIcon className="bad" />}
          <span className="rp-row-main"><span className="source-event-text">{e.text}</span></span>
          <span className="rp-row-meta">{e.time}</span>
        </div>)}
      </div>
    </section>

    <SaveBar dirty={form.dirty} onDiscard={form.discard} summary="Source changes apply to the next deployment" />

    <Dialog open={changeOpen} onClose={() => setChangeOpen(false)} title="Change repository" description="The next deployment builds from the new repository. History is kept."
      footer={<><Button type="button" onClick={() => setChangeOpen(false)}>Cancel</Button><Button type="button" variant="primary" disabled={!/^[\w.-]+\/[\w.-]+\/[\w.-]+$/.test(draftRepo.trim())} onClick={() => { setRepo(draftRepo.trim()); setChangeOpen(false); setAccess(null); log('ok', `Repository changed to ${draftRepo.trim()}`); }}>Change</Button></>}>
      <FormField label="Repository" hint="host/owner/name, e.g. github.com/acme/web" htmlFor="new-repo"><TextInput id="new-repo" className="mono" value={draftRepo} onChange={e => setDraftRepo(e.target.value)} /></FormField>
    </Dialog>
  </form>;
}

function ImageSource({ resource }) {
  const later = useLater();
  const [name, tag] = resource.source.includes(' ') ? [resource.source.split(' ')[0].toLowerCase(), resource.source.split(' ')[1]] : resource.source.split(':');
  const form = useSettingsForm({ image: name, tag: tag ?? 'latest', credentials: 'none', autoUpdate: 'off' });
  const { values, bind, bindInput } = form;
  const [check, setCheck] = useState(null);
  const digest = 'sha256:3b1f0c8e9a4d72f5e61c0b9a2d8e47f1c6a5b3d9e2f8a7c4b1d0e9f6a3c2b5d8';

  return <form className="settings-page rp-settings" onSubmit={event => { event.preventDefault(); form.save(); }}>
    <PageIntro>This resource runs a container image instead of building from a repository.</PageIntro>
    <SettingsGroup title="Image">
      <SettingsField label="Image" hint="Docker Hub name, or a full registry path." htmlFor="image"><TextInput className="mono" {...bindInput('image')} /></SettingsField>
      <SettingsField label="Tag" htmlFor="tag"><TextInput className="mono" {...bindInput('tag')} /></SettingsField>
      <SettingsField label="Digest" hint="The exact image currently running."><code className="readonly-value mono source-digest">{digest.slice(0, 19)}…</code></SettingsField>
      <SettingsField label="Pull credentials" htmlFor="credentials"><Select {...bind('credentials')} label="Pull credentials" options={opts([['none', 'None (public image)'], ['dockerhub', 'Docker Hub · acme-bot'], ['ghcr', 'GitHub Container Registry']])} /></SettingsField>
    </SettingsGroup>
    <SettingsGroup title="Updates">
      <SettingsField label="Automatic updates" htmlFor="autoUpdate"><Select {...bind('autoUpdate')} label="Automatic updates" options={opts([['off', 'Off', 'Only when you update manually'], ['patch', 'Patch versions', 'e.g. 16.4.1 → 16.4.2'], ['minor', 'Minor versions', 'e.g. 16.4 → 16.5']])} /></SettingsField>
      <SettingsField label="Check for a newer tag" hint={check?.text ?? 'Compares against the registry.'}>
        {check?.newer && <Button type="button" variant="primary" onClick={() => { form.set('tag')(check.newer); setCheck(null); }}>Use {check.newer}</Button>}
        <Button type="button" icon={ArrowPathIcon} disabled={check?.state === 'checking'} onClick={() => { setCheck({ state: 'checking', text: 'Checking…' }); later(() => setCheck({ state: 'done', newer: `${values.tag}.1`.replace(/\.1\.1$/, '.2'), text: `A newer patch release is available.` }), 1000); }}>Check</Button>
      </SettingsField>
    </SettingsGroup>
    <SaveBar dirty={form.dirty} onDiscard={form.discard} summary={form.dirty ? 'Changing the image restarts the resource' : undefined} />
  </form>;
}
