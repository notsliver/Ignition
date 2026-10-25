import { useState } from 'react';
import { cx } from '../../utils/cx';
import './Avatar.css';

// Used for every user until real profile pictures come from the API.
export const PLACEHOLDER_AVATAR = 'https://cdn.devme.app/profile-pictures/cbb82c41-9fdc-4372-aae2-2e4f8a597825.webp';

/** Round user avatar. Falls back to the name's first letter if the image fails to load. */
export function Avatar({ name, src = PLACEHOLDER_AVATAR, size = 20, className }) {
  const [failed, setFailed] = useState(false);
  return <span className={cx('avatar', className)} style={{ width: size, height: size, fontSize: size * 0.45 }} title={name}>
    {failed || !src
      ? <span aria-hidden="true">{name?.[0]?.toUpperCase()}</span>
      : <img src={src} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />}
    <span className="visually-hidden">{name}</span>
  </span>;
}
