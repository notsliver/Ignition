import { useEffect, useRef, useState } from 'react';
import { ArrowPathIcon, ArrowsPointingInIcon, ArrowsPointingOutIcon, CommandLineIcon, MinusIcon, PlusIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';
import { Button, Switch, cx } from '../../../ui';
import { useSession } from '../../../app/AuthGate';
import { PageIntro } from './shared';
import './Terminal.css';

/*
 * A real shell in the resource's running container: xterm.js on a WebSocket to a docker exec with a TTY.
 * Binary frames are terminal bytes; text frames are JSON control messages (resize / ready / exit).
 */

const THEME = {
  background: '#141517', foreground: '#e6e6e8', cursor: '#9fb4ff', cursorAccent: '#141517', selectionBackground: '#3c55cf66',
  black: '#1d1f22', red: '#f87171', green: '#4ade80', yellow: '#facc15', blue: '#8fa2ff', magenta: '#d8a4ff', cyan: '#67e8f9', white: '#d4d4d8',
  brightBlack: '#6b7280', brightRed: '#fca5a5', brightGreen: '#86efac', brightYellow: '#fde68a', brightBlue: '#b4c2ff', brightMagenta: '#e9c6ff', brightCyan: '#a5f3fc', brightWhite: '#ffffff',
};
const FONT_SIZES = [11, 12, 13, 14, 16, 18];

export function Terminal({ resource, onNavigate }) {
  const { user } = useSession();
  const isAdmin = user.role === 'admin' || user.role === 'owner';
  const canUse = user.role !== 'viewer';
  const running = resource.status === 'running';
  const hostRef = useRef(null);
  const termRef = useRef(null);
  const fitRef = useRef(null);
  const wsRef = useRef(null);
  const [state, setState] = useState({ phase: 'idle' }); // idle, connecting, open, closed
  const [root, setRoot] = useState(false);
  const [session, setSession] = useState(0); // bump to reconnect
  const [fontSize, setFontSize] = useState(() => { try { return Number(localStorage.getItem('ignition.terminal.font')) || 13; } catch { return 13; } });
  const [full, setFull] = useState(false);

  // One xterm instance per page visit.
  useEffect(() => {
    const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
    const term = new XTerm({
      theme: THEME, fontSize, cursorBlink: !reduced, scrollback: 5000, allowProposedApi: false,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon((_, uri) => window.open(uri, '_blank', 'noopener')));
    term.open(hostRef.current);
    // Ctrl+Shift+C copies the selection (Ctrl+C stays an interrupt for the shell).
    term.attachCustomKeyEventHandler(e => {
      if (e.type === 'keydown' && e.ctrlKey && e.shiftKey && e.code === 'KeyC' && term.hasSelection()) {
        navigator.clipboard?.writeText(term.getSelection());
        return false;
      }
      return true;
    });
    termRef.current = term;
    fitRef.current = fit;
    requestAnimationFrame(() => fit.fit());
    const ro = new ResizeObserver(() => { try { fit.fit(); } catch { /* not visible */ } });
    ro.observe(hostRef.current);
    return () => { ro.disconnect(); term.dispose(); };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const term = termRef.current;
    if (!term) return;
    term.options.fontSize = fontSize;
    try { localStorage.setItem('ignition.terminal.font', String(fontSize)); } catch { /* private mode */ }
    requestAnimationFrame(() => fitRef.current?.fit());
  }, [fontSize]);

  // (Re)connect whenever a new session is requested.
  useEffect(() => {
    const term = termRef.current;
    if (!term || !running || !canUse) return;
    term.reset();
    fitRef.current.fit();
    setState({ phase: 'connecting' });
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const params = new URLSearchParams({ cols: term.cols, rows: term.rows, ...(root ? { root: '1' } : {}) });
    const ws = new WebSocket(`${proto}://${location.host}/api/resources/${resource.id}/terminal?${params}`);
    ws.binaryType = 'arraybuffer';
    wsRef.current = ws;
    let ready = false;
    const encoder = new TextEncoder();
    const send = data => ws.readyState === WebSocket.OPEN && ws.send(encoder.encode(data));
    const onData = term.onData(send);
    const onBinary = term.onBinary(data => ws.readyState === WebSocket.OPEN && ws.send(Uint8Array.from(data, c => c.charCodeAt(0))));
    const onResize = term.onResize(({ cols, rows }) => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify({ type: 'resize', cols, rows })));
    ws.onmessage = e => {
      if (typeof e.data !== 'string') { term.write(new Uint8Array(e.data)); return; }
      const msg = JSON.parse(e.data);
      if (msg.type === 'ready') {
        ready = true;
        setState({ phase: 'open', root: msg.root, container: msg.container });
        term.focus();
      } else if (msg.type === 'exit') {
        setState({ phase: 'closed', reason: msg.reason });
      }
    };
    ws.onclose = () => {
      setState(s => s.phase === 'closed' ? s : {
        phase: 'closed',
        reason: ready ? 'The connection was lost.' : 'Couldn\'t open a terminal. The resource may have stopped, it may already have 4 open terminals, or the Ignition API restarted.',
      });
    };
    return () => { onData.dispose(); onBinary.dispose(); onResize.dispose(); ws.onclose = null; ws.close(); };
  }, [session, resource.id, running, canUse]); // eslint-disable-line react-hooks/exhaustive-deps

  const reconnect = () => setSession(n => n + 1);
  const status = !running ? { cls: 'off', text: `${resource.name} isn't running` }
    : state.phase === 'open' ? { cls: 'on', text: `Connected${state.root ? ' as root' : ''} · container ${state.container}` }
      : state.phase === 'connecting' ? { cls: 'wait', text: 'Connecting…' } : { cls: 'off', text: 'Disconnected' };

  return <div className={cx('rp-page', 'terminal-page', full && 'full')}>
    <PageIntro actions={<>
      {isAdmin && <label className="term-root"><Switch label="Open as root" checked={root} disabled={!running} onChange={v => { setRoot(v); setSession(n => n + 1); }} />Root</label>}
      <Button icon={ArrowPathIcon} disabled={!running || !canUse} onClick={reconnect}>New session</Button>
    </>}>
      A shell inside {resource.name}'s running container: bash if the image has it, otherwise sh. It runs as the image's user{isAdmin ? ' unless you switch to root' : ''}.
    </PageIntro>

    <div className="term-card">
      <div className="term-bar">
        <span className={cx('term-dot', status.cls)} />
        <span className="term-status">{status.text}</span>
        <span className="term-spacer" />
        <button type="button" className="term-btn" aria-label="Smaller text" title="Smaller text" disabled={fontSize === FONT_SIZES[0]} onClick={() => setFontSize(FONT_SIZES[FONT_SIZES.indexOf(fontSize) - 1] ?? fontSize)}><MinusIcon /></button>
        <span className="term-size">{fontSize}px</span>
        <button type="button" className="term-btn" aria-label="Larger text" title="Larger text" disabled={fontSize === FONT_SIZES.at(-1)} onClick={() => setFontSize(FONT_SIZES[FONT_SIZES.indexOf(fontSize) + 1] ?? fontSize)}><PlusIcon /></button>
        <button type="button" className="term-btn" aria-label="Clear the screen" title="Clear the screen" onClick={() => { termRef.current?.clear(); termRef.current?.focus(); }}><TrashIcon /></button>
        <button type="button" className="term-btn" aria-label={full ? 'Exit full size' : 'Full size'} title={full ? 'Exit full size' : 'Full size'} onClick={() => { setFull(f => !f); requestAnimationFrame(() => { fitRef.current?.fit(); termRef.current?.focus(); }); }}>
          {full ? <ArrowsPointingInIcon /> : <ArrowsPointingOutIcon />}
        </button>
      </div>
      <div className="term-body">
        <div ref={hostRef} className="term-host" onClick={() => termRef.current?.focus()} />
        {(!running || !canUse || state.phase === 'closed') && <div className="term-overlay">
          <CommandLineIcon />
          {!canUse ? <><strong>Viewers can't open a terminal</strong><span>Ask an admin for developer access.</span></>
            : !running ? <><strong>{resource.name} isn't running</strong><span>Start or deploy it to open a shell.</span><Button onClick={() => onNavigate?.('Overview')}>Go to Overview</Button></>
              : <><strong>Session ended</strong><span>{state.reason}</span><Button variant="primary" icon={ArrowPathIcon} onClick={reconnect}>Reconnect</Button></>}
        </div>}
      </div>
    </div>
    <p className="term-foot">Ctrl+Shift+C copies the selection. Sessions close after 15 minutes without input. Opening and closing a terminal is recorded in the audit log; what you type isn't.</p>
  </div>;
}
