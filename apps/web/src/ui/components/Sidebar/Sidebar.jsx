import { useState } from 'react';
import { ChevronDownIcon } from '@heroicons/react/16/solid';
import { cx } from '../../utils/cx';
import './Sidebar.css';

export function Sidebar({ className, label = 'Main navigation', ...props }) {
  return <aside className={cx('sidebar', className)} aria-label={label} {...props} />;
}

export function SidebarHeader(props) {
  return <div className="sidebar-header" {...props} />;
}

export function SidebarNav(props) {
  return <nav className="sidebar-nav" {...props} />;
}

export function SidebarFooter(props) {
  return <div className="sidebar-footer" {...props} />;
}

export function SidebarSeparator() {
  return <div className="sidebar-separator" role="separator" />;
}

export function SidebarItem({ icon: Icon, active, children, ...props }) {
  return <button className={cx('sidebar-item', active && 'active')} aria-current={active ? 'page' : undefined} {...props}>{Icon && <Icon />}{children}</button>;
}

export function SidebarGroup({ label, defaultOpen = true, children }) {
  const [open, setOpen] = useState(defaultOpen);
  return <section className="sidebar-group">
    <button className="sidebar-group-label" aria-expanded={open} onClick={() => setOpen(value => !value)}><span>{label}</span><ChevronDownIcon className={cx('sidebar-group-caret', !open && 'closed')} /></button>
    {open && <div className="sidebar-group-items">{children}</div>}
  </section>;
}

export function SidebarSubItem({ active, ...props }) {
  return <button className={cx('sidebar-subitem', active && 'active')} aria-current={active ? 'page' : undefined} {...props} />;
}
