// Every variable each database image understands, with its default and what it does.
// `init`: read only when the data directory is first created, so changing it later doesn't change
// existing users or passwords (the fix is shown instead). `generate`: offer a generated password.

const pw = { secret: true, generate: true };

export const engineEnv = {
  postgresql: {
    label: 'PostgreSQL',
    vars: [
      { key: 'POSTGRES_PASSWORD', default: '', required: true, init: true, ...pw, does: 'Password of the superuser.' },
      { key: 'POSTGRES_USER', default: 'postgres', init: true, does: 'Superuser that\'s created.' },
      { key: 'POSTGRES_DB', default: '= POSTGRES_USER', init: true, does: 'Database created on first start.' },
      { key: 'POSTGRES_INITDB_ARGS', default: '', init: true, does: 'Extra initdb arguments, e.g. --data-checksums or --locale-provider=icu.' },
      { key: 'POSTGRES_INITDB_WALDIR', default: '', init: true, does: 'Separate directory for the write-ahead log.' },
      { key: 'POSTGRES_HOST_AUTH_METHOD', default: 'scram-sha-256', init: true, does: 'How other hosts authenticate. Never set trust on a reachable database.' },
      { key: 'PGDATA', default: '/var/lib/postgresql/data', danger: true, does: 'Data directory. Moving it outside the volume loses data on redeploy.' },
      { key: 'TZ', default: 'UTC', does: 'Time zone of the server process.' },
    ],
    change: { POSTGRES_PASSWORD: 'ALTER USER <user> PASSWORD \'…\';  (in the Terminal: psql -U <user>)' },
  },
  mysql: {
    label: 'MySQL',
    vars: [
      { key: 'MYSQL_ROOT_PASSWORD', default: '', required: true, init: true, ...pw, does: 'Password of root.' },
      { key: 'MYSQL_DATABASE', default: '', init: true, does: 'Database created on first start.' },
      { key: 'MYSQL_USER', default: '', init: true, does: 'User created with full access to MYSQL_DATABASE.' },
      { key: 'MYSQL_PASSWORD', default: '', init: true, ...pw, does: 'Password of MYSQL_USER.' },
      { key: 'MYSQL_ROOT_HOST', default: '%', init: true, does: 'Hosts root may connect from.' },
      { key: 'MYSQL_ALLOW_EMPTY_PASSWORD', default: 'no', init: true, danger: true, does: 'Allow root without a password. Don\'t.' },
      { key: 'MYSQL_RANDOM_ROOT_PASSWORD', default: 'no', init: true, does: 'Generate a random root password (printed once in the logs).' },
      { key: 'MYSQL_ONETIME_PASSWORD', default: 'no', init: true, does: 'Expire root\'s password after the first login.' },
      { key: 'MYSQL_INITDB_SKIP_TZINFO', default: '', init: true, does: 'Skip loading time zone tables (faster first start).' },
      { key: 'TZ', default: 'UTC', does: 'Time zone of the server process.' },
    ],
    change: { MYSQL_ROOT_PASSWORD: 'ALTER USER \'root\'@\'%\' IDENTIFIED BY \'…\';', MYSQL_PASSWORD: 'ALTER USER \'<user>\'@\'%\' IDENTIFIED BY \'…\';' },
  },
  mariadb: {
    label: 'MariaDB',
    vars: [
      { key: 'MARIADB_ROOT_PASSWORD', default: '', required: true, init: true, ...pw, does: 'Password of root.' },
      { key: 'MARIADB_DATABASE', default: '', init: true, does: 'Database created on first start.' },
      { key: 'MARIADB_USER', default: '', init: true, does: 'User created with full access to MARIADB_DATABASE.' },
      { key: 'MARIADB_PASSWORD', default: '', init: true, ...pw, does: 'Password of MARIADB_USER.' },
      { key: 'MARIADB_ROOT_HOST', default: '%', init: true, does: 'Hosts root may connect from.' },
      { key: 'MARIADB_AUTO_UPGRADE', default: '', does: 'Run mariadb-upgrade after a version change (recommended: 1).' },
      { key: 'MARIADB_MYSQL_LOCALHOST_USER', default: '', init: true, does: 'Create a mysql@localhost user for health checks.' },
      { key: 'MARIADB_ALLOW_EMPTY_ROOT_PASSWORD', default: '', init: true, danger: true, does: 'Allow root without a password. Don\'t.' },
      { key: 'MARIADB_RANDOM_ROOT_PASSWORD', default: '', init: true, does: 'Generate a random root password (printed once in the logs).' },
      { key: 'MARIADB_INITDB_SKIP_TZINFO', default: '', init: true, does: 'Skip loading time zone tables.' },
      { key: 'TZ', default: 'UTC', does: 'Time zone of the server process.' },
    ],
    change: { MARIADB_ROOT_PASSWORD: 'ALTER USER \'root\'@\'%\' IDENTIFIED BY \'…\';', MARIADB_PASSWORD: 'ALTER USER \'<user>\'@\'%\' IDENTIFIED BY \'…\';' },
  },
  mongodb: {
    label: 'MongoDB',
    vars: [
      { key: 'MONGO_INITDB_ROOT_USERNAME', default: '', required: true, init: true, does: 'Root user (in the admin database). Without it, MongoDB runs with no authentication.' },
      { key: 'MONGO_INITDB_ROOT_PASSWORD', default: '', required: true, init: true, ...pw, does: 'Password of the root user.' },
      { key: 'MONGO_INITDB_DATABASE', default: 'test', init: true, does: 'Database the init scripts run against.' },
      { key: 'TZ', default: 'UTC', does: 'Time zone of the server process.' },
    ],
    change: { MONGO_INITDB_ROOT_PASSWORD: 'db.getSiblingDB("admin").changeUserPassword("<user>", "…")' },
  },
  redis: {
    label: 'Redis',
    vars: [
      { key: 'REDIS_PASSWORD', default: '', required: true, ...pw, does: 'Required password (Ignition passes it as --requirepass). Applies on the next deploy.' },
      { key: 'TZ', default: 'UTC', does: 'Time zone of the server process.' },
    ],
    note: 'Other Redis settings are command-line flags: add them to the start command under Advanced Settings (e.g. --maxmemory 256mb --maxmemory-policy allkeys-lru).',
  },
  dragonfly: {
    label: 'Dragonfly',
    vars: [
      { key: 'DFLY_requirepass', default: '', required: true, ...pw, does: 'Required password.' },
      { key: 'DFLY_maxmemory', default: '0 (available memory)', does: 'Memory limit, e.g. 512mb. Keep it under the container\'s limit.' },
      { key: 'DFLY_cache_mode', default: 'false', does: 'Evict keys when full instead of refusing writes.' },
      { key: 'DFLY_proactor_threads', default: '0 (one per CPU)', does: 'Worker threads.' },
      { key: 'DFLY_snapshot_cron', default: '', does: 'Snapshot schedule, e.g. */30 * * * *.' },
      { key: 'DFLY_dbfilename', default: 'dump-{timestamp}', does: 'Snapshot file name.' },
      { key: 'DFLY_dir', default: '/data', does: 'Where snapshots are written (keep it on the volume).' },
    ],
    note: 'Dragonfly reads any flag as DFLY_<flag>.',
  },
  clickhouse: {
    label: 'ClickHouse',
    vars: [
      { key: 'CLICKHOUSE_USER', default: 'default', init: true, does: 'User that\'s created.' },
      { key: 'CLICKHOUSE_PASSWORD', default: '', required: true, init: true, ...pw, does: 'Its password. Empty means the user only works from inside the container.' },
      { key: 'CLICKHOUSE_DB', default: 'default', init: true, does: 'Database created on first start.' },
      { key: 'CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT', default: '0', init: true, does: 'Let the user manage other users and grants with SQL.' },
      { key: 'CLICKHOUSE_SKIP_USER_SETUP', default: '0', init: true, does: 'Skip creating the user (keep the image\'s passwordless default).' },
      { key: 'TZ', default: 'UTC', does: 'Time zone of the server process.' },
    ],
    change: { CLICKHOUSE_PASSWORD: 'ALTER USER <user> IDENTIFIED BY \'…\';' },
  },
  external: {
    label: 'External database',
    vars: [
      { key: 'DB_HOST', default: '', required: true, does: 'Where it runs.' },
      { key: 'DB_PORT', default: '', required: true, does: 'Its port.' },
      { key: 'DB_NAME', default: '', does: 'Database to use.' },
      { key: 'DB_USER', default: '', does: 'User to connect as.' },
      { key: 'DB_PASSWORD', default: '', secret: true, does: 'That user\'s password.' },
      { key: 'DATABASE_URL', default: '', secret: true, does: 'The full connection URL apps get with "Use in an app". Update it when you change the parts.' },
    ],
    note: 'Ignition checks DB_HOST:DB_PORT every minute. Changes apply right away; nothing is redeployed.',
  },
};

export const generatePassword = () => Array.from(crypto.getRandomValues(new Uint32Array(24)), n => 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'[n % 56]).join('');
