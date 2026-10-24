import { cx } from '../../utils/cx';
import './Button.css';

/** variant: 'default' | 'primary' */
export function Button({ icon: Icon, variant = 'default', className, children, ...props }) {
  return <button className={cx('button', variant !== 'default' && variant, className)} {...props}>{Icon && <Icon />}{children}</button>;
}
