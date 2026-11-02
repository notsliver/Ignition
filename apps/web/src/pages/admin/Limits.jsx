import { useEffect, useMemo, useState } from 'react';
import { PlusIcon, XMarkIcon } from '@heroicons/react/16/solid';
import { Button, Notice, Select, TextInput } from '../../ui';
import { api } from '../../api/client';
import { useQuery } from '../../api/hooks';
import { SaveBar, SettingsField, SettingsGroup } from '../settings/SettingsField';
import '../settings/settings.css';
import './Limits.css';

/*
 * Instance administration › Limits: who gets a team, how big teams get, and quotas at every level.
 * Quotas apply to the whole instance, every team and every person (each can have their own instead),
 * and each server location; per-resource caps limit any single resource, and per-type caps add to
 * them. Empty means unlimited. The API checks every quota that applies before creating, resizing,
 * publishing or storing anything; whatever is already over keeps what it has but can't add more.
 */

const TEAM_FIELDS = [
  { key: 'teamsPerUser', label: 'Teams per person', unit: 'teams', hint: 'How many teams one person can own. Their own team (above) doesn\'t count.', highest: 'teams', of: 'the most anyone owns' },
  { key: 'membersPerTeam', label: 'Members per team', unit: 'members', hint: 'Including invitations not yet accepted.', highest: 'members', of: 'the largest team' },
  { key: 'projectsPerTeam', label: 'Projects per team', unit: 'projects', hint: 'Imports and duplicates count too.', highest: 'projects', of: 'the most in a team' },
];

// Quota fields. scale converts the stored unit (MB) to the one shown (GB).
const QUOTA = [
  { key: 'gameServers', label: 'Game servers' },
  { key: 'applications', label: 'Applications' },
  { key: 'databases', label: 'Databases' },
  { key: 'workers', label: 'Workers and cron jobs' },
  { key: 'functions', label: 'Functions' },
  { key: 'services', label: 'Services in total', hint: 'All of the above together' },
  { key: 'cpus', label: 'CPU', unit: 'cores', decimal: true },
  { key: 'memoryMb', label: 'Memory', unit: 'GB', scale: 1024, decimal: true },
  { key: 'storageGb', label: 'Storage', unit: 'GB', hint: 'Volumes, measured on disk' },
  { key: 'backupGb', label: 'Backup storage', unit: 'GB' },
  { key: 'ports', label: 'Published ports', hint: 'Allocations included' },
];
const CAP = QUOTA.filter(f => ['cpus', 'memoryMb', 'storageGb', 'ports'].includes(f.key));
const TYPES = [
  { value: 'application', label: 'Applications' }, { value: 'database', label: 'Databases' }, { value: 'worker', label: 'Workers' },
  { value: 'cron', label: 'Cron jobs' }, { value: 'function', label: 'Functions' }, { value: 'game', label: 'Game servers' },
];
const KINDS = { teams: 'Team', users: 'Person', locations: 'Location' };

const show = (fields, q = {}) => Object.fromEntries(fields.map(f => [f.key, q[f.key] ? String(+(q[f.key] / (f.scale ?? 1)).toFixed(2)) : '']));
const store = (fields, v) => Object.fromEntries(fields.map(f => {
  const n = Number(v[f.key] || 0) * (f.scale ?? 1);
  return [f.key, f.decimal && !f.scale ? n : Math.round(n)];
}));

function used(u, key) {
  if (!u) return null;
  const gb = b => +(b / 2 ** 30).toFixed(1);
  return { storageGb: gb(u.storageBytes), backupGb: gb(u.backupBytes), memoryMb: +(u.memoryMb / 1024).toFixed(1), cpus: +u.cpus.toFixed(2) }[key] ?? u[key];
}

