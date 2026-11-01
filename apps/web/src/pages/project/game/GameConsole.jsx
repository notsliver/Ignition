import { useEffect, useRef, useState } from 'react';
import { ArrowDownIcon, PauseIcon, PlayIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Notice, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useSession, can } from '../../../app/AuthGate';
import { GamePage, useGame } from './shared';
import './game.css';

/*
 * The server's live output with a command line under it, like sitting at the server. Java servers
 * answer over RCON, so replies show right under the command; Bedrock answers in the log itself.
 * Up and down walk through earlier commands; Tab completes common ones.
 */

const MAX = 3000;
const COMMON = ['say ', 'list', 'save-all', 'time set day', 'time set night', 'weather clear', 'weather rain', 'gamemode survival ', 'gamemode creative ', 'tp ', 'give ',
  'whitelist add ', 'whitelist remove ', 'op ', 'deop ', 'kick ', 'ban ', 'pardon ', 'difficulty ', 'gamerule ', 'tps', 'plugins', 'stop'];
const levelOf = m => /\b(ERROR|SEVERE|FATAL)\b|Exception/.test(m) ? 'error' : /\bWARN(ING)?\b/.test(m) ? 'warn' : /joined the game|logged in with/.test(m) ? 'ok' : '';
const histKey = id => `ignition.console.${id}`;

