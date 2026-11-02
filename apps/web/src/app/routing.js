// Maps the app's navigation state to URLs and back, so pages survive reloads and can be linked.
//
//   /dashboard, /projects, /servers …           global pages
//   /settings/team-members                       your team and you
//   /admin/updates                               instance administration (no team in the URL)
//   /projects/{projectId}/deployments?env=…      a project page
//   /projects/{projectId}/resources/{resourceId}/environment-variables?env=…
//   /servers/{serverId}/diagnostics                a server workspace page
import { teamPath } from '../api/client';
import { adminPages, gameSections, groups, projectItems, resourceSections, serverViews, settingsPages, topItems } from './navigation';

export const slug = title => title.toLowerCase().replace(/&/g, 'and').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');

const bySlug = titles => Object.fromEntries(titles.map(t => [slug(t), t]));
const globalPages = bySlug([...topItems.map(i => i.title).filter(t => t !== 'Settings'), ...groups.flatMap(g => g.items)]);
const settings = bySlug(settingsPages);
const admin = bySlug(adminPages);
const projectPages = bySlug(projectItems.map(i => i.title));
const resourcePages = bySlug([...resourceSections.flat(), ...gameSections.flat()].map(i => i.title));
const serverPages = bySlug(serverViews);
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/** State → URL. */
function localUrl({ active, project, resource, resourceView, environment, server, serverView }) {
  if (server) return `/servers/${server.id}${serverView === 'Overview' ? '' : `/${slug(serverView)}`}`;
  const env = project && environment && environment !== project.environments?.[0]?.name ? `?env=${encodeURIComponent(environment)}` : '';
  if (resource) return `/resources/${resource.id}${resourceView === 'Overview' ? '' : `/${slug(resourceView)}`}`;

  if (project) return `/projects/${project.id}${active === 'Resources' ? '' : `/${slug(active)}`}${env}`;
  if (settingsPages.includes(active)) return `/settings/${slug(active)}`;
  return `/${slug(active)}`;
}

// Instance administration isn't about a team, so its URLs have none.
export function toUrl(state) { return adminPages.includes(state.active) && !state.project && !state.resource && !state.server ? `/admin/${slug(state.active)}` : teamPath(localUrl(state)); }

/**
 * URL → state. Returns IDs for anything that must be loaded from the API.
 * Unknown paths fall back to the dashboard rather than erroring.
 */
export function fromUrl(pathname, search) {
  pathname = pathname.replace(/^\/teams\/[^/]+/, '') || '/';
  const parts = pathname.split('/').filter(Boolean).map(decodeURIComponent);
  const env = new URLSearchParams(search).get('env');
  if (parts[0] === 'resources' && UUID.test(parts[1] ?? '')) return { active: 'Resources', resourceId: parts[1], resourceView: resourcePages[parts[2]] ?? 'Overview' };
  if (parts[0] === 'settings') return { active: settings[parts[1]] ?? admin[parts[1]] ?? 'Team' };
  if (parts[0] === 'admin' || parts[0] === 'admins') return { active: admin[parts[1]] ?? 'General' };
  if (parts[0] === 'servers' && UUID.test(parts[1] ?? '')) return { active: 'Servers', serverId: parts[1], serverView: serverPages[parts[2]] ?? 'Overview' };
  if (parts[0] === 'projects' && UUID.test(parts[1] ?? '')) {
    if (parts[2] === 'resources' && UUID.test(parts[3] ?? '')) {
      return { active: 'Resources', projectId: parts[1], resourceId: parts[3], resourceView: resourcePages[parts[4]] ?? 'Overview', env };
    }
    return { active: projectPages[parts[2]] ?? 'Resources', projectId: parts[1], env };
  }
  return { active: globalPages[parts[0]] ?? 'Dashboard' };
}
