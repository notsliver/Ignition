// End-to-end checks for project imports (Git, Compose, duplicate), database imports into every engine,
// external databases and functions. Needs IGNITION_URL/EMAIL/PASSWORD and the docker CLI (to verify data).
import { execFileSync } from 'node:child_process';

const BASE = process.env.IGNITION_URL;
let cookie = '';
const ok = msg => console.log('✓', msg);
const sleep = ms => new Promise(r => setTimeout(r, ms));
async function call(method, path, body, headers = {}) {
  const isForm = body instanceof FormData;
  const res = await fetch(`${BASE}/api${path}`, { method, headers: { Origin: BASE, ...(cookie ? { Cookie: cookie } : {}), ...(body !== undefined && !isForm ? { 'Content-Type': 'application/json' } : {}), ...headers },
    body: body === undefined ? undefined : isForm ? body : JSON.stringify(body) });
  const set = res.headers.get('set-cookie');
  if (set) cookie = set.split(';')[0];
  const text = await res.text();
  try { return { status: res.status, data: JSON.parse(text) }; } catch { return { status: res.status, data: text }; }
}
const api = async (m, p, b) => { const r = await call(m, p, b); if (r.status >= 300) throw new Error(`${m} ${p} → ${r.status} ${JSON.stringify(r.data).slice(0, 400)}`); return r.data; };
async function until(what, fn, ms = 300_000) { const end = Date.now() + ms; while (Date.now() < end) { const v = await fn(); if (v) return v; await sleep(2000); } throw new Error('timed out waiting for ' + what); }
const running = (id, ms = 600_000) => until(`resource ${id} running`, async () => { const { resource } = await api('GET', `/resources/${id}`); if (resource.status === 'failed') throw new Error(`${resource.name} failed to start`); return resource.status === 'running' && resource; }, ms);
const container = id => execFileSync('docker', ['ps', '-q', '--filter', `label=ignition.resource=${id}`]).toString().trim().split('\n')[0];
const sh = (id, cmd) => execFileSync('docker', ['exec', container(id), 'sh', '-c', cmd]).toString().trim();
const finished = id => until('import', async () => { const out = await api('GET', `/data-imports/${id}`); return !['queued', 'running'].includes(out.import.status) && out; }, 900_000);
async function importDone(id, label) {
  const out = await finished(id);
  if (out.import.status !== 'succeeded') throw new Error(`${label} import ${out.import.status}: ${out.import.summary}\n${out.logs.map(l => l.message).slice(-15).join('\n')}`);
  return out;
}
async function upload(resourceId, name, text, replace = false) {
  const form = new FormData();
  form.append('replace', String(replace));
  form.append('file', new Blob([text]), name);
  return (await api('POST', `/resources/${resourceId}/imports`, form)).import;
}

const PW = 'Tr0ub4dor-and-3-horses';
const db = (name, image, env, dataPath, extra = {}) => ({ name, type: 'database', sourceType: 'image', image, dataPath, memoryMb: 512, env: env.map(([key, value]) => ({ key, value, secret: /PASS|requirepass/i.test(key) })), ...extra });
const DBS = {
  pg: n => db(n, 'postgres:17', [['POSTGRES_DB', 'app'], ['POSTGRES_USER', 'ignition'], ['POSTGRES_PASSWORD', PW]], '/var/lib/postgresql/data'),
  mysql: n => db(n, 'mysql:8.4', [['MYSQL_DATABASE', 'app'], ['MYSQL_USER', 'ignition'], ['MYSQL_PASSWORD', PW], ['MYSQL_ROOT_PASSWORD', PW]], '/var/lib/mysql'),
  mariadb: n => db(n, 'mariadb:11.4', [['MARIADB_DATABASE', 'app'], ['MARIADB_USER', 'ignition'], ['MARIADB_PASSWORD', PW], ['MARIADB_ROOT_PASSWORD', PW]], '/var/lib/mysql'),
  mongo: n => db(n, 'mongo:8.0', [['MONGO_INITDB_DATABASE', 'app'], ['MONGO_INITDB_ROOT_USERNAME', 'ignition'], ['MONGO_INITDB_ROOT_PASSWORD', PW]], '/data/db'),
  redis: n => db(n, 'redis:7.4-alpine', [['REDIS_PASSWORD', PW]], '/data', { command: 'exec redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"' }),
  dragonfly: n => db(n, 'docker.dragonflydb.io/dragonflydb/dragonfly:v1.25.0', [['DFLY_requirepass', PW]], '/data'),
  clickhouse: n => db(n, 'clickhouse/clickhouse-server:24.8', [['CLICKHOUSE_DB', 'app'], ['CLICKHOUSE_USER', 'ignition'], ['CLICKHOUSE_PASSWORD', PW], ['CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT', '1']], '/var/lib/clickhouse', { memoryMb: 1024 }),
};

