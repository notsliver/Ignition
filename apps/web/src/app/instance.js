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
