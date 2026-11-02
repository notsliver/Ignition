// Compatibility for API E2E scripts: authenticate globally, then use one explicit
// fixture team for workload requests. Multi-team accounts must set IGNITION_TEAM_ID.
const nativeFetch = globalThis.fetch;
let fixtureTeam = process.env.IGNITION_TEAM_ID;
const globalPath = /^\/api\/(auth|account|setup|settings|users|invites|platform|teams|team-invitations|hooks|automation-hooks|shared-files|links)(\/|$)/;

globalThis.fetch = async (input, init) => {
  const url = new URL(typeof input === 'string' || input instanceof URL ? input : input.url);
  if (url.origin !== new URL(process.env.IGNITION_URL).origin || !url.pathname.startsWith('/api/') || globalPath.test(url.pathname) || ['/api/system', '/api/permissions', '/api/health', '/api/ready'].includes(url.pathname)) return nativeFetch(input, init);
  if (!fixtureTeam) {
    const memberships = await nativeFetch(`${url.origin}/api/teams`, { headers: init?.headers ?? input.headers });
    if (!memberships.ok) return nativeFetch(input, init);
    const { teams } = await memberships.json();
    if (teams.length !== 1) throw new Error('Set IGNITION_TEAM_ID to the intended E2E fixture team.');
    fixtureTeam = teams[0].id;
  }
  url.pathname = url.pathname.replace('/api/', `/api/teams/${fixtureTeam}/`);
  return nativeFetch(typeof input === 'string' || input instanceof URL ? url : new Request(url, input), init);
};