try {
  await api('POST', '/auth/login', { email: process.env.IGNITION_EMAIL, password: process.env.IGNITION_PASSWORD });
  const run = Date.now() % 10000;

  /* -------------------------------- Compose import -------------------------------- */
  const compose = `services:
  web:
    image: nginx:1.27-alpine
    ports: ["8080:80"]
    environment:
      API_KEY: \${API_KEY}
      DATABASE_URL: postgres://app:\${DB_PASSWORD:-x}@cache:6379
    depends_on: [cache]
  cache:
    image: redis:7.4-alpine
    volumes: [cachedata:/data, ./redis.conf:/usr/local/etc/redis/redis.conf]
    healthcheck: { test: ["CMD", "redis-cli", "ping"] }
  api:
    build: ./api
`;
  const prev = await api('POST', '/projects/import/compose/preview', { compose, env: 'API_KEY=k-123' });
  ok(`compose preview: ${prev.plan.services.map(s => `${s.name}(${s.type}${s.warnings.length ? `, ${s.warnings.length} warning` : ''})`).join(', ')}; ${prev.plan.warnings.length} global warning(s)`);
  const badBuild = await call('POST', '/projects/import', { kind: 'compose', name: `stack-${run}`, compose, env: 'API_KEY=k-123' });
  ok(`build service without a repository refused: ${badBuild.status} ${badBuild.data.error.fields?.services}`);
  const imported = await api('POST', '/projects/import', { kind: 'compose', name: `stack-${run}`, compose, env: 'API_KEY=k-123', services: ['web', 'cache'] });
  ok(`compose project created: ${imported.resources.join(', ')}`);
  const stack = (await api('GET', `/projects/${imported.project.id}/resources`)).resources;
  for (const r of stack) await running(r.id);
  const web = stack.find(r => r.name === 'web');
  const env = (await api('GET', `/resources/${web.id}/env`)).variables.map(v => `${v.key}${v.secret ? '(secret)' : ''}`);
  ok(`compose resources running; web env: ${env.join(', ')}; web reaches cache by name: ${sh(web.id, 'nc -z cache 6379 && echo yes || echo no')}`);

  /* -------------------------------- Duplicate -------------------------------- */
  const dup = await api('POST', `/projects/${imported.project.id}/duplicate`, { name: `stack-${run}-copy`, includeVariables: false, deploy: false });
  const dupRes = (await api('GET', `/projects/${dup.project.id}/resources`)).resources;
  const dupEnv = (await api('GET', `/resources/${dupRes.find(r => r.name === 'web').id}/env`)).variables;
  ok(`duplicated: ${dup.resources} resources, statuses ${dupRes.map(r => r.status).join('/')}; secret API_KEY copied empty: ${dupEnv.find(v => v.key === 'API_KEY')?.value === '' || dupEnv.find(v => v.key === 'API_KEY')?.secret}; notes: ${dup.notes.length}`);

  /* -------------------------------- Git import -------------------------------- */
  const insp = await api('POST', '/projects/import/git/inspect', { repoUrl: 'https://github.com/traefik/whoami', branch: 'master' });
  ok(`git inspect: commit ${insp.commit.slice(0, 7)}, dockerfile ${insp.dockerfile}, compose ${!!insp.plan}, suggested name ${insp.suggestedName}`);
  const badRepo = await call('POST', '/projects/import/git/inspect', { repoUrl: 'https://github.com/notsliver/does-not-exist-x', branch: 'main' });
  ok(`missing repository: ${badRepo.status} ${badRepo.data.error.fields?.repoUrl?.slice(0, 90)}`);
  const gitProj = await api('POST', '/projects/import', { kind: 'git', name: `whoami-${run}`, repoUrl: 'https://github.com/traefik/whoami', branch: 'master', port: 80 });
  const gitRes = (await api('GET', `/projects/${gitProj.project.id}/resources`)).resources[0];
  await running(gitRes.id, 900_000);
  ok(`git project built and running: ${gitRes.name} from ${gitRes.repoUrl}`);

  /* -------------------------------- Database imports -------------------------------- */
  const { project } = await api('POST', '/projects', { name: `data-${run}` });
  const envId = project.environments[0].id;
  const make = async (kind, name) => (await api('POST', `/projects/${project.id}/resources`, { ...DBS[kind](name), environmentId: envId })).resource;
  const dbs = {};
  for (const [kind, name] of [['pg', 'pg-a'], ['pg', 'pg-b'], ['mysql', 'mysql'], ['mariadb', 'maria'], ['mongo', 'mongo-a'], ['mongo', 'mongo-b'], ['redis', 'redis'], ['dragonfly', 'dragonfly'], ['clickhouse', 'clickhouse']]) {
    dbs[name] = await make(kind, name);
  }
  ok(`created ${Object.keys(dbs).length} databases; engines: ${(await api('GET', `/projects/${project.id}/resources`)).resources.map(r => `${r.name}=${r.engine}`).join(' ')}`);
  for (const r of Object.values(dbs)) await running(r.id, 900_000);
  ok('all databases running');

  const sql = 'CREATE TABLE people (id int primary key, name varchar(40));\nINSERT INTO people VALUES (1, \'ada\'), (2, \'grace\');\n';
  let imp = await upload(dbs['pg-a'].id, 'people.sql', sql);
  let out = await importDone(imp.id, 'postgres');
  ok(`postgres upload: ${out.import.summary}; rows: ${sh(dbs['pg-a'].id, `PGPASSWORD=${PW} psql -tA -U ignition -d app -c "select count(*) from people"`)}`);
  const again = await upload(dbs['pg-a'].id, 'people.sql', sql);
  const dupOut = await finished(again.id);
  ok(`importing the same rows again fails cleanly: ${dupOut.import.status} — ${dupOut.import.summary.slice(0, 90)}`);
  imp = await upload(dbs['pg-a'].id, 'people.sql', sql, true);
  out = await importDone(imp.id, 'postgres replace');
  ok(`…and with "replace" it succeeds: ${out.import.summary}`);

  imp = (await api('POST', `/resources/${dbs['pg-b'].id}/imports`, { source: 'live', url: `postgres://ignition:${PW}@pg-a:5432/app` })).import;
  out = await importDone(imp.id, 'postgres live');
  ok(`postgres live pg-a → pg-b: rows ${sh(dbs['pg-b'].id, `PGPASSWORD=${PW} psql -tA -U ignition -d app -c "select string_agg(name, ',') from people"`)}; label "${out.import.label}" (no password)`);

  for (const [name, cli] of [['mysql', `mysql -uroot -p${PW}`], ['maria', `mariadb -uroot -p${PW}`]]) {
    imp = await upload(dbs[name].id, 'people.sql.gz', execFileSync('gzip', ['-c'], { input: sql }));
    out = await importDone(imp.id, name);
    ok(`${name} gzipped upload: ${out.import.summary}; rows ${sh(dbs[name].id, `${cli} -N app -e "select count(*) from people" 2>/dev/null`)}`);
  }
  await api('POST', `/resources/${dbs.maria.id}/imports`, { source: 'live', url: `mysql://root:${PW}@mysql:3306/app`, replace: true }).then(r => importDone(r.import.id, 'mysql→mariadb live'));
  ok(`mysql → mariadb live: rows ${sh(dbs.maria.id, `mariadb -uroot -p${PW} -N app -e "select count(*) from people" 2>/dev/null`)}`);

  sh(dbs['mongo-a'].id, `mongosh --quiet -u ignition -p ${PW} --authenticationDatabase admin app --eval 'db.people.insertMany([{n:"ada"},{n:"grace"},{n:"linus"}])'`);
  imp = (await api('POST', `/resources/${dbs['mongo-b'].id}/imports`, { source: 'live', url: `mongodb://ignition:${PW}@mongo-a:27017/?authSource=admin` })).import;
  out = await importDone(imp.id, 'mongo live');
  ok(`mongo live mongo-a → mongo-b: ${sh(dbs['mongo-b'].id, `mongosh --quiet -u ignition -p ${PW} --authenticationDatabase admin app --eval 'db.people.countDocuments()'`)} documents`);
  const archive = execFileSync('docker', ['exec', container(dbs['mongo-a'].id), 'sh', '-c', `mongodump --quiet -u ignition -p ${PW} --authenticationDatabase admin --db app --archive --gzip`]);
  imp = await upload(dbs['mongo-b'].id, 'app.archive.gz', archive, true);
  out = await importDone(imp.id, 'mongo archive');
  ok(`mongo archive upload (--drop): ${sh(dbs['mongo-b'].id, `mongosh --quiet -u ignition -p ${PW} --authenticationDatabase admin app --eval 'db.people.countDocuments()'`)} documents`);

  imp = await upload(dbs.redis.id, 'seed.redis', 'SET greeting hello\nHSET user:1 name ada lang en\nRPUSH queue a b c\n');
  out = await importDone(imp.id, 'redis');
  ok(`redis commands file: ${out.import.summary}; HGET → ${sh(dbs.redis.id, `redis-cli -a ${PW} --no-auth-warning HGET user:1 name`)}`);
  imp = (await api('POST', `/resources/${dbs.dragonfly.id}/imports`, { source: 'live', url: `redis://:${PW}@redis:6379` })).import;
  out = await finished(imp.id);
  ok(`redis → dragonfly replication: ${out.import.status} — ${out.import.summary}`);
  if (out.import.status !== 'succeeded') console.log(out.logs.map(l => '    ' + l.message).slice(-8).join('\n'));

  imp = await upload(dbs.clickhouse.id, 'events.sql', 'CREATE TABLE events (id UInt32, kind String) ENGINE = MergeTree ORDER BY id;\nINSERT INTO events VALUES (1, \'signup\'), (2, \'login\'), (3, \'login\');\n');
  out = await importDone(imp.id, 'clickhouse');
  ok(`clickhouse SQL file: ${out.import.summary}`);

  const history = (await api('GET', `/resources/${dbs['pg-a'].id}/imports`)).imports;
  ok(`pg-a import history: ${history.map(i => i.status).join(', ')}`);
  const badLive = await call('POST', `/resources/${dbs['pg-b'].id}/imports`, { source: 'live', url: 'mysql://x@y/z' });
  const localhost = await call('POST', '/databases/check-source', { engine: 'postgresql', url: 'postgres://u:p@localhost/db' });
  const unreachable = await api('POST', '/databases/check-source', { engine: 'postgresql', url: 'postgres://u:p@10.255.255.1:5432/db' });
  ok(`validation: wrong scheme ${badLive.status} (${badLive.data.error.fields?.url}); localhost: ${localhost.data.error}; unreachable: ${unreachable.error}`);

  /* -------------------------------- Functions -------------------------------- */
  const fnNode = (await api('POST', `/projects/${project.id}/resources`, { environmentId: envId, name: 'dump-server', type: 'function', engine: 'node',
    code: `export default async (req) => new Response("CREATE TABLE fromurl (id int);\\nINSERT INTO fromurl VALUES (42);\\n", { headers: { 'content-type': 'application/sql' } });\n` })).resource;
  const fnPy = (await api('POST', `/projects/${project.id}/resources`, { environmentId: envId, name: 'py', type: 'function', engine: 'python' })).resource;
  await running(fnNode.id);
  await running(fnPy.id);
  let inv = (await api('POST', `/resources/${fnPy.id}/function/invoke`, { method: 'GET', path: '/?name=ada' })).result;
  ok(`python function GET: ${inv.status} ${inv.body} (${inv.durationMs} ms)`);
  inv = (await api('POST', `/resources/${fnPy.id}/function/invoke`, { method: 'POST', path: '/', body: '{"a":1}' })).result;
  ok(`python function POST: ${inv.status} ${inv.body}`);
  await api('PUT', `/resources/${fnPy.id}/function`, { code: 'def handler(request):\n    raise ValueError("boom")\n', deploy: true });
  await sleep(3000);
  await running(fnPy.id);
  await until('new code', async () => (await api('POST', `/resources/${fnPy.id}/function/invoke`, { method: 'GET', path: '/' })).result.status === 500, 120_000);
  inv = (await api('POST', `/resources/${fnPy.id}/function/invoke`, { method: 'GET', path: '/' })).result;
  ok(`updated python function surfaces errors: ${inv.status} ${inv.body}`);
  const big = await call('PUT', `/resources/${fnPy.id}/function`, { code: 'x'.repeat(120 * 1024) });
  ok(`oversized handler refused: ${big.status} ${big.data.error.fields?.code}`);

  // URL import served by the Node function.
  const fnRes = (await api('GET', `/resources/${fnNode.id}`)).resource;
  imp = (await api('POST', `/resources/${dbs['pg-b'].id}/imports`, { source: 'url', url: `http://host.docker.internal:${fnRes.hostPort}/dump.sql` })).import;
  out = await importDone(imp.id, 'url');
  ok(`URL import (from the node function): ${sh(dbs['pg-b'].id, `PGPASSWORD=${PW} psql -tA -U ignition -d app -c "select id from fromurl"`)}`);

  /* -------------------------------- External + connection -------------------------------- */
  const ext = (await api('POST', `/projects/${project.id}/external-databases`, { environmentId: envId, name: 'legacy', engine: 'postgresql', url: 'postgres://ignition:secret@db:5432/ignition' })).resource;
  const t = await api('POST', `/resources/${ext.id}/connection/test`);
  const extDeploy = await call('POST', `/resources/${ext.id}/deployments`, {});
  const extConn = await api('GET', `/resources/${ext.id}/connection`);
  ok(`external database: test ${t.ok ? 'reachable' : t.error} (${t.latencyMs} ms); deploy refused ${extDeploy.status}; masked ${extConn.maskedUrl}`);
  const extBad = await call('POST', `/projects/${project.id}/external-databases`, { name: 'bad', engine: 'mysql', host: 'localhost' });
  ok(`external validation: ${JSON.stringify(extBad.data.error.fields)}`);
  const conn = await api('GET', `/resources/${dbs['pg-a'].id}/connection`);
  ok(`pg-a connection: ${conn.maskedUrl}`);
  const shared = await api('POST', `/resources/${dbs['pg-a'].id}/connection/share`, { targetId: fnNode.id, key: 'DATABASE_URL' });
  const fnEnv = (await api('GET', `/resources/${fnNode.id}/env`)).variables.find(v => v.key === 'DATABASE_URL');
  ok(`shared with ${shared.target}: ${fnEnv?.secret ? 'DATABASE_URL (secret)' : 'missing'}; redeploy needed: ${shared.requiresRedeploy}`);
  console.log(JSON.stringify({ projectId: project.id, pgId: dbs['pg-a'].id, fnId: fnNode.id, extId: ext.id, stackId: imported.project.id }));
} catch (e) {
  console.log('✗', e.message);
  process.exitCode = 1;
}
