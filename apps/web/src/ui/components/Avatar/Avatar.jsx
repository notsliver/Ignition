import { useState } from 'react';
import { cx } from '../../utils/cx';
import './Avatar.css';

// Retained for callers that explicitly request the legacy placeholder.
export const PLACEHOLDER_AVATAR = 'https://upload.wikimedia.org/wikipedia/commons/7/7c/Profile_avatar_placeholder_large.png';

/** Round user avatar. Falls back to the name's first letter if the image fails to load. */
export function Avatar({ name, src, size = 20, className }) {
  const [failedSrc, setFailedSrc] = useState(null);
  return <span className={cx('avatar', className)} style={{ width: size, height: size, fontSize: size * 0.45 }} title={name}>
    {(failedSrc === src) || !src
      ? <span aria-hidden="true">{name?.[0]?.toUpperCase()}</span>
      : <img src={src} alt="" loading="lazy" decoding="async" onError={() => setFailedSrc(src)} />}
    <span className="visually-hidden">{name}</span>
  </span>;
}
