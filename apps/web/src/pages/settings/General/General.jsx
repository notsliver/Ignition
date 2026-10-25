import { useEffect, useState } from 'react';
import { Notice, Select, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { SaveBar, SettingsField, SettingsGroup } from '../SettingsField';

/*
 * Instance settings stored by the API. Deployment defaults apply to resources created afterwards;
 * each resource keeps its own copy and can change it. The dashboard URL comes from .env because
 * it also guards against cross-site requests, so it isn't editable here.
 */

const MEMORY = [128, 256, 512, 768, 1024, 2048, 4096].map(v => ({ value: String(v), label: v >= 1024 ? `${v / 1024} GB` : `${v} MB` }));
const CPUS = [0.25, 0.5, 1, 2, 4].map(v => ({ value: String(v), label: `${v} vCPU` }));
const RESTART = [
  { value: 'always', label: 'Always restart', description: 'Also after Docker or this machine restarts' },
  { value: 'on-failure', label: 'Restart a few times', description: 'Then leave it stopped' },
  { value: 'never', label: 'Never restart' },
];
const BUILDERS = [
  { value: 'auto', label: 'Automatic', description: 'Dockerfile if present, otherwise detect the stack' },
  { value: 'dockerfile', label: 'Dockerfile only', description: 'Fail when there is no Dockerfile' },
];
const fmtDate = iso => iso ? new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' }) : '—';
const gb = b => b ? `${(b / (1 << 30)).toFixed(1)} GB` : '—';

const toForm = d => ({ name: d.instance.name, dashboardUrl: d.about.dashboardUrl === d.about.envDashboardUrl ? '' : d.about.dashboardUrl, memoryMb: String(d.defaults.memoryMb), cpus: String(d.defaults.cpus), healthSeconds: String(d.defaults.healthSeconds), stopGraceSeconds: String(d.defaults.stopGraceSeconds), restartPolicy: d.defaults.restartPolicy, buildMode: d.defaults.buildMode });

export function General() {
  const { data, error, refetch } = useQuery(() => api.general(), []);
  const [saved, setSaved] = useState(null);
  const [values, setValues] = useState(null);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const dirty = saved && JSON.stringify(values) !== JSON.stringify(saved);
  useEffect(() => { if (data && !dirty) { const f = toForm(data); setSaved(f); setValues(f); } }, [data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (error && !data) return <div className="settings-page"><Notice tone="danger" title="Couldn't load settings">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data || !values) return <div className="settings-page"><p className="readonly-value">Loading…</p></div>;

  const set = key => value => { setValues(v => ({ ...v, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); setFeedback(null); };
  const bind = key => ({ id: key, value: values[key], 'aria-invalid': !!errors[key], onChange: e => set(key)(e.target.value) });
  const hint = (key, text) => errors[key] ? <span className="form-error">{errors[key]}</span> : text;
  const a = data.about;

  async function save(e) {
    e.preventDefault();
    try {
      const out = await api.putGeneral({ name: values.name, dashboardUrl: values.dashboardUrl, defaults: { memoryMb: Number(values.memoryMb), cpus: Number(values.cpus), healthSeconds: Number(values.healthSeconds), stopGraceSeconds: Number(values.stopGraceSeconds), restartPolicy: values.restartPolicy, buildMode: values.buildMode } });
      const f = toForm({ ...data, ...out, about: { ...a, dashboardUrl: out.dashboardUrl } });
      setSaved(f); setValues(f);
      setFeedback({ tone: 'success', title: 'Saved' });
      window.dispatchEvent(new CustomEvent('ignition:instance', { detail: out.instance }));
    } catch (err) {
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: err.fields ? 'Check the highlighted fields.' : err.message });
    }
  }

  return <form className="settings-page" onSubmit={save}>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    <SettingsGroup title="Instance">
      <SettingsField label="Instance name" hint={hint('name', 'Shown in the sidebar and browser tab.')} htmlFor="name">
        <TextInput {...bind('name')} maxLength={40} />
      </SettingsField>
      <SettingsField label="Dashboard URL" hint={hint('dashboardUrl', `The address people open Ignition at. Used in invite and reset links and to accept requests from that address. Empty uses ${a.envDashboardUrl} from .env.`)} htmlFor="dashboardUrl">
        <TextInput {...bind('dashboardUrl')} className="mono" type="url" placeholder={a.envDashboardUrl} spellCheck={false} />
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Deployment defaults" description="Used for resources created from now on. Each resource keeps its own settings and can change them.">
      <SettingsField label="Memory limit" hint={hint('memoryMb', 'When a new resource doesn\'t ask for a specific amount.')} htmlFor="memoryMb">
        <Select id="memoryMb" label="Memory limit" options={MEMORY.some(m => m.value === values.memoryMb) ? MEMORY : [{ value: values.memoryMb, label: `${values.memoryMb} MB` }, ...MEMORY]} value={values.memoryMb} onChange={set('memoryMb')} />
      </SettingsField>
      <SettingsField label="CPU limit" hint={hint('cpus')} htmlFor="cpus">
        <Select id="cpus" label="CPU limit" options={CPUS.some(c => c.value === values.cpus) ? CPUS : [{ value: values.cpus, label: `${values.cpus} vCPU` }, ...CPUS]} value={values.cpus} onChange={set('cpus')} />
      </SettingsField>
      <SettingsField label="Health wait" hint={hint('healthSeconds', 'How long a new release must stay up before the old one is retired.')} htmlFor="healthSeconds">
        <TextInput {...bind('healthSeconds')} inputMode="numeric" suffix="seconds" />
      </SettingsField>
      <SettingsField label="Shutdown grace period" hint={hint('stopGraceSeconds', 'Time a stopping release gets to finish requests.')} htmlFor="stopGraceSeconds">
        <TextInput {...bind('stopGraceSeconds')} inputMode="numeric" suffix="seconds" />
      </SettingsField>
      <SettingsField label="On crash" hint={hint('restartPolicy')} htmlFor="restartPolicy">
        <Select id="restartPolicy" label="On crash" options={RESTART} value={values.restartPolicy} onChange={set('restartPolicy')} />
      </SettingsField>
      <SettingsField label="Builder" hint={hint('buildMode', 'For Git repositories.')} htmlFor="buildMode">
        <Select id="buildMode" label="Builder" options={BUILDERS} value={values.buildMode} onChange={set('buildMode')} />
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="About this instance">
      <SettingsField label="Version"><span className="readonly-value">{a.version}{a.commit && a.commit !== 'unknown' && <> · <code className="mono">{a.commit.slice(0, 7)}</code></>}{a.dirty && ' · local changes'}{a.builtAt && ` · built ${fmtDate(a.builtAt)}`}</span></SettingsField>
      <SettingsField label="Installed"><span className="readonly-value">{fmtDate(a.installedAt)}</span></SettingsField>
      <SettingsField label="Docker"><span className="readonly-value">{a.docker ? `${a.docker} · ${a.os}` : 'Not reachable'}</span></SettingsField>
      <SettingsField label="This machine"><span className="readonly-value">{a.cpus ? `${a.cpus} CPUs · ${gb(a.memoryBytes)} for Docker` : '—'}</span></SettingsField>
      <SettingsField label="Database"><span className="readonly-value">PostgreSQL {a.postgres || '—'}</span></SettingsField>
      <SettingsField label="Domain proxy"><span className="readonly-value">HTTP {a.proxyPorts[0]} · HTTPS {a.proxyPorts[1]}</span></SettingsField>
    </SettingsGroup>

    <SaveBar dirty={dirty} onDiscard={() => { setValues(saved); setErrors({}); }} />
  </form>;
}
