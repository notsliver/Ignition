import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Squares2X2Icon, PuzzlePieceIcon, Cog6ToothIcon, ChevronDownIcon, ViewColumnsIcon, Bars3Icon, BoltIcon, SunIcon, MoonIcon } from '@heroicons/react/20/solid';
import { ChevronDownIcon as CaretIcon } from '@heroicons/react/16/solid';
import './style.css';

const topItems = [['Dashboard', Squares2X2Icon], ['Integrations', PuzzlePieceIcon], ['Settings', Cog6ToothIcon]];
const groups = [
  { label: 'Projects', items: ['Applications', 'Databases', 'Services', 'Scheduled jobs', 'Deployments'] },
  { label: 'Infrastructure', items: ['Servers', 'Sources', 'Domains', 'Logs & monitoring'] },
  { label: 'Discover', items: ['Templates', 'Changelog', 'Documentation'] },
];

function App() {
  const [active, setActive] = useState('Dashboard');
  const [collapsed, setCollapsed] = useState({});
  const [hidden, setHidden] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [theme, setTheme] = useState(() => {
    try { const saved = localStorage.getItem('theme'); if (saved) return saved; } catch {}
    return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  });
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try { localStorage.setItem('theme', theme); } catch {}
  }, [theme]);
  const go = title => { setActive(title); setMobileOpen(false); };

  return <div className={`app${hidden ? ' sidebar-hidden' : ''}${mobileOpen ? ' mobile-open' : ''}`}>
    {mobileOpen && <button className="backdrop" aria-label="Close navigation" onClick={() => setMobileOpen(false)} />}
    <aside className="sidebar" aria-label="Main navigation">
      <div className="brand">
        <button className="workspace"><span className="brand-mark"><BoltIcon /></span><span>Ignition</span><ChevronDownIcon className="workspace-caret" /></button>
        <button className="icon-button" aria-label="Hide sidebar" onClick={() => setHidden(true)}><ViewColumnsIcon /></button>
      </div>
      <nav>
        {topItems.map(([title, Icon]) => <button key={title} className={`top-item${active === title ? ' active' : ''}`} aria-current={active === title ? 'page' : undefined} onClick={() => go(title)}><Icon />{title}</button>)}
        {groups.map(({ label, items }) => {
          const open = !collapsed[label];
          return <section key={label} className="group">
            <button className="group-label" aria-expanded={open} onClick={() => setCollapsed(value => ({ ...value, [label]: open }))}><span>{label}</span><CaretIcon className={`group-caret${open ? '' : ' closed'}`} /></button>
            {open && <div className="group-items">{items.map(title => <button key={title} className={`sub-item${active === title ? ' active' : ''}`} aria-current={active === title ? 'page' : undefined} onClick={() => go(title)}>{title}</button>)}</div>}
          </section>;
        })}
      </nav>
      <div className="theme-toggle" role="group" aria-label="Theme">
        {[['light', SunIcon, 'Light'], ['dark', MoonIcon, 'Dark']].map(([value, Icon, label]) => <button key={value} className={theme === value ? 'active' : ''} aria-pressed={theme === value} onClick={() => setTheme(value)}><Icon />{label}</button>)}
      </div>
    </aside>
    <main className="container">
      <header className="page-header">
        <button className={`icon-button show-sidebar${hidden ? ' visible' : ''}`} aria-label="Show navigation" onClick={() => { setHidden(false); setMobileOpen(true); }}>{hidden ? <ViewColumnsIcon /> : <Bars3Icon />}</button>
        <h1>{active}</h1><span className="badge">Preview</span>
      </header>
      <div className="coming-soon"><p className="soon-title">Coming soon</p><p>{active} is on its way.</p></div>
    </main>
  </div>;
}

createRoot(document.getElementById('root')).render(<App />);
