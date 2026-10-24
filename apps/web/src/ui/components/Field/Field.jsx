import { cx } from '../../utils/cx';
import './Field.css';

export function TextInput({ className, prefix, suffix, ...props }) {
  return <span className={cx('input', className)}>
    {prefix && <span className="input-affix">{prefix}</span>}
    <input {...props} />
    {suffix && <span className="input-affix">{suffix}</span>}
  </span>;
}

export function Switch({ checked, onChange, label, disabled }) {
  return <button type="button" role="switch" disabled={disabled} aria-checked={checked} aria-label={label} className={cx('switch', checked && 'on')} onClick={() => onChange(!checked)}><span /></button>;
}