export function GameConsole({ resource, onNavigate }) {
  const { user } = useSession();
  const canSend = can(user, 'games.console');
  const game = useGame(resource, 10000);
  const [lines, setLines] = useState([]);
  const [live, setLive] = useState(true);
  const [stuck, setStuck] = useState(true);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [note, setNote] = useState('');
  const history = useRef([]);
  const cursor = useRef(-1);
  const last = useRef('');
  const box = useRef(null);
  const inputRef = useRef(null);

  useEffect(() => { try { history.current = JSON.parse(localStorage.getItem(histKey(resource.id)) ?? '[]'); } catch { history.current = []; } }, [resource.id]);

  // History first, then only lines newer than the last one shown.
  useEffect(() => {
    let stop = false;
    async function load(initial) {
      try {
        const since = !initial && last.current ? Math.floor(new Date(last.current).getTime() / 1000) : undefined;
        const out = await api.containerLogs(resource.id, initial ? { tail: 400 } : { tail: 1000, since });
        if (stop) return;
        setNote(out.available === false ? out.reason : '');
        const fresh = (out.lines ?? []).filter(l => initial || !last.current || l.time > last.current);
        if (fresh.length) {
          last.current = fresh.at(-1).time;
          setLines(cur => (initial ? fresh.map(l => ({ ...l, kind: 'log' })) : [...cur, ...fresh.map(l => ({ ...l, kind: 'log' }))]).slice(-MAX));
        }
      } catch (e) { if (!stop) setNote(e.message); }
    }
    load(true);
    const t = setInterval(() => { if (live && document.visibilityState === 'visible') load(false); }, 1500);
    return () => { stop = true; clearInterval(t); };
  }, [resource.id, live]);

  useEffect(() => { if (stuck && box.current) box.current.scrollTop = box.current.scrollHeight; }, [lines, stuck]);
  const onScroll = () => { const el = box.current; setStuck(el.scrollHeight - el.scrollTop - el.clientHeight < 24); };

  async function send(command) {
    command = command.trim().replace(/^\//, '');
    if (!command || sending) return;
    setSending(true);
    history.current = [command, ...history.current.filter(h => h !== command)].slice(0, 100);
    try { localStorage.setItem(histKey(resource.id), JSON.stringify(history.current)); } catch { /* private mode */ }
    cursor.current = -1;
    setInput('');
    const at = new Date().toISOString();
    setLines(cur => [...cur, { kind: 'cmd', time: at, message: command }]);
    setStuck(true);
    try {
      const out = await api.gameConsole(resource.id, command);
      if (out.replies) setLines(cur => [...cur, { kind: 'reply', time: at, message: out.output || '(no reply)' }]);
    } catch (e) {
      setLines(cur => [...cur, { kind: 'fail', time: at, message: e.message }]);
    }
    setSending(false);
    inputRef.current?.focus();
  }

  function onKey(e) {
    if (e.key === 'Enter') { e.preventDefault(); send(input); }
    else if (e.key === 'ArrowUp' && history.current.length) {
      e.preventDefault();
      cursor.current = Math.min(cursor.current + 1, history.current.length - 1);
      setInput(history.current[cursor.current]);
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      cursor.current = Math.max(cursor.current - 1, -1);
      setInput(cursor.current < 0 ? '' : history.current[cursor.current]);
    } else if (e.key === 'Tab' && input) {
      const match = [...history.current, ...COMMON].find(c => c.startsWith(input) && c !== input);
      if (match) { e.preventDefault(); setInput(match); }
    }
  }

  const data = game.data;
  const running = resource.status === 'running';
  return <GamePage query={game} title="the console" className="gm-console-page">
    {data && <>
      {!data.console && <Notice tone="info" title="No console">This game has no console Ignition can talk to.</Notice>}
      {data.console && !data.replies && <Notice tone="info" title="Replies show in the log">{data.edition.name} servers don't answer commands directly; their reply appears in the output below.</Notice>}
      <div className="rp-console gm-console">
        <div className="rp-console-bar">
          <span className={cx('gm-dot', running ? (data.status ? 'on' : 'wait') : 'off')} />
          <span>{running ? (data.status ? `Online · ${data.status.players}/${data.status.maxPlayers} players` : 'Starting') : 'Stopped'}</span>
          <span className="spacer" />
          <button type="button" className="icon-action" onClick={() => setLive(l => !l)} title={live ? 'Pause' : 'Resume'} aria-label={live ? 'Pause output' : 'Resume output'}>{live ? <PauseIcon /> : <PlayIcon />}</button>
          <button type="button" className="icon-action" onClick={() => setLines([])} title="Clear the view" aria-label="Clear the view"><TrashIcon /></button>
        </div>
        <div ref={box} className="gm-console-out" onScroll={onScroll} role="log" aria-live="off">
          {lines.map((l, i) => <div key={i} className={cx('gm-line', l.kind, l.kind === 'log' && levelOf(l.message))}>
            {l.kind === 'cmd' ? <><span className="cmd">&gt;</span> {l.message}</> : l.message}
          </div>)}
          {!lines.length && <div className="gm-line dim">{note || (running ? 'Waiting for output…' : 'The server is stopped. Its output appears here when it starts.')}</div>}
        </div>
        {!stuck && <button type="button" className="gm-jump" onClick={() => setStuck(true)}><ArrowDownIcon />Newest</button>}
        {canSend && data.console && <form className="gm-console-input" onSubmit={e => { e.preventDefault(); send(input); }}>
          <span>&gt;</span>
          <input ref={inputRef} value={input} onChange={e => { setInput(e.target.value); cursor.current = -1; }} onKeyDown={onKey} disabled={!running}
            placeholder={running ? 'Type a command, e.g. say Hello (↑ for history, Tab to complete)' : 'Start the server to send commands'} aria-label="Console command" spellCheck={false} autoComplete="off" />
          <Button type="submit" variant="primary" disabled={!running || !input.trim() || sending}>Send</Button>
        </form>}
      </div>
      {running && canSend && data.quick.length > 0 && <div className="gm-chips">{data.quick.map(q => <button key={q.command} type="button" className="gm-chip" title={q.hint} onClick={() => send(q.command)}>{q.label}</button>)}</div>}
      <p className="gm-muted gm-small">Every command is recorded in the audit log. Older output is under <button type="button" className="inline-link" onClick={() => onNavigate('Logs')}>Logs</button>.</p>
    </>}
  </GamePage>;
}
