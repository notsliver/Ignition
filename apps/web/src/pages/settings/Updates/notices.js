// Update notices, each shown only when its condition is true for this instance.
// `server` notices are about the last update run; dismissing them is remembered by the API.

const duration = (from, to) => {
  const s = Math.max(0, Math.round((new Date(to) - new Date(from)) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
};
const v = s => (s && /^\d+\.\d+/.test(s) ? `v${s}` : s);

export function activeNotices({ installed, config, check, lastCheck, run, docker, startError, retry, viewLogs }) {
  const list = [];
  const dockerMajor = parseInt(docker, 10);

  if (startError) list.push({
    id: 'start-failed', tone: 'danger', title: "The update couldn't start", text: startError,
  });
  if (run?.state === 'failed' && !run.dismissed) list.push({
    id: 'last-update-failed', server: true, tone: 'danger',
    title: 'Last update failed',
    text: `Updating to ${v(run.version)} didn't finish. Ignition is still on ${v(installed.version)}.`,
    action: 'View logs', onAction: viewLogs,
  });
  if (installed.fromSource && installed.dirty) list.push({
    id: 'local-changes', tone: 'info',
    title: 'This build has local changes',
    text: 'It was built from a checkout with uncommitted changes. Updating replaces it with the published image; your checkout itself is left as it is.',
  });
  if (dockerMajor && dockerMajor < 24) list.push({
    id: 'docker-incompatible', tone: 'warning',
    title: 'Docker version not supported',
    text: `Ignition needs Docker 24.0 or newer. This machine is running Docker ${docker}.`,
  });
  const failedCheck = lastCheck?.channel === config.channel || !lastCheck?.channel ? lastCheck?.error : '';
  if (failedCheck) list.push({
    id: 'check-failed', tone: 'warning',
    title: "Couldn't check for updates", text: failedCheck,
    action: 'Retry', onAction: retry,
  });
  if (config.channel !== 'stable') list.push({
    id: 'prerelease-channel', tone: 'info',
    title: `You're on the ${config.channel} channel`,
    text: config.channel === 'edge' ? 'Edge builds come straight from the main branch and can break. Switch to Stable for production.' : 'Beta releases may contain bugs. Switch to Stable for production workloads.',
  });
  if (run?.state === 'succeeded' && !run.dismissed) list.push({
    id: 'update-complete', server: true, tone: 'success',
    title: `Updated to ${v(run.version)}`,
    text: run.finishedAt && !run.finishedAt.startsWith('0001') ? `The update finished in ${duration(run.startedAt, run.finishedAt)}. All services are running.` : 'All services are running.',
  });
  return list;
}
