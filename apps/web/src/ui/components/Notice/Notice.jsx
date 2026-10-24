import { CheckCircleIcon, ExclamationTriangleIcon, InformationCircleIcon, ShieldExclamationIcon, XMarkIcon } from '@heroicons/react/16/solid';
import { cx } from '../../utils/cx';
import './Notice.css';

const icons = { info: InformationCircleIcon, success: CheckCircleIcon, warning: ExclamationTriangleIcon, danger: ShieldExclamationIcon };

/** tone: 'info' | 'success' | 'warning' | 'danger' */
export function Notice({ tone = 'info', icon, title, children, action, onAction, onDismiss }) {
  const Icon = icon ?? icons[tone];
  return <div className={cx('notice', tone)} role={tone === 'danger' || tone === 'warning' ? 'alert' : 'status'}>
    <Icon className="notice-icon" />
    <div className="notice-copy">
      <strong>{title}</strong>
      {children && <span>{children}</span>}
    </div>
    {action && <button className="notice-action" onClick={onAction}>{action}</button>}
    {onDismiss && <button className="notice-dismiss" aria-label="Dismiss" onClick={onDismiss}><XMarkIcon /></button>}
  </div>;
}
