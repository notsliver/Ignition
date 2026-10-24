import { cx } from '../../utils/cx';
import './Status.css';

/** tone: 'success' | 'warning' | 'danger' | 'progress' | 'neutral' */
export function Status({ tone = 'neutral', label, children }) {
  if (!children) return <span className={cx('status', 'dot-only', tone)} role="img" aria-label={label} title={label} />;
  return <span className={cx('status', tone)}>{children}</span>;
}