function toForm(limits, data) {
  const q = data.quotas;
  const names = Object.fromEntries(['teams', 'users', 'locations'].map(k => [k, Object.fromEntries((data[k] ?? []).map(x => [x.id, x.name]))]));
  const overrides = ['teams', 'users', 'locations'].flatMap(kind => Object.entries(q[kind] ?? {}).map(([id, v]) => ({ kind, id, name: names[kind][id] ?? id, values: show(QUOTA, v) })));
  return {
    personalTeams: limits.personalTeams ? 'on' : 'off',
    ...Object.fromEntries(TEAM_FIELDS.map(f => [f.key, limits[f.key] ? String(limits[f.key]) : ''])),
    instance: show(QUOTA, q.instance), team: show(QUOTA, q.team), user: show(QUOTA, q.user), overrides,
    resource: show(CAP, q.resource), types: Object.fromEntries(TYPES.map(t => [t.value, show(CAP, q.types?.[t.value])])),
  };
}

function fromForm(v) {
  const group = kind => Object.fromEntries(v.overrides.filter(o => o.kind === kind).map(o => [o.id, store(QUOTA, o.values)]));
  return {
    limits: { ...Object.fromEntries(TEAM_FIELDS.map(f => [f.key, Number(v[f.key] || 0)])), personalTeams: v.personalTeams === 'on' },
    quotas: {
      instance: store(QUOTA, v.instance), team: store(QUOTA, v.team), user: store(QUOTA, v.user),
      teams: group('teams'), users: group('users'), locations: group('locations'),
      resource: store(CAP, v.resource), types: Object.fromEntries(TYPES.map(t => [t.value, store(CAP, v.types[t.value])])),
    },
  };
}

function Cell({ id, label, field, value, onChange, usage }) {
  const n = used(usage, field.key);
  const over = n != null && value !== '' && Number(value) > 0 && n > Number(value);
  return <div className="lq-cell">
    <TextInput id={id} aria-label={label} inputMode={field.decimal ? 'decimal' : 'numeric'} placeholder="No limit" value={value}
      onChange={e => onChange(e.target.value.replace(field.decimal ? /[^\d.]/g : /[^\d]/g, ''))} />
    {n != null && <small className={over ? 'over' : undefined}>{n} used{over ? ', over' : ''}</small>}
  </div>;
}

