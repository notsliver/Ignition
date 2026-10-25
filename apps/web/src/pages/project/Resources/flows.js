// Creation flows for "Add resource". Each supported flow describes its form fields and
// builds the API payload for POST /projects/{id}/resources. Keys are the menu path,
// e.g. "Application/From Git repository/Continue with GitHub".
// Options the backend can't do yet are listed in `unsupported` with an honest reason.
import { CircleStackIcon, CogIcon, CubeIcon, LinkIcon } from '@heroicons/react/16/solid';
import { GitHubIcon, GitLabIcon } from '../../../ui';

const opts = list => list.map(v => (typeof v === 'string' ? { value: v, label: v } : v));
const slug = value => value.toLowerCase().replace(/\.git$/, '').split('/').pop().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '');
const envFrom = values => (values.env ?? []).filter(e => e.key.trim()).map(e => ({ key: e.key.trim(), value: e.value, secret: /SECRET|PASSWORD|TOKEN|KEY/.test(e.key) }));

const name = (fallback = '') => ({ key: 'name', label: 'Name', placeholder: fallback || 'my-resource', default: fallback, required: true, kind: 'name', hint: 'Lowercase letters, numbers and dashes.' });
const envVars = { key: 'env', label: 'Environment variables', type: 'env', default: [] };
const port = { key: 'port', label: 'Port', type: 'number', default: '3000', hint: 'The port your app listens on. Published on a free port on this machine.' };
const dockerfile = { key: 'dockerfile', label: 'Dockerfile path', default: 'Dockerfile', hint: 'Builds use your Dockerfile. Buildpacks and Nixpacks aren\'t supported yet.' };
const toPort = v => (v.port ? Number(v.port) : null);

const gitFlow = (host, provider, icon) => ({
  icon: CubeIcon,
  context: v => ({ label: `Importing from ${provider}`, icon, text: v.repository || 'owner/repository', branch: v.branch, placeholder: !v.repository }),
  title: `Deploy from ${provider}`,
  description: `Builds a public ${provider} repository with its Dockerfile and runs it on this machine.`,
  submit: 'Deploy',
  fields: [
    { key: 'repository', label: 'Repository', prefix: `${host}/`, placeholder: 'owner/repository', required: true, kind: 'repo' },
    { key: 'branch', label: 'Branch', default: 'main', required: true },
    name(), dockerfile, port, envVars,
  ],
  build: v => ({ name: v.name, type: 'application', sourceType: 'git', repoUrl: `https://${host}/${v.repository.trim().replace(/^\/+/, '').replace(/\.git$/, '')}`, branch: v.branch.trim(), dockerfile: v.dockerfile, port: toPort(v), env: envFrom(v) }),
});

/* Databases: official images with their documented environment variables and data directories. */
const engines = {
  PostgreSQL: { image: v => `postgres:${v}`, versions: ['17', '16', '15'], port: 5432, data: '/var/lib/postgresql/data',
    env: v => [['POSTGRES_DB', v.database], ['POSTGRES_USER', v.user], ['POSTGRES_PASSWORD', v.password]] },
  MySQL: { image: v => `mysql:${v}`, versions: ['8.4', '8.0'], port: 3306, data: '/var/lib/mysql',
    env: v => [['MYSQL_DATABASE', v.database], ['MYSQL_USER', v.user], ['MYSQL_PASSWORD', v.password], ['MYSQL_ROOT_PASSWORD', v.password]] },
  MariaDB: { image: v => `mariadb:${v}`, versions: ['11.4', '10.11'], port: 3306, data: '/var/lib/mysql',
    env: v => [['MARIADB_DATABASE', v.database], ['MARIADB_USER', v.user], ['MARIADB_PASSWORD', v.password], ['MARIADB_ROOT_PASSWORD', v.password]] },
  MongoDB: { image: v => `mongo:${v}`, versions: ['8.0', '7.0'], port: 27017, data: '/data/db',
    env: v => [['MONGO_INITDB_DATABASE', v.database], ['MONGO_INITDB_ROOT_USERNAME', v.user], ['MONGO_INITDB_ROOT_PASSWORD', v.password]] },
  Redis: { image: v => `redis:${v}-alpine`, versions: ['7.4', '7.2'], port: 6379, data: '/data', keyValue: true,
    env: v => [['REDIS_PASSWORD', v.password]], command: 'exec redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"' },
  Dragonfly: { image: v => `docker.dragonflydb.io/dragonflydb/dragonfly:v${v}`, versions: ['1.25.0'], port: 6379, data: '/data', keyValue: true,
    env: v => [['DFLY_requirepass', v.password]] },
  ClickHouse: { image: v => `clickhouse/clickhouse-server:${v}`, versions: ['24.8', '24.3'], port: 8123, data: '/var/lib/clickhouse', memory: 1024,
    env: v => [['CLICKHOUSE_DB', v.database], ['CLICKHOUSE_USER', v.user], ['CLICKHOUSE_PASSWORD', v.password], ['CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT', '1']] },
};

