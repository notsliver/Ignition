import { cx } from '../../utils/cx';
import './Badge.css';

export function Badge({ className, ...props }) {
  return <span className={cx('badge', className)} {...props} />;
}
