import { useState } from 'react';
import { ArrowPathIcon, CheckIcon, ClipboardDocumentIcon, EyeIcon, EyeSlashIcon, FolderIcon, PlusIcon, XMarkIcon } from '@heroicons/react/16/solid';
import { BranchIcon, Button, Dialog, FormField, Select, TextInput, cx } from '../../../ui';
import { suggestName, validate } from './flows';
import './AddResourceDialog.css';

const envTone = env => env === 'Production' ? 'var(--success)' : env === 'Staging' ? 'var(--warning)' : 'var(--muted)';
const generatePassword = () => {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789';
  return Array.from(crypto.getRandomValues(new Uint32Array(24)), n => chars[n % chars.length]).join('');
};
const initialValues = flow => Object.fromEntries(flow.fields.map(f => [f.key,
  f.type === 'env' ? [{ key: '', value: '' }] : f.type === 'secret' ? generatePassword() : f.default ?? '']));

function SecretInput({ id, value, onChange, invalid }) {
  const [visible, setVisible] = useState(false);
  const [copied, setCopied] = useState(false);
  function copy() {
    navigator.clipboard?.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  }
  return <TextInput id={id} className="mono secret-input" type={visible ? 'text' : 'password'} value={value} autoComplete="new-password" spellCheck={false} aria-invalid={invalid}
    onChange={event => onChange(event.target.value)}
    suffix={<span className="secret-actions">
      <button type="button" aria-label={visible ? 'Hide password' : 'Show password'} title={visible ? 'Hide' : 'Show'} onClick={() => setVisible(v => !v)}>{visible ? <EyeSlashIcon /> : <EyeIcon />}</button>
      <button type="button" aria-label="Copy password" title="Copy" onClick={copy}>{copied ? <CheckIcon /> : <ClipboardDocumentIcon />}</button>
      <button type="button" aria-label="Generate a new password" title="Regenerate" onClick={() => onChange(generatePassword())}><ArrowPathIcon /></button>
    </span>} />;
}

