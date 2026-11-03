import { useEffect, useState } from 'react';
import { Button, Dialog, FormField, Notice, Select } from '../../ui';
import { api } from '../../api/client';
import { useQuery } from '../../api/hooks';
import { useResourceCreation } from '../project/Resources/Resources';

export function ResourceQuickCreate({ action, projectsQuery, projectsOn = true, onClose, onCreateProject, onCreated, onOpen }) {
  const [projectId, setProjectId] = useState('');
  const [environmentId, setEnvironmentId] = useState('');
  const [target, setTarget] = useState(null);
  const projects = projectsQuery.data?.projects ?? [];
  const environments = useQuery(() => api.teamEnvironments(), []);
  const project = projects.find(p => p.id === projectId) ?? {id:null,name:'No project',environments:environments.data?.environments ?? []};
  useEffect(() => { if (!projectId && !environmentId && project.environments.length) setEnvironmentId(project.environments[0].id); }, [environments.data, projectId]);
  const environment = project?.environments.find(e => e.id === environmentId);

  if (target) return <CreateResource action={action} target={target} onClose={onClose} onCreated={onCreated} onOpen={onOpen} />;

  return <Dialog open onClose={onClose} title={action.label} description={projectsOn ? 'Choose an environment. A project is optional.' : 'Choose an environment.'}
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={!environment} onClick={() => setTarget({ project, environment })}>Continue</Button></>}>
    {projectsQuery.error && <Notice tone="danger" title="Couldn't load projects">{projectsQuery.error.message} <button type="button" className="inline-link" onClick={projectsQuery.refetch}>Retry</button></Notice>}
    {!projectsQuery.data && !projectsQuery.error && <p>Loading projects…</p>}
    {projectsQuery.data && <>
      {projectsOn && <FormField label="Project" htmlFor="quick-create-project">
        <Select id="quick-create-project" label="Project" value={projectId} options={[{ value: '', label: 'No project' }, ...projects.map(p => ({ value: p.id, label: p.name }))]}
          onChange={id => { setProjectId(id); setEnvironmentId((projects.find(p => p.id === id)?.environments ?? environments.data?.environments)?.[0]?.id ?? ''); }} />
      </FormField>}
      {project && <FormField label="Environment" htmlFor="quick-create-environment">
        <Select id="quick-create-environment" label="Environment" value={environmentId} options={project.environments.map(e => ({ value: e.id, label: e.name }))} onChange={setEnvironmentId} />
      </FormField>}
    </>}
  </Dialog>;
}

function CreateResource({ action, target: { project, environment }, onClose, onCreated, onOpen }) {
  const query = useQuery(() => api.resources(project.id, { environment: environment.id }), [project.id, environment.id]);
  const creation = useResourceCreation({ project, environment: environment.name, environmentId: environment.id,
    resources: (query.data?.resources ?? []).filter(r => r.environmentId === environment.id), initialAction: query.data ? action : undefined,
    onClose, onCreated, onOpen, onRefresh: query.refetch });

  if (!query.data) return <Dialog open onClose={onClose} title={action.label}>
    {query.error ? <Notice tone="danger" title="Couldn't load resources">{query.error.message} <button type="button" className="inline-link" onClick={query.refetch}>Retry</button></Notice> : <p>Loading resources…</p>}
  </Dialog>;
  return creation.dialogs;
}
