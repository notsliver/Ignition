import { MagnifyingGlassIcon, XMarkIcon } from '@heroicons/react/16/solid';
import { cx } from '../../utils/cx';
import './SearchField.css';

export function SearchField({ value, onChange, placeholder = 'Search', label = placeholder, className }) {
  return <label className={cx('search-field', className)}>
    <MagnifyingGlassIcon />
    <input type="search" value={value} placeholder={placeholder} aria-label={label} onChange={event => onChange(event.target.value)} />
    {value && <button type="button" aria-label="Clear search" onClick={() => onChange('')}><XMarkIcon /></button>}
  </label>;
}
