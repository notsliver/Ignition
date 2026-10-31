import { useEffect, useState } from 'react';
import { ArrowPathIcon, ArrowTopRightOnSquareIcon, CheckCircleIcon, CodeBracketIcon, CubeIcon, ExclamationTriangleIcon, KeyIcon, RocketLaunchIcon } from '@heroicons/react/16/solid';
import { Button, GitHubIcon, GitLabIcon, Notice, Select, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { SourceSelect } from '../Resources/AddResourceDialog';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import { PageIntro } from './shared';

/*
 * Source decides what gets fetched: a repository and branch, or an image, plus the saved credentials used to read it.
 * Branches and the latest commit are read live from the remote (git ls-remote). How it's built lives in Build & Deploy.
 */

const hostOf = url => url.replace(/^https:\/\//, '').replace(/^ssh:\/\//, '').replace(/^git@/, '').split(/[/:]/)[0];
const providerIcon = url => hostOf(url).includes('github') ? GitHubIcon : hostOf(url).includes('gitlab') ? GitLabIcon : CodeBracketIcon;
// A browsable https address for the repository, also for git@host:owner/repo.git URLs.
const webUrl = url => url.startsWith('https://') ? url.replace(/\.git$/, '')
  : `https://${url.replace(/^ssh:\/\//, '').replace(/^git@/, '').replace(/:(?!\d)/, '/').replace(/\.git$/, '')}`;
const short = sha => sha?.slice(0, 7);

export function Source({ resource, onResourceChange }) {
  const { user } = useSession();
  const canEdit = can(user, 'resources.update');
  const git = resource.sourceType === 'git';
  const { data, error, loading, refetch } = useQuery(() => api.resourceSource(resource.id), [resource.id]);
  const initial = git ? { repoUrl: resource.repoUrl, branch: resource.branch, sourceId: resource.sourceId ?? '' } : { image: resource.image, sourceId: resource.sourceId ?? '' };
  const [saved, setSaved] = useState(initial);
  const [values, setValues] = useState(initial);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [deploying, setDeploying] = useState(false);
  const dirty = JSON.stringify(values) !== JSON.stringify(saved);
  const set = key => value => { setValues(v => ({ ...v, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); };
  useEffect(() => { if (!dirty) { setSaved(initial); setValues(initial); } }, [resource]); // eslint-disable-line react-hooks/exhaustive-deps

  async function save(event) {
    event.preventDefault();
    setFeedback(null);
    try {
      const { resource: updated } = await api.updateResource(resource.id, values);
      setSaved(values);
      onResourceChange?.(updated);
      setFeedback({ tone: 'success', title: 'Source saved', text: 'The next deployment uses it.', deploy: true });
      refetch();
    } catch (err) {
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: err.message });
    }
  }
  async function deploy() {
    setDeploying(true);
    try {
      await api.deploy(resource.id, {});
      setFeedback({ tone: 'success', title: 'Deployment started', text: 'Follow it under Deployments.' });
    } catch (err) {
      setFeedback({ tone: 'danger', title: 'Couldn\'t start the deployment', text: err.message });
    }
    setDeploying(false);
  }

  const deployed = data?.deployed;
  const latest = data?.latestCommit;
  const switched = git && deployed && deployed.branch && deployed.branch !== resource.branch;
  const upToDate = git && !switched && deployed?.commit && latest && latest.startsWith(deployed.commit);
  const branchOptions = (data?.branches ?? []).map(b => ({ value: b.name, label: b.name, description: short(b.commit) }));
  if (git && values.branch && !branchOptions.some(o => o.value === values.branch)) branchOptions.unshift({ value: values.branch, label: values.branch });
  const ProviderIcon = git ? providerIcon(resource.repoUrl) : CubeIcon;
  const field = (key, label, hint, control) => <SettingsField label={label} hint={errors[key] ?? hint} htmlFor={key}>{control}</SettingsField>;

  return <form className="settings-page rp-settings" onSubmit={save}>
    <PageIntro>{git ? 'Ignition clones this repository and builds it with its Dockerfile.' : 'This resource runs a container image instead of building from a repository.'}</PageIntro>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}{feedback.deploy && canEdit && <> <button type="button" className="inline-link" onClick={deploy}>Deploy now</button></>}</Notice>}

    <section className="settings-group">
      <h2>{git ? 'Repository' : 'Image'}</h2>
      <div className="settings-card">
        <div className="source-repo">
          <ProviderIcon className="source-provider" />
          <div className="rp-row-main">
            <strong className="mono">{git ? resource.repoUrl : resource.image}</strong>
            <span>{data?.source ? `Using ${data.source.name}` : git ? 'Public repository, no credentials' : 'Public image, no credentials'}{git && ` · ${resource.branch}`}</span>
          </div>
          {git && <a className="button" href={webUrl(resource.repoUrl)} target="_blank" rel="noreferrer"><ArrowTopRightOnSquareIcon />Open</a>}
        </div>
        <div className="source-tools">
          {git && <Button type="button" icon={ArrowPathIcon} onClick={refetch} disabled={loading}>{loading ? 'Checking…' : 'Check repository'}</Button>}
          {data && !data.error && git && <span className="source-result ok"><CheckCircleIcon />Read access confirmed · {data.branches.length} {data.branches.length === 1 ? 'branch' : 'branches'}</span>}
          {data?.error && <span className="source-result bad"><ExclamationTriangleIcon />{data.error}</span>}
          {error && <span className="source-result bad"><ExclamationTriangleIcon />{error.message}</span>}
          {data?.source?.status === 'failed' && <span className="source-result bad"><KeyIcon />{data.source.name}: {data.source.statusMessage}</span>}
        </div>
      </div>
    </section>

    <section className="settings-group">
      <h2>What's deployed</h2>
      <div className="settings-card source-drift">
        <div className="rp-row-main">
          {!deployed ? <><strong>Nothing deployed yet</strong><span>Deploy to build the current source.</span></>
            : git ? <>
              <strong>{switched ? `Switched to ${resource.branch}, not deployed yet` : !latest ? `Running ${short(deployed.commit) || 'an unknown commit'}` : upToDate ? `Up to date with ${resource.branch}` : `${resource.branch} has newer commits`}</strong>
              <span>Running <code>{short(deployed.commit) || '—'}</code>{deployed.branch && ` from ${deployed.branch}`}{latest && <> · latest is <code>{short(latest)}</code></>} · deployed {timeAgo(deployed.at)}</span>
            </> : <>
              <strong className="mono">{deployed.imageRef || resource.image}</strong>
              <span>Deployed {timeAgo(deployed.at)}</span>
            </>}
        </div>
        {canEdit && (!deployed || (git && (switched || (latest && !upToDate)))) && <Button type="button" variant="primary" icon={RocketLaunchIcon} disabled={deploying || dirty} onClick={deploy}>{deployed ? 'Deploy latest' : 'Deploy'}</Button>}
      </div>
    </section>

    <SettingsGroup title={git ? 'Source reference' : 'Image settings'}>
      {git ? <>
        {field('repoUrl', 'Repository URL', 'https URL, or git@host:owner/repo.git with SSH-key credentials.',
          <TextInput id="repoUrl" className="mono" value={values.repoUrl} disabled={!canEdit} spellCheck={false} onChange={e => set('repoUrl')(e.target.value)} />)}
        {field('branch', 'Branch', data?.branches.length ? 'Branches are read from the repository.' : 'Type a branch name; the list appears once the repository can be read.',
          branchOptions.length > 1
            ? <Select id="branch" label="Branch" value={values.branch} options={branchOptions} onChange={set('branch')} searchable={6} />
            : <TextInput id="branch" className="mono" value={values.branch} disabled={!canEdit} onChange={e => set('branch')(e.target.value)} />)}
      </> : field('image', 'Image', 'Name and tag, e.g. nginx:1.27-alpine or ghcr.io/owner/app:1.2.',
        <TextInput id="image" className="mono" value={values.image} disabled={!canEdit} spellCheck={false} onChange={e => set('image')(e.target.value)} />)}
      {field('sourceId', git ? 'Credentials' : 'Pull credentials', 'Saved under Infrastructure › Sources. None means public.',
        canEdit ? <SourceSelect id="sourceId" kind={git ? 'git' : 'registry'} value={values.sourceId} onChange={set('sourceId')} /> : <TextInput id="sourceId" value={data?.source?.name ?? 'None'} disabled readOnly />)}
    </SettingsGroup>

    {git && <p className="rp-footnote">Deploys run when you start them. Auto-deploy on push needs webhooks from your Git host, which can't reach a self-hosted Ignition unless it's exposed to the internet, so it isn't supported yet. Tags and pinned commits aren't supported yet either.</p>}

    <SaveBar dirty={dirty} onDiscard={() => { setValues(saved); setErrors({}); }} summary="Source changes apply to the next deployment" />
  </form>;
}
