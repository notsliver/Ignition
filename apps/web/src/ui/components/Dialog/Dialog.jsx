import { useEffect, useRef } from 'react';
import { XMarkIcon } from '@heroicons/react/16/solid';
import { cx } from '../../utils/cx';
import './Dialog.css';

/** Modal dialog built on <dialog>, which provides focus trapping and Escape handling. */
export function Dialog({ open, onClose, title, description, icon: Icon, children, footer, className }) {
  const ref = useRef(null);

  useEffect(() => {
    const dialog = ref.current;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  }, [open]);

  return <dialog ref={ref} className={cx('dialog', className)} aria-labelledby="dialog-title"
    onCancel={event => { event.preventDefault(); onClose(); }}
    onClick={event => { if (event.target === ref.current) onClose(); }}>
    {open && <div className="dialog-body">
      <header className="dialog-header">
        {Icon && <span className="dialog-icon"><Icon /></span>}
        <div><h2 id="dialog-title">{title}</h2>{description && <p>{description}</p>}</div>
        <button type="button" className="dialog-close" aria-label="Close" onClick={onClose}><XMarkIcon /></button>
      </header>
      <div className="dialog-content">{children}</div>
      {footer && <footer className="dialog-footer">{footer}</footer>}
    </div>}
  </dialog>;
}

export function FormField({ label, hint, error, htmlFor, children }) {
  return <div className={cx('form-field', error && 'has-error')}>
    <label htmlFor={htmlFor}>{label}</label>
    {children}
    {error ? <span className="form-error">{error}</span> : hint && <span className="form-hint">{hint}</span>}
  </div>;
}
