import {
  BeakerIcon, BoltIcon, ChatBubbleLeftRightIcon, CircleStackIcon, CloudIcon, CodeBracketIcon, CogIcon, CommandLineIcon, CpuChipIcon, CubeIcon, FireIcon,
  FolderOpenIcon, GlobeAltIcon, HeartIcon, MusicalNoteIcon, PuzzlePieceIcon, RocketLaunchIcon, ServerIcon, ShieldCheckIcon, ShoppingCartIcon, SparklesIcon, StarIcon, TrophyIcon, WrenchIcon,
} from '@heroicons/react/16/solid';
import { logos } from '../../project/game/logos';
import { cx } from '../../../ui';

// What a product can run, in the order the editor offers them.
export const KINDS = [
  { id: 'game', label: 'Game server', icon: PuzzlePieceIcon, hue: 'game', text: 'Minecraft with Paper, Fabric, Forge, modpacks and more.' },
  { id: 'files', label: 'Bring your own files', icon: FolderOpenIcon, hue: 'files', text: 'A ready runtime (static site, Node.js, Bun, Python, PHP). The team uploads its code with Files.' },
  { id: 'application', label: 'Application', icon: CubeIcon, hue: 'app', text: 'A web app from a container image or Git repository, with a public port.' },
  { id: 'database', label: 'Database', icon: CircleStackIcon, hue: 'db', text: 'PostgreSQL, MySQL, MariaDB, MongoDB, Redis and others, with a persistent volume.' },
  { id: 'worker', label: 'Worker', icon: CogIcon, hue: 'worker', text: 'A background process without a public port, like a queue consumer or bot.' },
  { id: 'function', label: 'Function', icon: BoltIcon, hue: 'fn', text: 'One handler file in Node.js, Bun or Python. No Dockerfile.' },
];
export const kindOf = id => KINDS.find(k => k.id === id) ?? KINDS[2];

// Runtimes for "Bring your own files": the image, where the team's files live, and how it starts.
// Each waits politely until there's something to run, so a fresh service doesn't crash-loop.
const wait = dir => `echo "Waiting for your app: upload it to ${dir} under Files, then restart." && exec sleep infinity`;
export const RUNTIMES = [
  { id: 'static', label: 'Static website', text: 'HTML, CSS and JavaScript, served by nginx.', image: 'nginx:1.27-alpine', dataPath: '/usr/share/nginx/html', port: 80, command: '' },
  { id: 'node', label: 'Node.js 22', text: 'Runs npm start after installing packages.', image: 'node:22-alpine', dataPath: '/app', port: 3000,
    command: `cd /app && if [ -f package.json ]; then (npm ci --omit=dev || npm install --omit=dev) && exec npm start; else ${wait('/app')}; fi` },
  { id: 'bun', label: 'Bun 1.2', text: 'Runs bun start after installing packages.', image: 'oven/bun:1.2-alpine', dataPath: '/app', port: 3000,
    command: `cd /app && if [ -f package.json ]; then bun install --production && exec bun run start; else ${wait('/app')}; fi` },
  { id: 'python', label: 'Python 3.12', text: 'Installs requirements.txt and runs main.py.', image: 'python:3.12-slim', dataPath: '/app', port: 8000,
    command: `cd /app && if [ -f main.py ]; then ([ -f requirements.txt ] && pip install --no-cache-dir -r requirements.txt || true) && exec python main.py; else ${wait('/app')}; fi` },
  { id: 'php', label: 'PHP 8.3', text: 'Apache with PHP. index.php or index.html.', image: 'php:8.3-apache', dataPath: '/var/www/html', port: 80, command: '' },
  { id: 'custom', label: 'Custom image', text: 'Any image; the team\'s files go to the folder you choose.', image: '', dataPath: '/data', port: 8080, command: '' },
];

export const PERIODS = [{ value: 'month', label: 'per month' }, { value: 'quarter', label: 'per quarter' }, { value: 'year', label: 'per year' }, { value: 'once', label: 'one-time' }];
const PERIOD_SHORT = { month: '/mo', quarter: '/qtr', year: '/yr', once: ' once' };
const SYMBOL = { USD: '$', EUR: '€', GBP: '£', JPY: '¥', INR: '₹', AUD: 'A$', CAD: 'C$' };

export function price(plan) {
  if (!plan.price) return '';
  const sym = SYMBOL[plan.currency];
  return `${sym ?? ''}${plan.price}${sym ? '' : ` ${plan.currency}`}${PERIOD_SHORT[plan.period] ?? ''}`;
}
export const memory = mb => mb >= 1024 ? `${+(mb / 1024).toFixed(1)} GB` : `${mb} MB`;
export function specs(plan) {
  const parts = [`${plan.cpus} CPU`, memory(plan.memoryMb)];
  if (plan.storageGb) parts.push(`${plan.storageGb} GB disk`);
  if (plan.backupGb) parts.push(`${plan.backupGb} GB backups`);
  if (plan.ports) parts.push(`${plan.ports} ${plan.ports === 1 ? 'port' : 'ports'}`);
  return parts.join(' · ');
}

