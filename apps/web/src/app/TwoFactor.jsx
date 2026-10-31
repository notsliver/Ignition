import { useEffect, useMemo, useState } from 'react';
import qrcode from 'qrcode-generator';
import { ArrowDownTrayIcon, CheckIcon, ClipboardDocumentIcon } from '@heroicons/react/16/solid';
import { Button, FormField, Notice, TextInput } from '../ui';
import './TwoFactor.css';

/*
 * Two-factor building blocks shared by sign-in (enforced enrollment) and the account dialog:
 * a QR code for the otpauth link, the enrollment form, and the one-time recovery codes.
 */

export function QRCode({ value, label }) {
  const svg = useMemo(() => {
    const qr = qrcode(0, 'M');
    qr.addData(value);
    qr.make();
    return qr.createSvgTag({ cellSize: 4, margin: 2, scalable: true });
  }, [value]);
  return <div className="tf-qr" role="img" aria-label={label} dangerouslySetInnerHTML={{ __html: svg }} />;
}

/** Shows the secret and asks for the first code. start() returns { secret, url }; confirm(code) finishes. */
export function EnrollTwoFactor({ start, confirm, submitLabel = 'Turn on' }) {
  const [setup, setSetup] = useState(null);
  const [error, setError] = useState('');
  const [code, setCode] = useState('');
  const [fieldError, setFieldError] = useState('');
  const [pending, setPending] = useState(false);
  const [copied, setCopied] = useState(false);
  useEffect(() => { start().then(setSetup).catch(e => setError(e.message)); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  async function submit(e) {
    e.preventDefault();
    setPending(true);
    setFieldError('');
    try { await confirm(code); } catch (err) { setFieldError(err.fields?.code ?? err.message); setPending(false); }
  }
  if (error) return <Notice tone="danger" title="Couldn't start setup">{error}</Notice>;
  if (!setup) return <p className="tf-muted">Preparing…</p>;
  const grouped = setup.secret.match(/.{1,4}/g).join(' ');
  return <form className="tf-enroll" onSubmit={submit}>
    <ol className="tf-steps">
      <li>Open an authenticator app (1Password, Google Authenticator, Authy, Bitwarden…) and scan this code.</li>
    </ol>
    <div className="tf-scan">
      <QRCode value={setup.url} label="QR code for your authenticator app" />
      <div className="tf-manual">
        <span>Can't scan? Enter this key:</span>
        <code>{grouped}</code>
        <button type="button" className="tf-copy" onClick={() => { navigator.clipboard?.writeText(setup.secret); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>
          {copied ? <CheckIcon /> : <ClipboardDocumentIcon />}{copied ? 'Copied' : 'Copy key'}
        </button>
        <small>Time-based, 6 digits, every 30 seconds.</small>
      </div>
    </div>
    <ol className="tf-steps" start={2}><li>Enter the 6-digit code the app shows.</li></ol>
    <FormField label="Code" htmlFor="tf-code" error={fieldError}>
      <TextInput id="tf-code" className="mono tf-code-input" inputMode="numeric" autoComplete="one-time-code" maxLength={7} autoFocus placeholder="123 456"
        value={code} onChange={e => { setCode(e.target.value.replace(/[^\d ]/g, '')); setFieldError(''); }} />
    </FormField>
    <Button type="submit" variant="primary" disabled={pending || code.replace(/\s/g, '').length !== 6}>{pending ? 'Checking…' : submitLabel}</Button>
  </form>;
}

export function RecoveryCodes({ codes, instance = 'Ignition', account = '' }) {
  const [copied, setCopied] = useState(false);
  const text = `${instance} recovery codes${account ? ` for ${account}` : ''}\nEach code works once.\n\n${codes.join('\n')}\n`;
  function download() {
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
    const a = Object.assign(document.createElement('a'), { href: url, download: 'ignition-recovery-codes.txt' });
    a.click();
    URL.revokeObjectURL(url);
  }
  return <div className="tf-recovery">
    <Notice tone="warning" title="Save these recovery codes now">If you lose your phone, each one signs you in once. They won't be shown again.</Notice>
    <ul className="tf-codes">{codes.map(c => <li key={c}><code>{c}</code></li>)}</ul>
    <div className="tf-recovery-actions">
      <Button icon={copied ? CheckIcon : ClipboardDocumentIcon} onClick={() => { navigator.clipboard?.writeText(codes.join('\n')); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>{copied ? 'Copied' : 'Copy'}</Button>
      <Button icon={ArrowDownTrayIcon} onClick={download}>Download</Button>
    </div>
  </div>;
}
