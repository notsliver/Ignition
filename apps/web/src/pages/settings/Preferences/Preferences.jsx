import { useMemo, useState } from 'react';
import { ArrowUturnLeftIcon, CheckIcon } from '@heroicons/react/16/solid';
import { Button, Notice, SegmentedControl, Select, Status, Switch, TextInput, cx } from '../../../ui';
import { timeAgo } from '../../../api/hooks';
import { DEFAULTS, PRESETS, resolvedTheme, setPreferences, usePreferences } from '../../../app/preferences';
import { SHORTCUTS } from '../../../app/shortcuts';
import { SettingsField, SettingsGroup } from '../SettingsField';
import '../settings.css';
import './Preferences.css';

/*
 * Your preferences: how Ignition looks and behaves for you. They apply as you change them and are
 * saved to your account, so they follow you to every browser.
 */

const themes = [{ value: 'system', label: 'System' }, { value: 'light', label: 'Light' }, { value: 'dark', label: 'Dark' }];
const sizes = [{ value: '90', label: '90%' }, { value: '100', label: '100%' }, { value: '110', label: '110%' }, { value: '125', label: '125%' }];
const motions = [
  { value: 'system', label: 'Follow the system', description: 'Your operating system\'s "reduce motion" setting' },
  { value: 'reduce', label: 'Reduce', description: 'No animations or transitions' },
  { value: 'full', label: 'Full', description: 'All animations' },
];
const startPages = [
  { value: 'dashboard', label: 'Dashboard' }, { value: 'projects', label: 'Projects' }, { value: 'servers', label: 'Servers' },
  { value: 'monitor', label: 'Monitor' }, { value: 'domains', label: 'Domains' }, { value: 'last', label: 'Where I left off' },
];
const refreshes = [
  { value: 'live', label: 'Live', description: 'Twice as often. More load on the server.' },
  { value: 'normal', label: 'Normal', description: 'Each page\'s own rhythm (5 to 60 seconds)' },
  { value: 'relaxed', label: 'Relaxed', description: 'Three times less often. Easier on slow links.' },
];

