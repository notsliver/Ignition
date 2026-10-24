import { useEffect, useRef } from 'react';
import { CheckIcon, MinusIcon } from '@heroicons/react/16/solid';
import { cx } from '../../utils/cx';
import './Checkbox.css';

export function Checkbox({ checked, indeterminate, onChange, label, className }) {
  const ref = useRef(null);
  useEffect(() => { ref.current.indeterminate = !!indeterminate; }, [indeterminate]);
  return <label className={cx('checkbox', (checked || indeterminate) && 'on', className)} onClick={event => event.stopPropagation()}>
    <input ref={ref} type="checkbox" checked={checked} aria-label={label} onChange={event => onChange(event.target.checked, event.nativeEvent)} />
    {indeterminate ? <MinusIcon /> : checked && <CheckIcon />}
  </label>;
}
