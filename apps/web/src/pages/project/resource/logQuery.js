// Log parsing and the filter language for the Logs page. Kept free of React so it's easy to test.

const LEVEL_ALIASES = {
  fatal: 'error', panic: 'error', critical: 'error', crit: 'error', emerg: 'error', alert: 'error', error: 'error', err: 'error',
  warning: 'warn', warn: 'warn', notice: 'info', info: 'info', information: 'info', debug: 'debug', trace: 'debug', verbose: 'debug',
};
// pino / bunyan numeric levels
const numericLevel = n => n >= 50 ? 'error' : n >= 40 ? 'warn' : n >= 30 ? 'info' : 'debug';
const LEVEL_KEYS = ['level', 'lvl', 'severity', 'log.level', 'loglevel'];
const MESSAGE_KEYS = ['msg', 'message', 'event', 'error.message', 'err.message'];
const TIME_KEYS = ['time', 'ts', 'timestamp', '@timestamp', 't'];

// Free text has no structure, so infer a level from common markers.
export function guessLevel(message) {
  if (/\b(fatal|panic|error|err|exception|traceback|unhandled)\b/i.test(message)) return 'error';
  if (/\b(warn|warning|deprecated)\b/i.test(message)) return 'warn';
  if (/\b(debug|trace)\b/i.test(message)) return 'debug';
  return 'info';
}

export function normalizeLevel(value) {
  if (typeof value === 'number') return numericLevel(value);
  return LEVEL_ALIASES[String(value ?? '').toLowerCase().trim()] ?? null;
}

// { a: { b: 1 } } → { 'a.b': 1 }. Arrays are kept as JSON text.
export function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, key, out);
    else out[key] = Array.isArray(v) ? JSON.stringify(v) : v;
  }
  return out;
}

const LOGFMT_PAIR = /([A-Za-z_][\w.-]*)=("(?:[^"\\]|\\.)*"|\S*)/g;

// Turns one raw Docker line into { level, message, fields, format }.
export function parseMessage(raw, stream) {
  const text = raw.trimEnd();
  const trimmed = text.trim();
  let fields = null, format = 'text';
  if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
    try { fields = flatten(JSON.parse(trimmed)); format = 'json'; } catch { /* not JSON after all */ }
  } else if (/^[A-Za-z_][\w.-]*=/.test(trimmed)) {
    const pairs = [...trimmed.matchAll(LOGFMT_PAIR)];
    if (pairs.length >= 2) {
      fields = Object.fromEntries(pairs.map(([, k, v]) => [k, v.startsWith('"') ? v.slice(1, -1).replace(/\\"/g, '"') : v]));
      format = 'logfmt';
    }
  }
  if (!fields) return { level: guessLevel(text), message: text, fields: {}, format };
  const take = keys => {
    for (const k of keys) if (fields[k] !== undefined && fields[k] !== '') { const v = fields[k]; delete fields[k]; return v; }
    return undefined;
  };
  const levelValue = take(LEVEL_KEYS);
  const message = take(MESSAGE_KEYS);
  take(TIME_KEYS); // Docker's timestamp is authoritative; drop the duplicate
  const level = normalizeLevel(levelValue) ?? (stream === 'stderr' && message ? guessLevel(String(message)) : guessLevel(trimmed));
  return { level, message: message !== undefined ? String(message) : trimmed, fields, format };
}

/*
 * Filter language:
 *   timeout                 contains (case-insensitive)
 *   "connection refused"    exact phrase
 *   /5\d\d/i                regular expression
 *   -healthcheck            exclude
 *   level:error stream:stderr
 *   status:500 user.id:42   structured fields (JSON / logfmt)
 *   status:>=500 ms:>1000   numeric comparisons
 */
const TOKEN = /-?(?:"[^"]*"|\/(?:[^/\\]|\\.)+\/[gimsuy]*|[^\s"]+(?::"[^"]*")?)/g;

export function parseQuery(query) {
  const terms = [];
  for (let raw of query.match(TOKEN) ?? []) {
    const negate = raw.startsWith('-') && raw.length > 1;
    if (negate) raw = raw.slice(1);
    let term;
    const re = raw.match(/^\/((?:[^/\\]|\\.)+)\/([gimsuy]*)$/);
    if (re) {
      try { term = { type: 'regex', re: new RegExp(re[1], re[2].replace('g', '') || 'i') }; } catch { term = { type: 'text', value: raw.toLowerCase() }; }
    } else if (raw.startsWith('"') && raw.endsWith('"') && raw.length > 1) {
      term = { type: 'text', value: raw.slice(1, -1).toLowerCase() };
    } else if (/^[A-Za-z_@][\w.@-]*:/.test(raw) && !/^https?:/i.test(raw)) {
      const i = raw.indexOf(':');
      let value = raw.slice(i + 1);
      if (value.startsWith('"') && value.endsWith('"')) value = value.slice(1, -1);
      const cmp = value.match(/^(>=|<=|>|<)(-?\d+(?:\.\d+)?)$/);
      term = cmp ? { type: 'cmp', key: raw.slice(0, i), op: cmp[1], num: Number(cmp[2]) } : { type: 'field', key: raw.slice(0, i), value: value.toLowerCase() };
    } else {
      term = { type: 'text', value: raw.toLowerCase() };
    }
    terms.push({ ...term, negate });
  }
  return terms;
}

function termMatches(line, t) {
  switch (t.type) {
    case 'regex': return t.re.test(line.message) || t.re.test(line.raw);
    case 'text': return line.search.includes(t.value);
    case 'cmp': {
      const v = Number(line.fields[t.key]);
      if (Number.isNaN(v)) return false;
      return t.op === '>' ? v > t.num : t.op === '<' ? v < t.num : t.op === '>=' ? v >= t.num : v <= t.num;
    }
    case 'field': {
      if (t.key === 'level') return line.level === (normalizeLevel(t.value) ?? t.value);
      if (t.key === 'stream') return line.stream === t.value;
      const v = line.fields[t.key];
      return v !== undefined && String(v).toLowerCase().includes(t.value);
    }
    default: return true;
  }
}

export const matchesQuery = (line, terms) => terms.every(t => termMatches(line, t) !== t.negate);

// Character ranges in `text` to highlight for the positive text/regex terms.
export function highlightRanges(text, terms) {
  const ranges = [];
  const lower = text.toLowerCase();
  for (const t of terms) {
    if (t.negate) continue;
    if (t.type === 'text' && t.value) {
      for (let i = lower.indexOf(t.value); i >= 0; i = lower.indexOf(t.value, i + t.value.length)) ranges.push([i, i + t.value.length]);
    } else if (t.type === 'regex') {
      const g = new RegExp(t.re.source, t.re.flags.includes('g') ? t.re.flags : t.re.flags + 'g');
      for (const m of text.matchAll(g)) if (m[0]) ranges.push([m.index, m.index + m[0].length]);
    }
  }
  ranges.sort((a, b) => a[0] - b[0]);
  const merged = [];
  for (const r of ranges) {
    const last = merged.at(-1);
    if (last && r[0] <= last[1]) last[1] = Math.max(last[1], r[1]); else merged.push([...r]);
  }
  return merged;
}

// A filter token for a field value, quoted when needed.
export const fieldToken = (key, value, exclude = false) => {
  const v = String(value);
  return `${exclude ? '-' : ''}${key}:${/[\s"]/.test(v) || v === '' ? `"${v.replace(/"/g, '')}"` : v}`;
};
