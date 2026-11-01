import { useEffect, useMemo, useState } from 'react';
import { LockClosedIcon } from '@heroicons/react/16/solid';
import { Notice, SearchField, SegmentedControl, Select, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { SaveBar } from '../../settings/SettingsField';
import { RestartNotice } from './shared';
import './game.css';

/*
 * The game's settings file (server.properties) as a form: the common settings with plain names, then
 * every other key as it is in the file. Raw edits the file directly. Keys Ignition manages (the port,
 * RCON) are shown but locked. Saved values apply when the server restarts.
 */

export function GameProperties({ resource }) {
  const { user } = useSession();
  const canEdit = can(user, 'games.settings');
  const q = useQuery(() => api.gameProperties(resource.id), [resource.id]);
  const [values, setValues] = useState(null);
  const [raw, setRaw] = useState('');
  const [mode, setMode] = useState('form');
  const [filter, setFilter] = useState('');
  const [errors, setErrors] = useState({});
  const [restart, setRestart] = useState('');
  const [fail, setFail] = useState('');
  useEffect(() => { if (q.data) { setValues(q.data.values); setRaw(q.data.raw); } }, [q.data]);

  const d = q.data;
  const known = useMemo(() => new Set(d?.config.groups.flatMap(g => g.props.map(p => p.key)) ?? []), [d]);
  if (q.error && !d) return <div className="rp-page gm-page"><Notice tone="danger" title="Couldn't read the settings">{q.error.message}</Notice></div>;
  if (!d || !values) return <div className="rp-page gm-page"><p className="gm-muted">Loading…</p></div>;
  if (!d.exists) return <div className="rp-page gm-page"><Notice tone="info" title="Not created yet">The server writes {d.config.file} on its first start. Come back once it has started.</Notice></div>;

  const changed = Object.keys(values).filter(k => values[k] !== d.values[k]);
  const dirty = mode === 'raw' ? raw !== d.raw : changed.length > 0;
  const f = filter.trim().toLowerCase();
  const match = (key, label = '') => !f || key.includes(f) || label.toLowerCase().includes(f);
  const others = d.keys.filter(k => !known.has(k) && !d.config.locked[k] && match(k));
  const set = (k, v) => setValues(cur => ({ ...cur, [k]: v }));

  async function save(e) {
    e.preventDefault();
    setErrors({}); setFail('');
    try {
      const out = await api.saveGameProperties(resource.id, mode === 'raw' ? { raw } : { values: Object.fromEntries(changed.map(k => [k, values[k]])) });
      if (out.changed.length) setRestart(`Changed ${out.changed.join(', ')}. ${out.restartNeeded ? 'Restart the server to apply it.' : 'It applies on the next start.'}`);
      q.refetch();
    } catch (err) {
      setErrors(err.fields ?? {});
      setFail(err.fields ? Object.values(err.fields).join(' ') : err.message);
    }
  }

  const control = p => {
    const v = values[p.key];
    const disabled = !canEdit;
    if (p.kind === 'bool') return <Switch label={p.label} checked={v === 'true'} disabled={disabled} onChange={on => set(p.key, String(on))} />;
    if (p.kind === 'select') return <Select label={p.label} value={v} onChange={x => set(p.key, x)} options={p.options.some(o => o.value === v) ? p.options : [...p.options, { value: v, label: v }]} />;
    return <TextInput className={cx(p.kind === 'int' && 'gm-num')} type={p.kind === 'int' ? 'number' : 'text'} min={p.min} max={p.max} value={v} disabled={disabled} aria-label={p.label} onChange={e => set(p.key, e.target.value)} />;
  };

  return <form className="rp-page gm-page gm-props" onSubmit={save}>
    <div className="rp-toolbar">
      <SegmentedControl label="Edit as" className="auto-width" value={mode} onChange={m => { setMode(m); setValues(d.values); setRaw(d.raw); }} options={[{ value: 'form', label: 'Settings' }, { value: 'raw', label: d.config.file }]} />
      {mode === 'form' && <SearchField value={filter} onChange={setFilter} placeholder="Find a setting" />}
    </div>
    {restart && <RestartNotice resource={resource} text={restart} onDone={() => setRestart('')} />}
    {fail && <Notice tone="danger" title="Couldn't save">{fail}</Notice>}

    {mode === 'raw' ? <textarea className="gm-raw mono" value={raw} spellCheck={false} disabled={!canEdit} onChange={e => setRaw(e.target.value)} aria-label={d.config.file} /> : <>
      {d.config.groups.map(g => {
        const props = g.props.filter(p => p.key in values && match(p.key, p.label));
        if (!props.length) return null;
        return <section key={g.label} className="settings-group gm-prop-group">
          <h2>{g.label}</h2>
          <div className="rp-card">
            {props.map(p => <div key={p.key} className={cx('gm-prop', errors[p.key] && 'has-error', values[p.key] !== d.values[p.key] && 'changed')}>
              <span className="gm-prop-label"><strong>{p.label}</strong><small>{errors[p.key] ?? p.hint}</small><code>{p.key}</code></span>
              <span className="gm-prop-control">{control(p)}</span>
            </div>)}
          </div>
        </section>;
      })}
      {(others.length > 0 || Object.keys(d.config.locked).some(k => k in values && match(k))) && <section className="settings-group gm-prop-group">
        <h2>Everything else</h2>
        <div className="rp-card">
          {others.map(k => <div key={k} className={cx('gm-prop', errors[k] && 'has-error', values[k] !== d.values[k] && 'changed')}>
            <span className="gm-prop-label"><code className="strong">{k}</code>{errors[k] && <small>{errors[k]}</small>}</span>
            <span className="gm-prop-control"><TextInput value={values[k]} disabled={!canEdit} aria-label={k} onChange={e => set(k, e.target.value)} /></span>
          </div>)}
          {Object.entries(d.config.locked).filter(([k]) => k in values && match(k)).map(([k, why]) => <div key={k} className="gm-prop locked">
            <span className="gm-prop-label"><code className="strong">{k}</code><small><LockClosedIcon className="gm-inline-icon" />{why}</small></span>
            <span className="gm-prop-control mono">{k.includes('password') ? '••••••' : values[k] || '—'}</span>
          </div>)}
        </div>
      </section>}
    </>}
    {canEdit && <SaveBar dirty={dirty} onDiscard={() => { setValues(d.values); setRaw(d.raw); setErrors({}); }}
      summary={mode === 'raw' ? `${d.config.file} changed` : `${changed.length} setting${changed.length === 1 ? '' : 's'} changed`} />}
  </form>;
}