const dbFlow = engineName => {
  const e = engines[engineName];
  return {
    icon: CircleStackIcon,
    context: v => ({ label: 'Creating database', icon: CircleStackIcon, text: `${engineName} ${v.version}` }),
    title: `New ${engineName} database`,
    description: 'Runs the official image with a persistent volume. Credentials are stored encrypted.',
    submit: 'Create database',
    fields: [
      name(engineName.toLowerCase()),
      { key: 'version', label: 'Version', type: 'select', default: e.versions[0], options: opts(e.versions) },
      ...(e.keyValue ? [] : [
        { key: 'database', label: 'Database name', default: 'app', required: true, kind: 'identifier', mono: true },
        { key: 'user', label: 'User', default: 'ignition', required: true, kind: 'identifier', mono: true },
      ]),
      { key: 'password', label: 'Password', type: 'secret', required: true, minLength: 12, hint: 'Generated for you. Stored encrypted.' },
      { key: 'publicAccess', label: 'Access', type: 'select', default: 'private', options: [
        { value: 'private', label: 'Private', description: 'Only other resources on this machine can connect' },
        { value: 'public', label: `Publish port ${e.port}`, description: 'Reachable from your network. Use a strong password.' },
      ] },
    ],
    build: v => ({
      name: v.name, type: 'database', sourceType: 'image', image: e.image(v.version), dataPath: e.data, command: e.command ?? '', memoryMb: e.memory ?? 512,
      port: v.publicAccess === 'public' ? e.port : null,
      env: e.env(v).map(([key, value]) => ({ key, value, secret: /PASSWORD|requirepass/i.test(key) })),
    }),
  };
};

const workerFlow = {
  icon: CogIcon, title: 'New worker', description: 'A long-running process without a public port, such as a queue consumer.', submit: 'Deploy',
  fields: [
    name(),
    { key: 'source', label: 'Source', placeholder: 'https://github.com/owner/repo or ghcr.io/owner/image:tag', required: true },
    { key: 'command', label: 'Start command', placeholder: 'npm run worker', hint: 'Leave empty to use the image\'s default command.' },
    envVars,
  ],
  build: v => {
    const src = v.source.trim();
    const git = /^https:\/\//.test(src);
    return { name: v.name, type: 'worker', sourceType: git ? 'git' : 'image', ...(git ? { repoUrl: src.replace(/\.git$/, ''), branch: 'main' } : { image: src }), command: v.command.trim(), env: envFrom(v) };
  },
};