export function Limits() {
  const limitsQ = useQuery(() => api.instanceLimits(), []);
  const quotasQ = useQuery(() => api.quotas(), []);
  const [saved, setSaved] = useState(null);
  const [values, setValues] = useState(null);
  const [feedback, setFeedback] = useState(null);
  const [errors, setErrors] = useState({});
  const [adding, setAdding] = useState(null); // { kind, id }
  const dirty = saved && JSON.stringify(values) !== JSON.stringify(saved);
  useEffect(() => {
    if (limitsQ.data && quotasQ.data && !dirty) { const f = toForm(limitsQ.data.limits, quotasQ.data); setSaved(f); setValues(f); }
  }, [limitsQ.data, quotasQ.data]); // eslint-disable-line react-hooks/exhaustive-deps

  const data = quotasQ.data;
  const usageFor = useMemo(() => {
    const m = { teams: {}, users: {}, locations: {} };
    for (const k of Object.keys(m)) for (const x of data?.[k] ?? []) m[k][x.id] = x.usage;
    return m;
  }, [data]);

  const error = limitsQ.error || quotasQ.error;
  if (error && !(limitsQ.data && quotasQ.data)) return <div className="settings-page"><Notice tone="danger" title="Couldn't load limits">{error.message} <button type="button" className="inline-link" onClick={() => { limitsQ.refetch(); quotasQ.refetch(); }}>Retry</button></Notice></div>;
  if (!values) return <div className="settings-page"><p className="readonly-value">Loading…</p></div>;

  const update = fn => { setValues(v => fn(structuredClone(v))); setFeedback(null); setErrors({}); };
  const setAt = (path, key) => value => update(v => { path.reduce((o, p) => o[p], v)[key] = value; return v; });

  async function save(e) {
    e.preventDefault();
    const out = fromForm(values);
    try {
      const [l, q] = await Promise.all([api.saveInstanceLimits(out.limits), api.saveQuotas(out.quotas)]);
      const f = toForm(l.limits, q);
      setSaved(f); setValues(f);
      setFeedback({ tone: 'success', title: 'Saved', text: "New limits apply to what's created, resized or stored from now on." });
      limitsQ.refetch(); quotasQ.refetch();
    } catch (err) {
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: "Couldn't save", text: err.fields ? Object.values(err.fields)[0] : err.message });
    }
  }

  const teamField = f => {
    const n = Number(values[f.key] || 0);
    const top = limitsQ.data.highest[f.highest];
    return <SettingsField key={f.key} label={f.label} htmlFor={f.key}
      hint={errors[f.key] ? <span className="form-error">{errors[f.key]}</span>
        : <>{f.hint} Now {top} at most ({f.of}).{n > 0 && top > n && <span className="limits-over"> That's over this limit: it keeps what it has but can't add more.</span>}</>}>
      <span className="limits-input">
        <TextInput id={f.key} inputMode="numeric" placeholder="Unlimited" value={values[f.key]} onChange={e => setAt([], f.key)(e.target.value.replace(/[^\d]/g, ''))} />
        <span>{n ? f.unit : 'no limit'}</span>
      </span>
    </SettingsField>;
  };

  const label = f => <th scope="row"><span className="lq-label">{f.label}{f.unit && <em>{f.unit}</em>}</span>{f.hint && <small>{f.hint}</small>}</th>;

  const taken = new Set(values.overrides.map(o => `${o.kind}:${o.id.toLowerCase()}`));
  const choices = adding && adding.kind !== 'locations'
    ? (data[adding.kind] ?? []).filter(x => !taken.has(`${adding.kind}:${x.id.toLowerCase()}`)).map(x => ({ value: x.id, label: x.name }))
    : [];
  const addOverride = () => {
    const id = adding.id.trim();
    if (!id || taken.has(`${adding.kind}:${id.toLowerCase()}`)) return;
    const name = adding.kind === 'locations' ? id : choices.find(c => c.value === id)?.label ?? id;
    const base = adding.kind === 'teams' ? values.team : adding.kind === 'users' ? values.user : {};
    update(v => { v.overrides.push({ kind: adding.kind, id, name, values: { ...show(QUOTA), ...base } }); return v; });
    setAdding(null);
  };

  return <form className="settings-page limits-page" onSubmit={save}>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}

    <SettingsGroup title="Default team" description="Where someone lands the first time they sign in.">
      <SettingsField label="New accounts" htmlFor="personalTeams" hint={values.personalTeams === 'on'
        ? "Everyone who joins gets a team of their own, named after their email (they can rename it), on top of any team that invited them. The first owner's team is named after their email too."
        : 'New accounts start with no team of their own: they only see teams that invite them.'}>
        <Select id="personalTeams" label="New accounts" value={values.personalTeams} onChange={setAt([], 'personalTeams')}
          options={[{ value: 'on', label: 'Get their own team (named after their email)' }, { value: 'off', label: 'Start without a team' }]} />
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Teams" description="Leave a limit empty for no limit.">{TEAM_FIELDS.map(teamField)}</SettingsGroup>

    <section className="settings-group">
      <h2>Quotas</h2>
      <p>The most each level can hold in total. Everything that applies is checked: a new game server must fit the instance, its team, the person creating it and its server's location. Empty means no limit.</p>
      <div className="settings-card lq-card">
        <div className="lq-scroll">
          <table className="lq-table">
            <thead><tr><th /><th scope="col">Whole instance</th><th scope="col">Each team</th><th scope="col">Each person</th></tr></thead>
            <tbody>{QUOTA.map(f => <tr key={f.key}>
              {label(f)}
              <td><Cell id={`instance-${f.key}`} label={`${f.label}, whole instance`} field={f} value={values.instance[f.key]} onChange={setAt(['instance'], f.key)} usage={data.usage} /></td>
              <td><Cell id={`team-${f.key}`} label={`${f.label}, each team`} field={f} value={values.team[f.key]} onChange={setAt(['team'], f.key)} /></td>
              <td><Cell id={`user-${f.key}`} label={`${f.label}, each person`} field={f} value={values.user[f.key]} onChange={setAt(['user'], f.key)} /></td>
            </tr>)}</tbody>
          </table>
        </div>
        <p className="lq-foot">A person's quota counts what they created, across all their teams.</p>
      </div>
    </section>

    <section className="settings-group">
      <h2>Custom quotas</h2>
      <p>Give a team or person their own quota instead of the default above, or cap a server location (each server's Location setting).</p>
      <div className="lq-overrides">
        {values.overrides.map((o, i) => {
          const u = usageFor[o.kind][o.id];
          return <div className="settings-card lq-override" key={`${o.kind}:${o.id}`}>
            <header>
              <span className="rp-chip">{KINDS[o.kind]}</span>
              <strong>{o.name}</strong>
              {!u && o.kind === 'locations' && <span className="lq-muted">No server has this location yet</span>}
              <span className="lq-spacer" />
              <button type="button" className="icon-action" aria-label={`Remove ${o.name}'s quota`} title="Remove: the default applies again"
                onClick={() => update(v => { v.overrides.splice(i, 1); return v; })}><XMarkIcon /></button>
            </header>
            <div className="lq-grid">
              {QUOTA.map(f => <label key={f.key} className="lq-field">
                <span>{f.label}{f.unit && <em>{f.unit}</em>}</span>
                <Cell field={f} label={`${f.label}, ${o.name}`} value={o.values[f.key]} onChange={setAt(['overrides', i, 'values'], f.key)} usage={u} />
              </label>)}
            </div>
          </div>;
        })}
        {adding
          ? <div className="settings-card lq-add">
            <Select id="lq-kind" label="Applies to" value={adding.kind} onChange={kind => setAdding({ kind, id: '' })}
              options={Object.entries(KINDS).map(([value, label]) => ({ value, label: { teams: 'A team', users: 'A person', locations: 'A location' }[value] ?? label }))} />
            {adding.kind === 'locations'
              ? <><TextInput id="lq-target" aria-label="Location" list="lq-locations" placeholder="eu-west" value={adding.id} maxLength={64} onChange={e => setAdding(a => ({ ...a, id: e.target.value }))} />
                <datalist id="lq-locations">{(data.locations ?? []).map(l => <option key={l.id} value={l.id} />)}</datalist></>
              : <Select id="lq-target" label={adding.kind === 'teams' ? 'Team' : 'Person'} value={adding.id} searchable={8}
                onChange={id => setAdding(a => ({ ...a, id }))} options={choices.length ? choices : [{ value: '', label: 'All have one already' }]} />}
            <Button type="button" variant="primary" disabled={!adding.id.trim()} onClick={addOverride}>Add</Button>
            <Button type="button" onClick={() => setAdding(null)}>Cancel</Button>
          </div>
          : <Button type="button" icon={PlusIcon} className="lq-add-button" onClick={() => setAdding({ kind: 'teams', id: '' })}>Add custom quota</Button>}
      </div>
    </section>

    <section className="settings-group">
      <h2>Per resource</h2>
      <p>The most a single resource may have. "Any resource" applies to all of them; a type's column adds a stricter cap for that type.</p>
      <div className="settings-card lq-card">
        <div className="lq-scroll">
          <table className="lq-table lq-wide">
            <thead><tr><th /><th scope="col">Any resource</th>{TYPES.map(t => <th scope="col" key={t.value}>{t.label}</th>)}</tr></thead>
            <tbody>{CAP.map(f => <tr key={f.key}>
              {label({ ...f, hint: f.key === 'storageGb' ? 'Uploads stop at this' : f.key === 'ports' ? undefined : f.hint })}
              <td><Cell field={f} label={`${f.label}, any resource`} value={values.resource[f.key]} onChange={setAt(['resource'], f.key)} /></td>
              {TYPES.map(t => <td key={t.value}><Cell field={f} label={`${f.label}, ${t.label}`} value={values.types[t.value][f.key]} onChange={setAt(['types', t.value], f.key)} /></td>)}
            </tr>)}</tbody>
          </table>
        </div>
      </div>
    </section>

    <SaveBar dirty={dirty} onDiscard={() => { setValues(saved); setErrors({}); }} />
  </form>;
}
