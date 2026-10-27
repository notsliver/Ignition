import { Automations } from '../../automations/Automations';

/** A resource's automations: the shared Automations page, scoped to this resource. */
export function ResourceAutomations({ resource, onNavigate }) {
  return <div className="rp-page"><Automations scope="resource" resource={resource} onNavigate={onNavigate} /></div>;
}
