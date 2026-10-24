import { useId, useState } from 'react';
import './Sparkline.css';

const W = 200, H = 44;

/** Single-series area sparkline with a hover crosshair. `format` renders tooltip values. */
export function Sparkline({ data, max, label, format = v => v }) {
  const [hover, setHover] = useState(null);
  const gradient = useId();
  const top = max ?? Math.max(...data) * 1.15;
  const x = i => (i / (data.length - 1)) * W;
  const y = v => H - 2 - (v / top) * (H - 4);
  const line = data.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join('');

  function onMove(event) {
    const rect = event.currentTarget.getBoundingClientRect();
    setHover(Math.round(((event.clientX - rect.left) / rect.width) * (data.length - 1)));
  }

  return <div className="sparkline" onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
    <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={`${label}, last ${data.length} minutes`}>
      <defs><linearGradient id={gradient} x1="0" x2="0" y1="0" y2="1"><stop offset="0" stopColor="currentColor" stopOpacity=".16" /><stop offset="1" stopColor="currentColor" stopOpacity="0" /></linearGradient></defs>
      <path d={`${line}L${W},${H}L0,${H}Z`} fill={`url(#${gradient})`} />
      <path d={line} fill="none" stroke="currentColor" strokeWidth="1.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
      {hover != null && <line className="sparkline-cursor" x1={x(hover)} x2={x(hover)} y1="0" y2={H} vectorEffect="non-scaling-stroke" />}
    </svg>
    {hover != null && <span className="sparkline-tip" style={{ left: `${(hover / (data.length - 1)) * 100}%` }}>{format(data[hover])}<span>{data.length - 1 - hover}m ago</span></span>}
  </div>;
}
