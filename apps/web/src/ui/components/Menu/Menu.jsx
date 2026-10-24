import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { cx } from '../../utils/cx';
import './Menu.css';

/**
 * Floating menu list. `items` is a list of { label, icon, onSelect, danger } or 'separator'.
 * `anchor` is either a DOMRect (aligns to its bottom-right) or a point { x, y } (opens at the cursor).
 * Rendered in a portal with fixed positioning so scroll containers don't clip it.
 */
export function MenuPopover({ anchor, items, label, onClose }) {
  const [position, setPosition] = useState(null);
  const menuRef = useRef(null);

  useLayoutEffect(() => {
    const menu = menuRef.current.getBoundingClientRect();
    const isPoint = !('bottom' in anchor);
    const top = isPoint ? anchor.y : anchor.bottom + 4;
    const left = isPoint ? anchor.x : anchor.right - menu.width;
    setPosition({
      top: top + menu.height <= window.innerHeight - 8 ? top : Math.max(8, (isPoint ? anchor.y : anchor.top - 4) - menu.height),
      left: Math.max(8, Math.min(left, window.innerWidth - menu.width - 8)),
    });
    menuRef.current.querySelector('[role="menuitem"]')?.focus();
  }, [anchor]);

  useEffect(() => {
    const onPointer = event => { if (!menuRef.current?.contains(event.target)) onClose(false); };
    const close = () => onClose(false);
    document.addEventListener('pointerdown', onPointer);
    window.addEventListener('resize', close);
    window.addEventListener('scroll', close, true);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      window.removeEventListener('resize', close);
      window.removeEventListener('scroll', close, true);
    };
  }, [onClose]);

  function onKeyDown(event) {
    const buttons = [...menuRef.current.querySelectorAll('[role="menuitem"]')];
    const index = buttons.indexOf(document.activeElement);
    const moves = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: buttons.length - 1 };
    if (event.key in moves) {
      event.preventDefault();
      buttons[(moves[event.key] + buttons.length) % buttons.length].focus();
    } else if (event.key === 'Escape' || event.key === 'Tab') {
      event.preventDefault();
      onClose(true);
    }
  }

  return createPortal(<div ref={menuRef} className="menu" role="menu" aria-label={label} onKeyDown={onKeyDown} onContextMenu={event => event.preventDefault()} style={position ?? { visibility: 'hidden' }}>
    {items.map((item, i) => item === 'separator'
      ? <div key={i} className="menu-separator" role="separator" />
      : <button key={item.label} role="menuitem" className={cx('menu-item', item.danger && 'danger')} onClick={() => { onClose(true); item.onSelect?.(); }}>{item.icon && <item.icon />}{item.label}</button>)}
  </div>, document.body);
}

/** Button that opens a MenuPopover below itself. */
export function Menu({ trigger: Trigger, label, items }) {
  const [anchor, setAnchor] = useState(null);
  const triggerRef = useRef(null);

  function close(returnFocus) {
    setAnchor(null);
    if (returnFocus) triggerRef.current?.focus();
  }

  return <>
    <button ref={triggerRef} className={cx('menu-trigger', anchor && 'open')} aria-label={label} aria-haspopup="menu" aria-expanded={!!anchor}
      onPointerDown={event => anchor && event.stopPropagation()}
      onClick={() => setAnchor(anchor ? null : triggerRef.current.getBoundingClientRect())}><Trigger /></button>
    {anchor && <MenuPopover anchor={anchor} items={items} label={label} onClose={close} />}
  </>;
}