function EnvEditor({ id, rows, onChange }) {
  const update = (index, patch) => onChange(rows.map((row, i) => i === index ? { ...row, ...patch } : row));
  return <div className="env-editor">
    {rows.map((row, i) => <div key={i} className="env-row">
      <TextInput id={i === 0 ? id : undefined} className="mono env-key" placeholder="NAME" value={row.key} aria-label={`Variable ${i + 1} name`} autoComplete="off" spellCheck={false}
        onChange={event => update(i, { key: event.target.value })}
        onPaste={event => {
          // Paste a whole .env file into a name field to fill several rows at once.
          const text = event.clipboardData.getData('text');
          if (!text.includes('\n') && !text.includes('=')) return;
          event.preventDefault();
          const parsed = text.split(/\r?\n/).map(line => line.trim()).filter(line => line && !line.startsWith('#') && line.includes('='))
            .map(line => { const at = line.indexOf('='); return { key: line.slice(0, at).trim(), value: line.slice(at + 1).trim().replace(/^["']|["']$/g, '') }; });
          if (parsed.length) onChange([...rows.slice(0, i), ...parsed, ...rows.slice(i + 1)].filter((r, n, all) => r.key || r.value || all.length === 1));
        }} />
      <TextInput className="mono env-value" placeholder="Value" value={row.value} aria-label={`Variable ${i + 1} value`} autoComplete="off" spellCheck={false}
        onChange={event => update(i, { value: event.target.value })} />
      <button type="button" className="env-remove" aria-label={`Remove variable ${i + 1}`}
        onClick={() => onChange(rows.length === 1 ? [{ key: '', value: '' }] : rows.filter((_, n) => n !== i))}><XMarkIcon /></button>
    </div>)}
    <div className="env-actions">
      <Button type="button" icon={PlusIcon} onClick={() => onChange([...rows, { key: '', value: '' }])}>Add</Button>
      <span>Tip: paste a .env file into a name field</span>
    </div>
  </div>;
}

export function AddResourceDialog({ flow, projectName, environment, takenNames, onClose, onCreate }) {
  const [values, setValues] = useState(() => initialValues(flow));
  const [errors, setErrors] = useState({});
  const [nameTouched, setNameTouched] = useState(false);
  const [pending, setPending] = useState(false);
  const context = flow.context?.(values) ?? { label: 'Adding to', icon: FolderIcon, text: projectName };
  const ContextIcon = context.icon;
  const firstError = Object.values(errors)[0];

  function set(field, value) {
    setValues(current => {
      const next = { ...current, [field.key]: value };
      // Suggest a name from the repository until the user types their own.
      if (field.kind === 'repo' && !nameTouched && 'name' in next) next.name = suggestName(value);
      return next;
    });
    if (field.kind === 'name') setNameTouched(true);
    if (errors[field.key]) setErrors(({ [field.key]: _, ...rest }) => rest);
  }

  // API field names that differ from this form's keys.
  const serverField = { repository: 'repository', repoUrl: 'repository', image: 'image', dockerfile: 'dockerfile' };

  async function submit(event) {
    event.preventDefault();
    const found = validate(flow, values, takenNames);
    setErrors(found);
    if (Object.keys(found).length) {
      document.getElementById(`field-${Object.keys(found)[0]}`)?.focus();
      return;
    }
    setPending(true);
    try {
      await onCreate(flow.build(values));
    } catch (e) {
      const fields = Object.fromEntries(Object.entries(e.fields ?? {}).map(([k, v]) => [flow.fields.some(f => f.key === k) ? k : serverField[k] ?? k, v]));
      setErrors(Object.keys(fields).length ? fields : { form: e.message });
    } finally {
      setPending(false);
    }
  }

  return <Dialog open onClose={onClose} title={flow.title} description={flow.description} className="add-resource">
    <form className="add-panel" onSubmit={submit} noValidate>
      <div className="add-context">
        <span className="add-context-label">{context.label}</span>
        <span className={cx('add-context-value', context.placeholder && 'placeholder')}>
          {ContextIcon && <ContextIcon className="add-context-icon" />}
          <span className="add-context-text">{context.text}</span>
          {context.branch && <span className="add-context-branch"><BranchIcon />{context.branch}</span>}
        </span>
      </div>

      <div className="add-card">
        {flow.fields.map(field => {
          const id = `field-${field.key}`;
          const value = values[field.key];
          let control;
          if (field.type === 'select') control = <Select id={id} value={value} label={field.label} options={field.options} onChange={v => set(field, v)} />;
          else if (field.type === 'secret') control = <SecretInput id={id} value={value} invalid={!!errors[field.key]} onChange={v => set(field, v)} />;
          else if (field.type === 'env') control = <EnvEditor id={id} rows={value} onChange={rows => set(field, rows)} />;
          else if (field.type === 'file') control = <input id={id} type="file" className="file-input" accept={field.accept} onChange={event => set(field, event.target.files[0]?.name ?? '')} />;
          else control = <TextInput id={id} className={cx(field.mono && 'mono')} value={value} type={field.type === 'password' ? 'password' : field.type === 'number' ? 'number' : 'text'} prefix={field.prefix} placeholder={field.placeholder}
            autoComplete="off" spellCheck={false} aria-invalid={!!errors[field.key]} onChange={event => set(field, event.target.value)} />;
          return <FormField key={field.key} label={field.label} hint={errors[field.key] ? undefined : field.hint} error={errors[field.key] ? ' ' : undefined} htmlFor={id}>{control}</FormField>;
        })}
      </div>

      <div className="add-footer">
        <span className="add-target"><span className="add-target-dot" style={{ background: envTone(environment) }} /><strong>{projectName}</strong><span className="sep">/</span>{environment}</span>
        {firstError && <span className="add-error" role="alert">{firstError}</span>}
        <Button type="submit" variant="primary" disabled={pending}>{pending ? 'Creating…' : flow.submit}</Button>
      </div>
    </form>
  </Dialog>;
}
