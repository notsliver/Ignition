import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { CheckIcon, ChevronUpDownIcon, MagnifyingGlassIcon } from '@heroicons/react/16/solid';
import { cx } from '../../utils/cx';
import './Select.css';

/**
 * Custom listbox select. options: [{ value, label, description?, dot? }]; onChange receives the value.
 * `searchable` shows a filter input (true, or a number: only when there are more options than that).
 * `action` adds a footer item: { label, icon, onSelect }.
 */
export function Select({ id, options, value, onChange, label, className, searchable = false, action }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [position, setPosition] = useState(null);
  const triggerRef = useRef(null);
  const popoverRef = useRef(null);
  const selected = options.find(o => o.value === value);
  const showSearch = searchable === true || (typeof searchable === 'number' && options.length > searchable);
  const q = query.trim().toLowerCase();
  const visible = q ? options.filter(o => o.label.toLowerCase().includes(q)) : options;

  useLayoutEffect(() => {
    if (!open) return;
    const rect = triggerRef.current.getBoundingClientRect();
    const popover = popoverRef.current.getBoundingClientRect();
    const below = rect.bottom + 4 + popover.height <= window.innerHeight;
    setPosition({ top: below ? rect.bottom + 4 : rect.top - 4 - popover.height, left: rect.left, minWidth: rect.width });
    const target = showSearch
      ? popoverRef.current.querySelector('input')
      : popoverRef.current.querySelector('[aria-selected="true"]') ?? popoverRef.current.querySelector('[role="option"]');
    target?.focus();
  }, [open, showSearch]);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onPointer = event => { if (!popoverRef.current?.contains(event.target) && !triggerRef.current.contains(event.target)) close(); };
    const onScroll = event => { if (!popoverRef.current?.contains(event.target)) close(); };
    document.addEventListener('pointerdown', onPointer);
    window.addEventListener('resize', close);
    window.addEventListener('scroll', onScroll, true);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      window.removeEventListener('resize', close);
      window.removeEventListener('scroll', onScroll, true);
    };
  }, [open]);

  useEffect(() => { if (!open) setQuery(''); }, [open]);

  function close() {
    setOpen(false);
    triggerRef.current.focus();
  }

  function choose(option) {
    onChange(option.value);
    close();
  }

  function onKeyDown(event) {
    const items = [...popoverRef.current.querySelectorAll('[role="option"], .select-action')];
    const index = items.indexOf(document.activeElement);
    const moves = { ArrowDown: index + 1, ArrowUp: index - 1 };
    if (event.key in moves && items.length) {
      event.preventDefault();
      if (event.key === 'ArrowUp' && index <= 0 && showSearch) popoverRef.current.querySelector('input').focus();
      else items[(moves[event.key] + items.length) % items.length].focus();
    } else if (event.key === 'Enter' && event.target.tagName === 'INPUT' && visible.length) {
      event.preventDefault();
      choose(visible[0]);
    } else if (event.key === 'Escape' || event.key === 'Tab') {
      event.preventDefault();
      close();
    }
  }

  return <>
    <button ref={triggerRef} id={id} type="button" className={cx('input', 'select-trigger', open && 'open', className)} aria-haspopup="listbox" aria-expanded={open} aria-label={label}
      onClick={() => setOpen(v => !v)}
      onKeyDown={event => { if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); setOpen(true); } }}>
      <span className="select-value">{selected?.dot && <span className="select-dot" style={{ background: selected.dot }} />}<span>{selected?.label}</span></span><ChevronUpDownIcon />
    </button>
    {open && createPortal(<div ref={popoverRef} className="select-popover" onKeyDown={onKeyDown} style={position ?? { visibility: 'hidden' }}>
      {showSearch && <label className="select-search"><MagnifyingGlassIcon /><input value={query} onChange={event => setQuery(event.target.value)} placeholder="Search…" aria-label={`Search ${label ?? 'options'}`} /></label>}
      <div className="select-list" role="listbox" aria-label={label}>
        {visible.map(option => <button key={option.value} type="button" role="option" aria-selected={option.value === value} className="select-option" onClick={() => choose(option)}>
          {option.dot && <span className="select-dot" style={{ background: option.dot }} />}
          <span className="select-option-copy"><span>{option.label}</span>{option.description && <small>{option.description}</small>}</span>
          {option.value === value && <CheckIcon />}
        </button>)}
        {!visible.length && <p className="select-empty">No matches</p>}
      </div>
      {action && <button type="button" className="select-action" onClick={() => { close(); action.onSelect?.(); }}>{action.icon && <action.icon />}{action.label}</button>}
    </div>, triggerRef.current?.closest('dialog') ?? document.body)}
  </>;
}
