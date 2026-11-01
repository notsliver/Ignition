import { cx } from '../../utils/cx';
import './Meter.css';

export function Meter({ label, value, compact }) {
  const level = value >= 90 ? 'critical' : value >= 70 ? 'high' : null;
  return <div className={cx('meter', level, compact && 'compact')}>
    {!compact && <span>{label}</span>}
    <div className="meter-track" role="meter" aria-label={label} aria-valuenow={Math.round(value)} aria-valuemin={0} aria-valuemax={100}><div className="meter-fill" style={{ width: `${Math.min(Math.max(value, 0), 100)}%` }} /></div>
    <span className="meter-value">{Math.round(value)}%</span>
  </div>;
}
