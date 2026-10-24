import { cx } from '../../utils/cx';
import './IconButton.css';

export function IconButton({ icon: Icon, label, className, ...props }) {
  return <button className={cx('icon-button', className)} aria-label={label} {...props}><Icon /></button>;
}
