import { useEffect, useRef, useState } from 'react';
import { CheckIcon, ClipboardDocumentIcon } from '@heroicons/react/16/solid';
import { cx } from '../../../ui';
import '../../settings/settings.css';
import './resource.css';

/** Local form state with dirty tracking, for pages that end in a SaveBar. */
export function useSettingsForm(initial) {
  const [saved, setSaved] = useState(initial);
  const [values, setValues] = useState(initial);
  const set = key => value => setValues(v => ({ ...v, [key]: value }));
  return {
    values,
    saved,
    set,
    bind: key => ({ id: key, value: values[key], onChange: set(key) }),
    bindInput: key => ({ id: key, value: values[key], onChange: event => set(key)(event.target.value) }),
    dirty: JSON.stringify(values) !== JSON.stringify(saved),
    save: () => setSaved(values),
    discard: () => setValues(saved),
  };
}

/** setTimeout that is cleared when the component unmounts. */
export function useLater() {
  const timers = useRef([]);
  useEffect(() => () => timers.current.forEach(clearTimeout), []);
  return (fn, ms) => timers.current.push(setTimeout(fn, ms));
}

export function CopyButton({ value, label = 'Copy' }) {
  const [copied, setCopied] = useState(false);
  const later = useLater();
  return <button type="button" className="icon-action" aria-label={label} title={copied ? 'Copied' : 'Copy'} onClick={() => {
    navigator.clipboard?.writeText(value);
    setCopied(true);
    later(() => setCopied(false), 1200);
  }}>{copied ? <CheckIcon /> : <ClipboardDocumentIcon />}</button>;
}

export function PageIntro({ children, actions }) {
  return <div className="rp-intro"><p>{children}</p>{actions && <div className="rp-intro-actions">{actions}</div>}</div>;
}

export function Empty({ icon: Icon, title, children, action }) {
  return <div className="rp-empty">{Icon && <Icon />}<strong>{title}</strong>{children && <p>{children}</p>}{action}</div>;
}

export function Table({ columns, children, className }) {
  return <div className={cx('rp-table-wrap', className)}>
    <table className="rp-table">
      <thead><tr>{columns.map((c, i) => <th key={i} className={c.className}>{c.label}</th>)}</tr></thead>
      <tbody>{children}</tbody>
    </table>
  </div>;
}

// Deterministic series ending at `last`, used for placeholder charts.
export const series = (last, spread, seed, length = 60) => Array.from({ length }, (_, i) =>
  i === length - 1 ? last : Math.max(0, last + spread * (Math.sin(i * 0.37 + seed) * 0.6 + Math.sin(i * 1.3 + seed * 2) * 0.4)));

export const isPublicUrl = url => url && !url.startsWith('internal') && url !== 'external';
export const portOf = resource => resource.url?.match(/:(\d+)$/)?.[1] ?? (resource.type === 'database' ? '5432' : '3000');
export const randomId = (length = 9) => Array.from(crypto.getRandomValues(new Uint8Array(length)), n => 'abcdefghijkmnpqrstuvwxyz0123456789'[n % 34]).join('');
