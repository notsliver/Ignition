// Tracks pages with unsaved changes so navigation can warn before discarding them.
import { useEffect, useId } from 'react';

const dirtyPages = new Set();

export function useUnsavedChanges(dirty) {
  const id = useId();
  useEffect(() => {
    if (dirty) dirtyPages.add(id);
    else dirtyPages.delete(id);
    return () => dirtyPages.delete(id);
  }, [dirty, id]);
}

export function confirmLeave() {
  return dirtyPages.size === 0 || window.confirm('You have unsaved changes. Leave this page and discard them?');
}

if (typeof window !== 'undefined') window.addEventListener('beforeunload', event => {
  if (dirtyPages.size) event.preventDefault();
});
