// Maps the app's navigation state to URLs and back, so pages survive reloads and can be linked.
//
//   /dashboard, /projects, /servers …           global pages
//   /settings/users-and-permissions              instance settings
//   /projects/{projectId}/deployments?env=…      a project page
//   /projects/{projectId}/resources/{resourceId}/environment-variables?env=…
import { groups, projectItems, resourceSections, settingsPages, topItems } from './navigation';

export const slug = title => title.toLowerCase().replace(/&/g, 'and').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');

const bySlug = titles => Object.fromEntries(titles.map(t => [slug(t), t]));
const globalPages = bySlug([...topItems.map(i => i.title).filter(t => t !== 'Settings'), ...groups.flatMap(g => g.items)]);
const settings = bySlug(settingsPages);
const projectPages = bySlug(projectItems.map(i => i.title));
const resourcePages = bySlug(resourceSections.flat().map(i => i.title));
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/** State → URL. */
export function toUrl({ active, project, resource, resourceView, environment }) {
  const env = project && environment && environment !== project.environments?.[0]?.name ? `?env=${encodeURIComponent(environment)}` : '';
  if (project && resource) return `/projects/${project.id}/resources/${resource.id}${resourceView === 'Overview' ? '' : `/${slug(resourceView)}`}${env}`;
  if (project) return `/projects/${project.id}${active === 'Resources' ? '' : `/${slug(active)}`}${env}`;
  if (settingsPages.includes(active)) return `/settings/${slug(active)}`;
  return `/${slug(active)}`;
}

/**
 * URL → state. Returns IDs for anything that must be loaded from the API.
 * Unknown paths fall back to the dashboard rather than erroring.
 */
export function fromUrl(pathname, search) {
  const parts = pathname.split('/').filter(Boolean).map(decodeURIComponent);
  const env = new URLSearchParams(search).get('env');
  if (parts[0] === 'settings') return { active: settings[parts[1]] ?? 'General' };
  if (parts[0] === 'projects' && UUID.test(parts[1] ?? '')) {
    if (parts[2] === 'resources' && UUID.test(parts[3] ?? '')) {
      return { active: 'Resources', projectId: parts[1], resourceId: parts[3], resourceView: resourcePages[parts[4]] ?? 'Overview', env };
    }
    return { active: projectPages[parts[2]] ?? 'Resources', projectId: parts[1], env };
  }
  return { active: globalPages[parts[0]] ?? 'Dashboard' };
}
