import { useId, useState } from 'react';
import './Sparkline.css';

const W = 200, H = 44;

// Keep the tooltip inside the chart near the edges instead of centring it over the cursor.
const tipPosition = fraction => ({
  left: `${fraction * 100}%`,
  transform: fraction > 0.85 ? 'translateX(-100%)' : fraction < 0.15 ? 'none' : 'translateX(-50%)',
});

/**
 * Single-series area sparkline with a hover crosshair.
 * `data` may contain null for missing samples; those are drawn as gaps, never as zero.
 * `labels[i]` describes each point in the tooltip (defaults to "N ago" in minutes).
 * `limit` draws a dashed reference line (e.g. a memory limit).
 */
export function Sparkline({ data, max, label, format = v => v, labels, limit }) {
  const [hover, setHover] = useState(null);
  const gradient = useId();
  const values = data.filter(v => v != null);
  const top = max ?? Math.max(1e-9, ...values, limit ?? 0) * 1.15;
  const x = i => (data.length < 2 ? W / 2 : (i / (data.length - 1)) * W);
  const y = v => H - 2 - (Math.min(v, top) / top) * (H - 4);

  // Split into continuous runs so gaps stay empty.
  const runs = [];
  data.forEach((v, i) => {
    if (v == null) return;
    if (i === 0 || data[i - 1] == null) runs.push([]);
    runs.at(-1).push(i);
  });
  const line = runs.map(run => run.map((i, n) => `${n ? 'L' : 'M'}${x(i).toFixed(1)},${y(data[i]).toFixed(1)}`).join('')).join('');
  const area = runs.map(run => `M${x(run[0]).toFixed(1)},${H}` + run.map(i => `L${x(i).toFixed(1)},${y(data[i]).toFixed(1)}`).join('') + `L${x(run.at(-1)).toFixed(1)},${H}Z`).join('');

  function onMove(event) {
    const rect = event.currentTarget.getBoundingClientRect();
    setHover(Math.max(0, Math.min(data.length - 1, Math.round(((event.clientX - rect.left) / rect.width) * (data.length - 1)))));
  }

  return <div className="sparkline" onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
    <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={`${label}: ${values.length ? `latest ${format(values.at(-1))}` : 'no data'}`}>
      <defs><linearGradient id={gradient} x1="0" x2="0" y1="0" y2="1"><stop offset="0" stopColor="currentColor" stopOpacity=".16" /><stop offset="1" stopColor="currentColor" stopOpacity="0" /></linearGradient></defs>
      {limit != null && limit <= top && <line className="sparkline-limit" x1="0" x2={W} y1={y(limit)} y2={y(limit)} vectorEffect="non-scaling-stroke" />}
      <path d={area} fill={`url(#${gradient})`} />
      <path d={line} fill="none" stroke="currentColor" strokeWidth="1.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
      {runs.filter(r => r.length === 1).map(([i]) => <circle key={i} cx={x(i)} cy={y(data[i])} r="1.5" fill="currentColor" />)}
      {hover != null && <line className="sparkline-cursor" x1={x(hover)} x2={x(hover)} y1="0" y2={H} vectorEffect="non-scaling-stroke" />}
    </svg>
    {hover != null && <span className="sparkline-tip" style={tipPosition(hover / Math.max(1, data.length - 1))}>
      {data[hover] == null ? 'No data' : format(data[hover])}<span>{labels?.[hover] ?? `${data.length - 1 - hover}m ago`}</span>
    </span>}
  </div>;
}