export const flows = {
  'Application/From Git repository/Continue with GitHub': gitFlow('github.com', 'GitHub', GitHubIcon),
  'Application/From Git repository/Continue with GitLab': gitFlow('gitlab.com', 'GitLab', GitLabIcon),
  'Application/From Git repository/Continue with other source': {
    ...gitFlow('', 'Git', LinkIcon),
    title: 'Deploy from a Git URL',
    description: 'Any public https Git repository with a Dockerfile.',
    context: v => ({ label: 'Importing from Git', icon: LinkIcon, text: v.repository.replace(/^https?:\/\//, '') || 'git.example.com/team/app', branch: v.branch, placeholder: !v.repository }),
    fields: [{ key: 'repository', label: 'Repository URL', placeholder: 'https://git.example.com/team/app.git', required: true, kind: 'repo' }, { key: 'branch', label: 'Branch', default: 'main', required: true }, name(), dockerfile, port, envVars],
    build: v => ({ name: v.name, type: 'application', sourceType: 'git', repoUrl: v.repository.trim().replace(/\.git$/, ''), branch: v.branch.trim(), dockerfile: v.dockerfile, port: toPort(v), env: envFrom(v) }),
  },
  'Application/Start with Hello World!': {
    icon: CubeIcon, title: 'Start with Hello World!', description: 'A tiny web app that echoes request details. Deploys in seconds.', submit: 'Deploy',
    context: () => ({ label: 'Starting from', icon: CubeIcon, text: 'traefik/whoami' }),
    fields: [name('hello-world')],
    build: v => ({ name: v.name, type: 'application', sourceType: 'image', image: 'traefik/whoami:v1.10', port: 80, memoryMb: 128 }),
  },
  'Application/Deploy a container image': {
    icon: CubeIcon, title: 'Deploy a container image', description: 'Run any public image from Docker Hub or another registry.', submit: 'Deploy',
    context: v => ({ label: 'Deploying image', icon: CubeIcon, text: v.image || 'nginx:1.27-alpine', placeholder: !v.image }),
    fields: [{ key: 'image', label: 'Image', placeholder: 'nginx:1.27-alpine', required: true, mono: true }, name(), { ...port, default: '80' }, envVars],
    build: v => ({ name: v.name, type: 'application', sourceType: 'image', image: v.image.trim(), port: toPort(v), env: envFrom(v) }),
  },
  ...Object.fromEntries(Object.keys(engines).map(e => [`Database/Create new database/${e}`, dbFlow(e)])),
  Worker: workerFlow,
};

// Menu entries that aren't backed by the API yet, with the reason shown to the user.
export const unsupported = {
  'Continue with Artifacts': 'Artifacts isn\'t a supported Git provider yet. Use GitHub, GitLab or any public https Git URL.',
  'Select a template': 'Templates aren\'t available yet. Deploy a container image or a Git repository instead.',
  'Upload your static files': 'Uploading files isn\'t supported yet. Put the site in a Git repository with a Dockerfile (for example, nginx serving the files).',
  'Import existing data': 'Importing data isn\'t automated yet. Create the database, then restore your dump with its own tools.',
  'Restore from an Ignition backup': 'Backups aren\'t implemented yet, so there\'s nothing to restore from.',
  'Connect external database': 'External databases can\'t be registered yet. Store the connection URL as an environment variable on the apps that use it.',
  'Scheduled job': 'Scheduled jobs aren\'t supported yet.',
};

/** Validate submitted values. Returns { field: message }. */
export function validate(flow, values, takenNames) {
  const errors = {};
  const env = flow.fields.some(f => f.type === 'env') ? values.env : [];
  const keys = env.map(e => e.key.trim()).filter(Boolean);
  if (env.some(e => e.value && !e.key.trim())) errors.env = 'Every value needs a name.';
  else if (env.some(e => e.key.trim() && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(e.key.trim()))) errors.env = 'Names can only use letters, numbers and underscores.';
  else if (new Set(keys).size !== keys.length) errors.env = 'Each variable name must be unique.';
  for (const field of flow.fields) {
    const value = values[field.key];
    if (field.required && !String(value ?? '').trim()) errors[field.key] = `${field.label} is required.`;
    else if (field.kind === 'name' && value && !/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(value)) errors[field.key] = 'Use lowercase letters, numbers and dashes.';
    else if (field.kind === 'identifier' && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(value)) errors[field.key] = `${field.label} can only use letters, numbers and underscores.`;
    else if (field.minLength && String(value).length < field.minLength) errors[field.key] = `${field.label} must be at least ${field.minLength} characters.`;
    else if (field.key === 'port' && value && !(Number(value) >= 1 && Number(value) <= 65535)) errors.port = 'Ports are numbers from 1 to 65535.';
    else if (field.kind === 'name' && takenNames.includes(value)) errors[field.key] = 'A resource with this name already exists.';
  }
  return errors;
}

export const suggestName = value => slug(value);
