import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ChevronRightIcon } from '@heroicons/react/16/solid';
import { cx } from '../../utils/cx';
import './Menu.css';

/**
 * Floating menu list. `items` is a list of { label, icon, color, onSelect, danger, items } or 'separator'.
 * An item with `items` opens a submenu to the side.
 * `anchor` is a DOMRect or a point { x, y } (opens at the cursor). `align` is 'start' or 'end' for DOMRect anchors.
 * Rendered in a portal with fixed positioning so scroll containers don't clip it.
 */
export function MenuPopover({ anchor, items, label, onClose, align = 'end', side = 'below', submenu = false, onDone = onClose, onBack }) {
  const [position, setPosition] = useState(null);
  const [open, setOpen] = useState(null);
  const menuRef = useRef(null);
  const hoverTimer = useRef();

  useLayoutEffect(() => {
    const menu = menuRef.current.getBoundingClientRect();
    let top, left;
    if (!('bottom' in anchor)) {
      [top, left] = [anchor.y, anchor.x];
    } else if (side === 'right') {
      top = anchor.top - 5;
      left = anchor.right + 8 + menu.width <= window.innerWidth ? anchor.right + 8 : anchor.left - 8 - menu.width;
    } else {
      top = anchor.bottom + 6 + menu.height <= window.innerHeight - 8 ? anchor.bottom + 6 : anchor.top - 6 - menu.height;
      left = align === 'start' ? anchor.left : anchor.right - menu.width;
    }
    setPosition({
      top: Math.max(8, Math.min(top, window.innerHeight - menu.height - 8)),
      left: Math.max(8, Math.min(left, window.innerWidth - menu.width - 8)),
    });
    menuRef.current.querySelector('[role="menuitem"]')?.focus({ preventScroll: true });
  }, [anchor, align, side]);

  useEffect(() => {
    if (submenu) return;
    const onPointer = event => { if (!event.target.closest?.('.menu')) onClose(false); };
    const close = () => onClose(false);
    document.addEventListener('pointerdown', onPointer);
    window.addEventListener('resize', close);
    window.addEventListener('scroll', close, true);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      window.removeEventListener('resize', close);
      window.removeEventListener('scroll', close, true);
    };
  }, [onClose, submenu]);

  useEffect(() => () => clearTimeout(hoverTimer.current), []);

  function openSubmenu(index, element, focus) {
    clearTimeout(hoverTimer.current);
    setOpen({ index, anchor: element.getBoundingClientRect(), focus });
  }

  function onKeyDown(event) {
    if (event.target.closest('.menu') !== menuRef.current) return;
    const buttons = [...menuRef.current.querySelectorAll(':scope > [role="menuitem"]')];
    const index = buttons.indexOf(document.activeElement);
    const moves = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: buttons.length - 1 };
    if (event.key in moves) {
      event.preventDefault();
      buttons[(moves[event.key] + buttons.length) % buttons.length].focus();
    } else if (event.key === 'ArrowRight' && document.activeElement?.dataset.submenu) {
      event.preventDefault();
      openSubmenu(Number(document.activeElement.dataset.index), document.activeElement, true);
    } else if ((event.key === 'ArrowLeft' || event.key === 'Escape') && submenu) {
      event.preventDefault();
      onBack();
    } else if (event.key === 'Escape' || event.key === 'Tab') {
      event.preventDefault();
      onClose(true);
    }
  }

  return createPortal(<div ref={menuRef} className={cx('menu', submenu && 'submenu')} role="menu" aria-label={label} onKeyDown={onKeyDown} onContextMenu={event => event.preventDefault()} style={position ?? { visibility: 'hidden' }}>
    {items.map((item, i) => {
      if (item === 'separator') return <div key={i} className="menu-separator" role="separator" />;
      const Icon = item.icon;
      const icon = Icon && <Icon style={item.color ? { color: item.color } : undefined} />;
      if (item.items) return <button key={item.label} role="menuitem" aria-haspopup="menu" aria-expanded={open?.index === i} data-submenu="true" data-index={i}
        className={cx('menu-item', open?.index === i && 'expanded')}
        onClick={event => openSubmenu(i, event.currentTarget, true)}
        onPointerEnter={event => { const el = event.currentTarget; clearTimeout(hoverTimer.current); hoverTimer.current = setTimeout(() => openSubmenu(i, el, false), 80); }}>
        {icon}{item.label}<ChevronRightIcon className="menu-chevron" />
      </button>;
      return <button key={item.label} role="menuitem" className={cx('menu-item', item.danger && 'danger')}
        onPointerEnter={() => { clearTimeout(hoverTimer.current); hoverTimer.current = setTimeout(() => setOpen(null), 120); }}
        onClick={() => { onDone(true); item.onSelect?.(); }}>{icon}{item.label}</button>;
    })}
    {open && <MenuPopover key={open.index} submenu side="right" anchor={open.anchor} items={items[open.index].items} label={items[open.index].label} onClose={onClose} onDone={onDone}
      onBack={() => { setOpen(null); menuRef.current.querySelector(`[data-index="${open.index}"]`)?.focus(); }} />}
  </div>, document.body);
}

/** Button that opens a MenuPopover. Pass an icon component as `trigger`, or `children` + `className` for a custom button. */
export function Menu({ trigger: Trigger, label, items, children, className, align = 'end' }) {
  const [anchor, setAnchor] = useState(null);
  const triggerRef = useRef(null);

  function close(returnFocus) {
    setAnchor(null);
    if (returnFocus) triggerRef.current?.focus();
  }

  return <>
    <button ref={triggerRef} type="button" className={cx(children ? className : 'menu-trigger', anchor && 'open')} aria-label={children ? undefined : label} aria-haspopup="menu" aria-expanded={!!anchor}
      onPointerDown={event => anchor && event.stopPropagation()}
      onClick={() => setAnchor(anchor ? null : triggerRef.current.getBoundingClientRect())}>{children ?? <Trigger />}</button>
    {anchor && <MenuPopover anchor={anchor} align={align} items={items} label={label} onClose={close} />}
  </>;
}
