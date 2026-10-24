import { cx } from '../../utils/cx';
import './SegmentedControl.css';

export function SegmentedControl({ options, value, onChange, label, className }) {
  return <div className={cx('segmented', className)} role="group" aria-label={label}>
    {options.map(option => {
      const Icon = option.icon;
      const selected = value === option.value;
      return <button key={option.value} className={cx(selected && 'active')} aria-pressed={selected} title={option.iconOnly ? option.label : undefined} onClick={() => onChange(option.value)}>{Icon && <Icon />}{option.iconOnly ? <span className="visually-hidden">{option.label}</span> : option.label}</button>;
    })}
  </div>;
}
