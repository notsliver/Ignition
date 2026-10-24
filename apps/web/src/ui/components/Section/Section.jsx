import { ArrowRightIcon } from '@heroicons/react/16/solid';
import { cx } from '../../utils/cx';
import './Section.css';

export function Section({ title, count, action, onAction, className, children }) {
  return <section className={cx('section', className)}>
    <header className="section-header">
      <h2 className="section-title">{title}</h2>
      {count != null && <span className="section-count">{count}</span>}
      {action && <button className="section-action" onClick={onAction}>{action}<ArrowRightIcon /></button>}
    </header>
    {children}
  </section>;
}
