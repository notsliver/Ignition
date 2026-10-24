import { useEffect, useRef, useState } from 'react';
import { CheckIcon, ClipboardDocumentIcon } from '@heroicons/react/16/solid';
import { IconButton, Select, Switch, TextInput } from '../../../ui';
import { SaveBar, SettingsField, SettingsGroup } from '../SettingsField';

// Fictional sample data until the API is wired up.
const initial = {
  name: 'Ignition',
  url: 'https://ignition.example.com',
  timezone: 'Europe/Amsterdam',
  buildPack: 'nixpacks',
  server: 'production-01',
  autoDeploy: true,
  zeroDowntime: true,
  healthTimeout: '60',
};
const instance = { id: 'ign_8f3a2c71d94e4b0a', installed: 'Mar 14, 2026' };

const timezones = ['UTC', 'Europe/London', 'Europe/Amsterdam', 'America/New_York', 'America/Los_Angeles', 'Asia/Tokyo', 'Australia/Sydney'].map(tz => ({ value: tz, label: tz }));
const buildPacks = [
  { value: 'nixpacks', label: 'Nixpacks', description: 'Detects your stack automatically' },
  { value: 'dockerfile', label: 'Dockerfile', description: 'Build from a Dockerfile in the repository' },
  { value: 'compose', label: 'Docker Compose', description: 'Run multiple containers from compose.yml' },
  { value: 'static', label: 'Static site', description: 'Serve pre-built files with no runtime' },
];
const servers = ['production-01', 'production-02', 'build-01'].map(s => ({ value: s, label: s }));

export function General() {
  const [saved, setSaved] = useState(initial);
  const [values, setValues] = useState(initial);
  const [copied, setCopied] = useState(false);
  const timer = useRef();
  useEffect(() => () => clearTimeout(timer.current), []);

  const dirty = JSON.stringify(values) !== JSON.stringify(saved);
  const set = key => value => setValues(v => ({ ...v, [key]: value }));
  const bindSelect = key => ({ id: key, value: values[key], onChange: set(key) });
  const bind = key => ({ id: key, value: values[key], onChange: event => set(key)(event.target.value) });

  function copyId() {
    navigator.clipboard?.writeText(instance.id);
    setCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1500);
  }

  return <form className="settings-page" onSubmit={event => { event.preventDefault(); setSaved(values); }}>
    <SettingsGroup title="Instance">
      <SettingsField label="Instance name" hint="Shown in the sidebar and browser tab." htmlFor="name">
        <TextInput {...bind('name')} />
      </SettingsField>
      <SettingsField label="Public dashboard URL" hint="Used in links, emails and webhooks." htmlFor="url">
        <TextInput type="url" {...bind('url')} />
      </SettingsField>
      <SettingsField label="Default timezone" hint="For schedules, logs and backups." htmlFor="timezone">
        <Select options={timezones} {...bindSelect('timezone')} />
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Deployment defaults" description="Applied to new applications. Each application can override them.">
      <SettingsField label="Build pack" htmlFor="buildPack">
        <Select options={buildPacks} {...bindSelect('buildPack')} />
      </SettingsField>
      <SettingsField label="Default server" htmlFor="server">
        <Select options={servers} {...bindSelect('server')} />
      </SettingsField>
      <SettingsField label="Health check timeout" hint="How long to wait before a deployment is marked failed." htmlFor="healthTimeout">
        <TextInput type="number" min="5" suffix="seconds" {...bind('healthTimeout')} />
      </SettingsField>
      <SettingsField label="Auto-deploy on push" hint="Deploy when new commits reach the default branch.">
        <Switch label="Auto-deploy on push" checked={values.autoDeploy} onChange={set('autoDeploy')} />
      </SettingsField>
      <SettingsField label="Zero-downtime deployments" hint="Keep the old version running until the new one is healthy.">
        <Switch label="Zero-downtime deployments" checked={values.zeroDowntime} onChange={set('zeroDowntime')} />
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="About this instance">
      <SettingsField label="Instance ID" hint="Include this when contacting support.">
        <TextInput readOnly value={instance.id} className="mono" aria-label="Instance ID" />
        <IconButton icon={copied ? CheckIcon : ClipboardDocumentIcon} label={copied ? 'Copied' : 'Copy instance ID'} type="button" onClick={copyId} />
      </SettingsField>
      <SettingsField label="Installed on">
        <span className="readonly-value">{instance.installed}</span>
      </SettingsField>
    </SettingsGroup>

    <SaveBar dirty={dirty} onDiscard={() => setValues(saved)} />
  </form>;
}
