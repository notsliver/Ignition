import { useEffect, useState } from 'react';
import { api } from '../api/client';

// The instance name from Settings › General, loaded once and updated live when it's saved.
let cached = null;
let pending = null;

export function useInstanceName() {
  const [name, setName] = useState(cached ?? 'Ignition');
  useEffect(() => {
    if (!cached) {
      pending ??= api.system().then(s => (cached = s.instanceName || 'Ignition')).catch(() => 'Ignition');
      pending.then(setName);
    }
    const onChange = e => { cached = e.detail?.name || 'Ignition'; setName(cached); };
    window.addEventListener('ignition:instance', onChange);
    return () => window.removeEventListener('ignition:instance', onChange);
  }, []);
  return name;
}

// Whether Infrastructure (Servers, Sources, Domains, Networking, Logs & monitoring) lives in
// Instance administration only. Loaded once; updated live when General is saved.
let infraCached = null;
let infraPending = null;
export function useInfrastructureAdminOnly() {
  const [value, setValue] = useState(infraCached ?? false);
  useEffect(() => {
    if (infraCached === null) {
      infraPending ??= api.system().then(s => (infraCached = !!s.infrastructureAdminOnly)).catch(() => false);
      infraPending.then(setValue);
    }
    const onChange = e => { infraCached = !!e.detail?.infrastructureAdminOnly; setValue(infraCached); };
    window.addEventListener('ignition:instance', onChange);
    return () => window.removeEventListener('ignition:instance', onChange);
  }, []);
  return value;
}