export function Preferences() {
  const p = usePreferences();
  const set = key => value => setPreferences({ [key]: value });
  const theme = resolvedTheme(p);
  const zones = useMemo(() => {
    let list = [];
    try { list = Intl.supportedValuesOf('timeZone'); } catch { list = ['UTC']; }
    const browser = Intl.DateTimeFormat().resolvedOptions().timeZone;
    return [{ value: 'browser', label: `This browser (${browser})` }, { value: 'UTC', label: 'UTC' }, ...list.filter(z => z !== 'UTC').map(z => ({ value: z, label: z.replaceAll('_', ' ') }))];
  }, []);
  const sample = useMemo(() => new Date(Date.now() - 3 * 3600e3 - 17 * 60e3), []);
  const changed = Object.keys(DEFAULTS).filter(k => p[k] !== DEFAULTS[k]).length;

  return <div className="settings-page prefs">
    <p className="prefs-intro">Changes apply right away and are saved to your account, so they follow you to every browser.{changed > 0 && <> {changed} setting{changed === 1 ? '' : 's'} differ from the defaults.</>}</p>

    <SettingsGroup title="Appearance">
      <SettingsField label="Theme" hint={p.theme === 'system' ? `Following your system, which is ${theme} right now.` : 'The sidebar switch changes this too.'}>
        <SegmentedControl label="Theme" className="auto-width" value={p.theme} onChange={set('theme')} options={themes} />
      </SettingsField>
      <SettingsField label="Theme preset" hint="Restyles the whole page: background, surfaces, borders, text and accent, tuned for light and dark.">
        <div className="prefs-presets" role="radiogroup" aria-label="Theme preset">
          {Object.entries(PRESETS).map(([key, preset]) => <button key={key} type="button" role="radio" aria-checked={p.preset === key && !p.accent}
            className={cx('prefs-preset', p.preset === key && !p.accent && 'active')} onClick={() => setPreferences({ preset: key, accent: '' })} title={preset.label}>
            <PresetCard preset={preset[theme]} selected={p.preset === key && !p.accent} />
            <span>{preset.label}</span>
          </button>)}
        </div>
      </SettingsField>
      <SettingsField label="Custom accent" hint={p.accent ? 'Overrides the preset in both themes.' : 'Pick any colour instead of a preset.'} htmlFor="accent">
        <label className="prefs-color"><input id="accent" type="color" value={p.accent || PRESETS[p.preset]?.[theme]?.accent || '#3c55cf'} onChange={e => set('accent')(e.target.value)} /><span className="mono">{p.accent || 'Preset'}</span></label>
        {p.accent && <Button icon={ArrowUturnLeftIcon} onClick={() => set('accent')('')}>Use preset</Button>}
      </SettingsField>
      <SettingsField label="High contrast" hint="Stronger text, borders and focus rings.">
        <Switch label="High contrast" checked={p.contrast === 'high'} onChange={on => set('contrast')(on ? 'high' : 'normal')} />
      </SettingsField>
      <SettingsField label="Interface size" hint="Scales everything: text, controls and spacing.">
        <SegmentedControl label="Interface size" className="auto-width" value={p.size} onChange={set('size')} options={sizes} />
      </SettingsField>
      <SettingsField label="Table rows" hint="Compact fits more rows; comfortable is easier to scan.">
        <SegmentedControl label="Table rows" className="auto-width" value={p.tableDensity} onChange={set('tableDensity')} options={[{ value: 'compact', label: 'Compact' }, { value: 'comfortable', label: 'Comfortable' }]} />
      </SettingsField>
      <SettingsField label="Motion" htmlFor="motion">
        <Select id="motion" label="Motion" value={p.motion} onChange={set('motion')} options={motions} />
      </SettingsField>
      <div className="prefs-preview" aria-label="Preview">
        <span className="prefs-preview-label">Preview</span>
        <div className="prefs-preview-row">
          <Button variant="primary">Deploy</Button><Button>Cancel</Button>
          <Status tone="success">Running</Status><Status tone="warning">Degraded</Status>
          <a href="#preview" onClick={e => e.preventDefault()}>A link</a>
          <span className="rp-chip accent">Primary</span>
        </div>
        <TextInput placeholder="An input" aria-label="Preview input" />
      </div>
    </SettingsGroup>

    <SettingsGroup title="Dates and times" description="Used for every timestamp in Ignition.">
      <SettingsField label="Times" hint={`Looks like: ${timeAgo(sample.toISOString())}`}>
        <SegmentedControl label="Times" className="auto-width" value={p.timeStyle} onChange={set('timeStyle')} options={[{ value: 'relative', label: 'Relative' }, { value: 'absolute', label: 'Exact' }]} />
      </SettingsField>
      <SettingsField label="Clock" hint={`Looks like: ${sample.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}`}>
        <SegmentedControl label="Clock" className="auto-width" value={p.clock} onChange={set('clock')} options={[{ value: 'auto', label: 'Auto' }, { value: '12h', label: '12-hour' }, { value: '24h', label: '24-hour' }]} />
      </SettingsField>
      <SettingsField label="Date format" hint={`Looks like: ${sample.toLocaleDateString()}`} htmlFor="dateFormat">
        <Select id="dateFormat" label="Date format" value={p.dateFormat} onChange={set('dateFormat')} options={[
          { value: 'auto', label: 'This browser\'s' }, { value: 'iso', label: 'Year-month-day', description: '2026-10-31' },
          { value: 'dmy', label: 'Day/month/year', description: '31/10/2026' }, { value: 'mdy', label: 'Month/day/year', description: '10/31/2026' },
        ]} />
      </SettingsField>
      <SettingsField label="Time zone" hint={`Now: ${new Date().toLocaleString(undefined, { hour: 'numeric', minute: '2-digit', timeZoneName: 'short' })}`} htmlFor="timezone">
        <Select id="timezone" label="Time zone" value={p.timezone} onChange={set('timezone')} options={zones} searchable />
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Behaviour">
      <SettingsField label="Start page" hint="Where Ignition opens when you go to its address." htmlFor="startPage">
        <Select id="startPage" label="Start page" value={p.startPage} onChange={set('startPage')} options={startPages} />
      </SettingsField>
      <SettingsField label="Sidebar" hint="How the sidebar starts. Press [ to toggle it any time.">
        <SegmentedControl label="Sidebar" className="auto-width" value={p.sidebar} onChange={set('sidebar')} options={[{ value: 'expanded', label: 'Shown' }, { value: 'collapsed', label: 'Hidden' }]} />
      </SettingsField>
      <SettingsField label="Live updates" hint="How often pages refresh in the background. Hidden tabs never poll." htmlFor="refresh">
        <Select id="refresh" label="Live updates" value={p.refresh} onChange={set('refresh')} options={refreshes} />
      </SettingsField>
      <SettingsField label="Keyboard shortcuts" hint="Never while you're typing in a field.">
        <Switch label="Keyboard shortcuts" checked={p.shortcuts === 'on'} onChange={on => set('shortcuts')(on ? 'on' : 'off')} />
      </SettingsField>
      {p.shortcuts === 'on' && <ul className="prefs-keys">
        {SHORTCUTS.map(s => <li key={s.label}><span>{s.keys.map((k, i) => <span key={i}>{i > 0 && <small>then</small>}<kbd>{k}</kbd></span>)}</span>{s.label}</li>)}
      </ul>}
    </SettingsGroup>

    {changed > 0 && <Notice tone="info" title="Back to the defaults?" action="Reset all" onAction={() => setPreferences({ ...DEFAULTS })}>Resets every preference on this page, for all your browsers.</Notice>}
  </div>;
}

/** A tiny page in the preset's colours: background, a sidebar line, a card, text and the accent. */
function PresetCard({ preset: c, selected }) {
  return <span className="prefs-card" style={{ background: c.bg, borderColor: c.border }} aria-hidden="true">
    <span className="prefs-card-side" style={{ background: c.line }} />
    <span className="prefs-card-body" style={{ background: c.surface, borderColor: c.border }}>
      <span style={{ background: c.text, width: '70%' }} />
      <span style={{ background: c.muted, width: '45%' }} />
      <span className="prefs-card-btn" style={{ background: c.accent }} />
    </span>
    {selected && <span className="prefs-card-check" style={{ background: c.accent }}><CheckIcon style={{ color: readable(c.accent) }} /></span>}
  </span>;
}

const readable = hex => { const n = parseInt(hex.slice(1), 16); return ((n >> 16) * 299 + ((n >> 8) & 255) * 587 + (n & 255) * 114) / 1000 > 150 ? '#10131f' : '#fff'; };