// What a product runs, in a few words: "Paper 1.21.1", "PostgreSQL 17", "Node.js 22".
export function summary(product) {
  const t = product.template ?? {};
  const asks = (t._ask ?? []).map(a => a.key);
  if (product.kind === 'game') return [t._ui?.editionName ?? t.spec?.edition, asks.includes('version') ? 'any version' : t.spec?.version || 'latest'].filter(Boolean).join(' ');
  if (product.kind === 'database') return t._ui ? `${t._ui.engine} ${asks.includes('version') ? '(version picked)' : t._ui.version}` : t.image;
  if (product.kind === 'function') return t._ui?.runtimeLabel ?? t.engine;
  if (product.kind === 'files') return RUNTIMES.find(r => r.id === t._ui?.runtime)?.label ?? t.image;
  if (asks.includes('source')) return 'source picked when assigning';
  return t.sourceType === 'git' ? (t.repoUrl ?? '').replace(/^https:\/\//, '') : t.image;
}

// Icons a product can use, and the colours for them.
export const ICONS = { CubeIcon, ServerIcon, GlobeAltIcon, CircleStackIcon, CloudIcon, RocketLaunchIcon, BoltIcon, PuzzlePieceIcon, TrophyIcon, FireIcon, SparklesIcon, StarIcon,
  HeartIcon, ShieldCheckIcon, CpuChipIcon, CommandLineIcon, CodeBracketIcon, BeakerIcon, ChatBubbleLeftRightIcon, MusicalNoteIcon, ShoppingCartIcon, WrenchIcon, CogIcon, FolderOpenIcon };
export const COLORS = ['#4f7cff', '#22a06b', '#d08a1c', '#9b5de5', '#e2554f', '#0ea5a4', '#ec4899', '#64748b'];
export const parseIcon = icon => {
  if (icon?.startsWith('hero:')) { const [, name, color] = icon.split(':'); return { type: 'hero', name, color }; }
  if (icon?.startsWith('data:image/')) return { type: 'image', src: icon };
  return { type: 'auto' };
};

/** The product's mark: its own icon, else an edition logo for games, else the kind's icon. */
export function ProductMark({ product, kind, size = 36 }) {
  const k = kindOf(kind ?? product?.kind);
  const own = parseIcon(product?.icon);
  if (own.type === 'image') return <span className="pd-mark image" style={{ width: size, height: size }}><img src={own.src} alt="" /></span>;
  if (own.type === 'hero' && ICONS[own.name]) {
    const Icon = ICONS[own.name];
    return <span className="pd-mark" style={{ width: size, height: size, '--pd-hue': own.color }}><Icon /></span>;
  }
  const logo = (kind ?? product?.kind) === 'game' && logos[product?.template?.spec?.edition];
  return <span className={cx('pd-mark', `hue-${k.hue}`)} style={{ width: size, height: size }}>
    {logo ? <img src={logo} alt="" /> : <k.icon />}
  </span>;
}

export const STRATEGIES = [
  { id: 'least-utilized', label: 'Least utilized', text: 'The server with the smallest share of its memory committed.' },
  { id: 'most-available', label: 'Most available capacity', text: 'The server with the most free memory.' },
  { id: 'spread', label: 'Spread evenly', text: 'The server with the fewest services of this product (round robin).' },
  { id: 'random', label: 'Random', text: 'Any eligible server with room.' },
  { id: 'location', label: 'Preferred location', text: 'A server in your preferred location first, then the least utilized.' },
  { id: 'manual', label: 'Choose when assigning', text: 'You pick the server in the Assign dialog.' },
];

// Stock left, from the product's placement and its active services.
export function stockLeft(product) {
  const stock = product.placement?.stock;
  return stock ? Math.max(stock - product.services, 0) : null;
}

export const STATUS = { draft: { label: 'Draft', tone: 'neutral' }, active: { label: 'Active', tone: 'success' }, retired: { label: 'Retired', tone: 'warning' } };
export const slug = s => (s || '').toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40) || 'service';

// What teams can change on a service afterwards, per kind (the server enforces these).
export const ALLOW = {
  source: { label: 'Its source', text: 'Switch the image, repository or branch.' },
  command: { label: 'Its start command', text: 'Change how it starts.' },
  env: { label: 'Environment variables', text: 'Add and edit variables and secrets.' },
  version: { label: 'Server type and version', text: 'Switch edition, game version, loader or modpack.' },
};
export const ALLOW_FOR = { application: ['source', 'command', 'env'], worker: ['source', 'command', 'env'], files: ['command', 'env'], game: ['version'], database: ['env'], function: ['env'] };
export const ALLOW_DEFAULT = { application: ['command', 'env'], worker: ['command', 'env'], files: ['command', 'env'], game: ['version'], database: [], function: ['env'] };
