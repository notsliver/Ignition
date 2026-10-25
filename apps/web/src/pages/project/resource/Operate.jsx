import { useEffect, useMemo, useRef, useState } from 'react';
import { TrashIcon } from '@heroicons/react/16/solid';
import { Select, cx } from '../../../ui';
import { PageIntro } from './shared';


const servers = ['production-01', 'production-02'];

/* --------------------------------- Terminal --------------------------------- */

export function Terminal({ resource }) {
  const containers = useMemo(() => [0, 1].slice(0, resource.type === 'application' ? 2 : 1).map(i => ({ value: `c${i}`, label: `${resource.name}-${i + 1} · ${servers[i % 2]}` })), [resource]);
  const [container, setContainer] = useState(containers[0].value);
  const [connected, setConnected] = useState(true);
  const [output, setOutput] = useState(() => [['ok', `Connected to ${containers[0].label}`], ['dim', 'Type "help" for available commands.']]);
  const [input, setInput] = useState('');
  const [history, setHistory] = useState([]);
  const [cursor, setCursor] = useState(-1);
  const inputRef = useRef(null);
  const outputRef = useRef(null);
  const cwd = '/app';

  useEffect(() => { outputRef.current?.scrollTo(0, outputRef.current.scrollHeight); }, [output]);

  const commands = {
    help: () => [['', 'Available: help, ls, pwd, whoami, env, ps, df, date, echo, uname, node -v, clear, exit']],
    ls: () => [['', 'node_modules  package.json  package-lock.json  dist  public  server.js']],
    pwd: () => [['', cwd]],
    whoami: () => [['', 'node']],
    env: () => [['', 'NODE_ENV=production'], ['', 'PORT=3000'], ['', 'HOSTNAME=' + resource.name], ['dim', '(secret values are hidden)']],
    ps: () => [['', '  PID USER     TIME  COMMAND'], ['', '    1 node     2:14  node server.js'], ['', '   42 node     0:00  sh']],
    df: () => [['', 'Filesystem      Size  Used Avail Use% Mounted on'], ['', 'overlay          40G   12G   28G  30% /']],
    date: () => [['', new Date().toString()]],
    uname: () => [['', 'Linux ' + resource.name + ' 6.8.0 x86_64 GNU/Linux']],
    'node -v': () => [['', 'v22.11.0']],
  };

  function run(event) {
    event.preventDefault();
    const cmd = input.trim();
    setInput('');
    setCursor(-1);
    if (!cmd) return;
    setHistory(h => [cmd, ...h].slice(0, 50));
    if (cmd === 'clear') return setOutput([]);
    if (cmd === 'exit') { setConnected(false); return setOutput(o => [...o, ['cmd', `$ ${cmd}`], ['dim', 'Session closed.']]); }
    const result = cmd.startsWith('echo ') ? [['', cmd.slice(5)]] : commands[cmd]?.() ?? [['error', `sh: ${cmd.split(' ')[0]}: command not found`]];
    setOutput(o => [...o, ['cmd', `${cwd} $ ${cmd}`], ...result]);
  }

  function onKeyDown(event) {
    if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
      event.preventDefault();
      const next = Math.max(-1, Math.min(history.length - 1, cursor + (event.key === 'ArrowUp' ? 1 : -1)));
      setCursor(next);
      setInput(next < 0 ? '' : history[next]);
    } else if (event.key === 'l' && event.ctrlKey) {
      event.preventDefault();
      setOutput([]);
    }
  }

  function connect(value = container) {
    const label = containers.find(c => c.value === value).label;
    setContainer(value);
    setConnected(true);
    setOutput(o => [...o, ['ok', `Connected to ${label}`]]);
    requestAnimationFrame(() => inputRef.current?.focus());
  }

  return <div className="rp-page">
    <PageIntro actions={<Select label="Container" options={containers} value={container} onChange={connect} className="terminal-select" />}>
      Run commands inside a running container. Sessions are recorded in the audit log and close after 15 minutes of inactivity.
    </PageIntro>
    <div className="rp-console terminal" onClick={() => inputRef.current?.focus()}>
      <div className="rp-console-bar">
        <span className={cx('live-dot', !connected && 'paused')}>{connected ? 'Connected' : 'Disconnected'}</span>
        <span className="spacer" />
        {!connected && <button type="button" className="terminal-reconnect" onClick={() => connect()}>Reconnect</button>}
        <button type="button" className="icon-action" aria-label="Clear terminal" title="Clear (Ctrl+L)" onClick={() => setOutput([])}><TrashIcon /></button>
      </div>
      <pre ref={outputRef}>{output.map(([tone, text], i) => <span key={i} className={tone}>{text}{'\n'}</span>)}</pre>
      <form className="terminal-input" onSubmit={run}>
        <label htmlFor="terminal-command" className="cmd">{cwd} $</label>
        <input id="terminal-command" ref={inputRef} value={input} onChange={event => setInput(event.target.value)} onKeyDown={onKeyDown} disabled={!connected} autoComplete="off" spellCheck={false} autoFocus aria-label="Command" />
      </form>
    </div>
  </div>;
}
