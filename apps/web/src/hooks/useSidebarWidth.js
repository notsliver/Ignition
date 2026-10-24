import { useCallback, useEffect, useRef, useState } from 'react';

export const SIDEBAR_MIN = 200;
export const SIDEBAR_MAX = 360;
export const SIDEBAR_DEFAULT = 232;
const clamp = value => Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, Math.round(value)));

function initialWidth() {
  try { const saved = Number(localStorage.getItem('sidebar-width')); if (saved) return clamp(saved); } catch {}
  return SIDEBAR_DEFAULT;
}

/** Sidebar width with drag-to-resize. Width is written to a CSS variable on `target` while dragging, so React only re-renders at the end. */
export function useSidebarWidth(targetRef) {
  const [width, setWidth] = useState(initialWidth);
  const [resizing, setResizing] = useState(false);
  const live = useRef(width);
  const frame = useRef(0);

  useEffect(() => {
    live.current = width;
    targetRef.current?.style.setProperty('--sidebar-width', `${width}px`);
    try { localStorage.setItem('sidebar-width', String(width)); } catch {}
  }, [width, targetRef]);

  const startResize = useCallback(event => {
    if (event.button !== 0) return;
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = live.current;
    setResizing(true);

    const onMove = moveEvent => {
      cancelAnimationFrame(frame.current);
      frame.current = requestAnimationFrame(() => {
        live.current = clamp(startWidth + moveEvent.clientX - startX);
        targetRef.current?.style.setProperty('--sidebar-width', `${live.current}px`);
      });
    };
    const onUp = () => {
      cancelAnimationFrame(frame.current);
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      setResizing(false);
      setWidth(live.current);
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
  }, [targetRef]);

  const onKeyDown = useCallback(event => {
    const step = event.shiftKey ? 32 : 8;
    const next = { ArrowLeft: width - step, ArrowRight: width + step, Home: SIDEBAR_MIN, End: SIDEBAR_MAX }[event.key];
    if (next === undefined) return;
    event.preventDefault();
    setWidth(clamp(next));
  }, [width]);

  const reset = useCallback(() => setWidth(SIDEBAR_DEFAULT), []);

  return { width, resizing, startResize, onKeyDown, reset };
}
