import { Button } from '../../ui';
import { useUnsavedChanges } from '../../app/unsaved';
import './settings.css';

export function SettingsGroup({ title, description, children }) {
  return <section className="settings-group">
    <h2>{title}</h2>
    {description && <p>{description}</p>}
    <div className="settings-card">{children}</div>
  </section>;
}

export function SettingsField({ label, hint, htmlFor, children }) {
  return <div className="settings-field">
    <div className="settings-field-label">{htmlFor ? <label htmlFor={htmlFor}><strong>{label}</strong></label> : <strong>{label}</strong>}{hint && <span>{hint}</span>}</div>
    <div className="settings-field-control">{children}</div>
  </div>;
}

export function SaveBar({ dirty, onDiscard, summary }) {
  useUnsavedChanges(dirty);
  return <div className={`save-bar${dirty ? ' visible' : ''}`} aria-hidden={!dirty}>
    <span>{summary ?? 'You have unsaved changes'}</span>
    <Button type="button" onClick={onDiscard} tabIndex={dirty ? 0 : -1}>Discard</Button>
    <Button type="submit" variant="primary" tabIndex={dirty ? 0 : -1}>Save changes</Button>
  </div>;
}
