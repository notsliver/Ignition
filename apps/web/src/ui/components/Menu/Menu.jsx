import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { cx } from '../../utils/cx';
import './Menu.css';

/**
 * Dropdown menu. `items` is a list of { label, icon, onSelect, danger } or 'separator'.
 * Rendered in a portal with fixed positioning so scroll containers don't clip it.
 */
export function Menu({ trigger: Trigger, label, items }) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState(null);
  const triggerRef = useRef(null);
  const menuRef = useRef(null);

  useLayoutEffect(() => {
    if (!open) return;
    const rect = triggerRef.current.getBoundingClientRect();
    const menu = menuRef.current.getBoundingClientRect();
    const below = rect.bottom + 4 + menu.height <= window.innerHeight;
    setPosition({ top: below ? rect.bottom + 4 : rect.top - 4 - menu.height, left: Math.max(8, rect.right - menu.width) });
    menuRef.current.querySelector('[role="menuitem"]')?.focus();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onPointer = event => { if (!menuRef.current?.contains(event.target) && !triggerRef.current.contains(event.target)) close(); };
    document.addEventListener('pointerdown', onPointer);
    window.addEventListener('resize', close);
    window.addEventListener('scroll', close, true);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      window.removeEventListener('resize', close);
      window.removeEventListener('scroll', close, true);
    };
  }, [open]);

  function onKeyDown(event) {
    const buttons = [...menuRef.current.querySelectorAll('[role="menuitem"]')];
    const index = buttons.indexOf(document.activeElement);
    const moves = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: buttons.length - 1 };
    if (event.key in moves) {
      event.preventDefault();
      buttons[(moves[event.key] + buttons.length) % buttons.length].focus();
    } else if (event.key === 'Escape' || event.key === 'Tab') {
      event.preventDefault();
      setOpen(false);
      triggerRef.current.focus();
    }
  }

  function select(item) {
    setOpen(false);
    triggerRef.current.focus();
    item.onSelect?.();
  }

  return <>
    <button ref={triggerRef} className={cx('menu-trigger', open && 'open')} aria-label={label} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(value => !value)}><Trigger /></button>
    {open && createPortal(<div ref={menuRef} className="menu" role="menu" aria-label={label} onKeyDown={onKeyDown} style={position ?? { visibility: 'hidden' }}>
      {items.map((item, i) => item === 'separator'
        ? <div key={i} className="menu-separator" role="separator" />
        : <button key={item.label} role="menuitem" className={cx('menu-item', item.danger && 'danger')} onClick={() => select(item)}>{item.icon && <item.icon />}{item.label}</button>)}
    </div>, document.body)}
  </>;
}
