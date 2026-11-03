import { useEffect, useState } from 'react';
import { api } from '../api/client';
import { useSession } from './AuthGate';
import { usePreferences } from './preferences';

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

let sourceModeCached = null;
export function useSourceMode(){
 const [mode,setMode]=useState(sourceModeCached ?? 'self-managed');
 useEffect(()=>{let alive=true;api.system().then(s=>{sourceModeCached=s.sourceMode||'self-managed';if(alive)setMode(sourceModeCached);}).catch(()=>{});
 const changed=e=>{sourceModeCached=e.detail.mode;setMode(sourceModeCached);};window.addEventListener('ignition:source-policy',changed);return ()=>{alive=false;window.removeEventListener('ignition:source-policy',changed);};},[]);
 return mode;
}

// Whether teams can use projects (Instance administration › General › Experiences). Off: only
// standalone resources. Loaded once; updated live when General is saved.
let projectsCached = null;
let projectsPending = null;
export function useProjectsEnabled() {
  const [value, setValue] = useState(projectsCached ?? true);
  useEffect(() => {
    if (projectsCached === null) {
      projectsPending ??= api.system().then(s => (projectsCached = s.projects !== false)).catch(() => true);
      projectsPending.then(setValue);
    }
    const onChange = e => { if (e.detail && 'projects' in e.detail) { projectsCached = e.detail.projects !== false; setValue(projectsCached); } };
    window.addEventListener('ignition:instance', onChange);
    return () => window.removeEventListener('ignition:instance', onChange);
  }, []);
  return value;
}

// Whether the signed-in person may create resources themselves (Instance administration › General
// › Experiences › Resources). "assigned" leaves teams with what administrators assign them;
// instance administrators can always create.
let creationCached = null;
let creationPending = null;
export function useCanCreateResources() {
  const { platformUser, user } = useSession();
  const admin = ['owner', 'admin'].includes((platformUser ?? user)?.role);
  const [mode, setMode] = useState(creationCached ?? 'anyone');
  useEffect(() => {
    if (creationCached === null) {
      creationPending ??= api.system().then(s => (creationCached = s.resourceCreation || 'anyone')).catch(() => 'anyone');
      creationPending.then(setMode);
    }
    const onChange = e => { if (e.detail?.resourceCreation) { creationCached = e.detail.resourceCreation; setMode(creationCached); } };
    window.addEventListener('ignition:instance', onChange);
    return () => window.removeEventListener('ignition:instance', onChange);
  }, []);
  return admin || mode !== 'assigned';
}

// The Resources page layout: the person's own choice, or the instance's default (General).
let layoutCached = null;
let layoutPending = null;
export function useResourceLayout() {
  const own = usePreferences().resourceLayout;
  const [instance, setInstance] = useState(layoutCached ?? 'standard');
  useEffect(() => {
    if (layoutCached === null) {
      layoutPending ??= api.system().then(s => (layoutCached = s.resourceLayout || 'standard')).catch(() => 'standard');
      layoutPending.then(setInstance);
    }
    const onChange = e => { if (e.detail?.resourceLayout) { layoutCached = e.detail.resourceLayout; setInstance(layoutCached); } };
    window.addEventListener('ignition:instance', onChange);
    return () => window.removeEventListener('ignition:instance', onChange);
  }, []);
  return own === 'standard' || own === 'panel' ? own : instance;
}
