import { useEffect, useId, useRef, useState } from 'react';
import { ArrowUpTrayIcon } from '@heroicons/react/16/solid';
import { Avatar } from '../Avatar/Avatar';
import { Button } from '../Button/Button';
import './ImageUpload.css';

/** Shared, explicitly saved square image preview for account and team settings. */
export function ImageUpload({ label, name, src, square = false, disabled = false, onUpload, onRemove, onChanged }) {
  const id = useId();
  const input = useRef(null);
  const [file, setFile] = useState(null);
  const [preview, setPreview] = useState('');
  const [saved, setSaved] = useState(src);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState('');
  useEffect(() => setSaved(src), [src]);
  useEffect(() => {
    if (!file) { setPreview(''); return; }
    const url = URL.createObjectURL(file);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [file]);

  function choose(event) {
    const next = event.target.files?.[0];
    event.target.value = '';
    if (!next) return;
    setMessage(''); setError(''); setFile(null);
    if (next.size > 2 * 1024 * 1024) return setError('Choose an image up to 2 MB.');
    if (!['image/png', 'image/jpeg', 'image/webp'].includes(next.type)) return setError('Choose a PNG, JPEG or WebP image.');
    setFile(next);
  }

  async function save(remove = false) {
    setPending(true); setError(''); setMessage('');
    try {
      const result = await (remove ? onRemove() : onUpload(file));
      setSaved(result.url); setFile(null);
      setMessage(remove ? `${label} removed.` : `${label} saved.`);
      await onChanged?.();
    } catch (err) { setError(err.fields?.file || err.message); }
    finally { setPending(false); }
  }

  return <div className={`image-upload${square ? ' image-upload-square' : ''}`} aria-busy={pending}>
    <Avatar name={name} src={preview || saved || null} size={44} />
    <div className="image-upload-body">
      <strong id={`${id}-label`}>{label}</strong>
      <small id={`${id}-hint`}>{file ? 'Preview · cropped to a square when saved.' : 'PNG, JPEG or WebP · up to 2 MB'}</small>
      {!disabled && <div className="image-upload-actions">
        <input ref={input} type="file" hidden accept="image/png,image/jpeg,image/webp" aria-labelledby={`${id}-label`} aria-describedby={`${id}-hint`} disabled={pending} onChange={choose} />
        <Button type="button" icon={ArrowUpTrayIcon} disabled={pending} onClick={() => input.current?.click()}>{file ? 'Choose another' : saved ? 'Replace' : 'Upload'}</Button>
        {file ? <>
          <Button type="button" variant="primary" disabled={pending} onClick={() => save()}>{pending ? 'Saving…' : 'Save image'}</Button>
          <Button type="button" disabled={pending} onClick={() => { setFile(null); setError(''); }}>Cancel</Button>
        </> : saved && <Button type="button" disabled={pending} onClick={() => save(true)}>{pending ? 'Removing…' : 'Remove'}</Button>}
      </div>}
      {error && <span className="image-upload-error" role="alert">{error}</span>}
      <span className="image-upload-status" role="status">{message}</span>
    </div>
  </div>;
}
