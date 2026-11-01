import { useState } from 'react';
import { ChatBubbleLeftIcon, EllipsisHorizontalIcon, PlusIcon, UserGroupIcon, XMarkIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Menu, Notice, SegmentedControl, Select, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { Empty } from '../resource/shared';
import './game.css';

/*
 * Who's on, and who may join. Changes go through the server's console so they apply at once and the
 * game keeps its own files right; that's why they need the server running. The lists themselves are
 * read from the game's files, so they show even while it's stopped.
 */

export function GamePlayers({ resource }) {
  const { user } = useSession();
  const canManage = can(user, 'games.players');
  const q = useQuery(() => api.gamePlayers(resource.id), [resource.id], { pollMs: 5000 });
  const [tab, setTab] = useState(null);
  const [acting, setActing] = useState(null); // { action, player }
  const [result, setResult] = useState(null);
  const [anyName, setAnyName] = useState('');

  if (q.error && !q.data) return <div className="rp-page gm-page"><Notice tone="danger" title="Couldn't load players">{q.error.message}</Notice></div>;
  if (!q.data) return <div className="rp-page gm-page"><p className="gm-muted">Loading…</p></div>;
  const d = q.data;
  const list = d.lists.find(l => l.id === (tab ?? d.lists[0]?.id));

  async function run(body, label) {
    setResult(null);
    try {
      const out = await api.gamePlayerAction(resource.id, body);
      setResult({ tone: 'success', text: out.output || `${label}: done.` });
      q.refetch();
      return true;
    } catch (e) {
      setResult({ tone: 'danger', text: e.fields ? Object.values(e.fields).join(' ') : e.message });
      return false;
    }
  }
  const quickAction = (a, player) => (a.input || a.options?.length) ? setActing({ action: a, player }) : run({ action: a.id, player }, a.label);
  const actionsFor = player => d.actions.map(a => ({ label: a.label, danger: a.danger, onSelect: () => quickAction(a, player) }));

  return <div className="rp-page gm-page gm-players">
    {!d.running && <Notice tone="info" title="The server is stopped">The lists below are read from its files. Start the server to change them or to act on players.</Notice>}
    {result && <Notice tone={result.tone}>{result.text}</Notice>}

    <section className="rp-card">
      <header className="gm-card-head"><h2>Online</h2><span className="gm-muted">{!d.running ? 'Offline' : d.answering ? `${d.count} of ${d.max}` : 'Starting'}</span></header>
      {d.online.length ? d.online.map(name => <div key={name} className="rp-row gm-player">
        <img className="gm-head" src={`https://mc-heads.net/avatar/${encodeURIComponent(name)}/24`} alt="" onError={e => { e.currentTarget.style.visibility = 'hidden'; }} />
        <span className="rp-row-main"><strong>{name}</strong></span>
        {canManage && <>
          {d.actions.some(a => a.id === 'tell') && <Button icon={ChatBubbleLeftIcon} onClick={() => quickAction(d.actions.find(a => a.id === 'tell'), name)}>Message</Button>}
          {d.actions.some(a => a.id === 'kick') && <Button onClick={() => quickAction(d.actions.find(a => a.id === 'kick'), name)}>Kick</Button>}
          <Menu label={`More for ${name}`} items={actionsFor(name).filter(a => !['Message', 'Kick'].includes(a.label))} className="icon-action"><EllipsisHorizontalIcon /></Menu>
        </>}
      </div>) : <p className="gm-pad gm-muted">{!d.running ? 'The server is stopped.' : !d.answering ? "The server is starting and doesn't answer yet. Players show here once it's up." : d.count > 0 ? `${d.count} online. This game doesn't share player names; act on someone by name below.` : 'Nobody is online right now.'}</p>}
      {canManage && d.running && <form className="gm-row-form" onSubmit={e => e.preventDefault()}>
        <TextInput value={anyName} placeholder="Any player's name" aria-label="Player name" onChange={e => setAnyName(e.target.value)} />
        {anyName.trim() ? <Menu label="Act on this player" items={actionsFor(anyName.trim())} className="button">Act on player…</Menu> : <Button disabled>Act on player…</Button>}
      </form>}
    </section>

    {d.lists.length > 0 && <section className="gm-lists">
      <SegmentedControl label="Player lists" className="auto-width" value={list.id} onChange={setTab}
        options={d.lists.map(l => ({ value: l.id, label: `${l.label}${l.entries.length ? ` · ${l.entries.length}` : ''}` }))} />
      <PlayerList key={list.id} list={list} running={d.running} canManage={canManage} nameRule={d.nameRule} onRun={run} />
    </section>}

    {acting && <ActionDialog act={acting} onClose={() => setActing(null)} onRun={async body => { if (await run(body, acting.action.label)) setActing(null); }} />}
  </div>;
}

function PlayerList({ list, running, canManage, nameRule, onRun }) {
  const [name, setName] = useState('');
  const [reason, setReason] = useState('');
  const [error, setError] = useState('');
  const isIP = list.id === 'ipbans';
  async function add(e) {
    e.preventDefault();
    const v = name.trim();
    if (!isIP && !new RegExp(nameRule).test(v)) return setError('That isn\'t a valid player name.');
    setError('');
    if (await onRun({ action: 'add', list: list.id, player: v, input: reason }, `Added ${v}`)) { setName(''); setReason(''); }
  }
  return <div className="rp-card">
    <header className="gm-card-head">
      <span className="gm-muted">{list.description}</span>
      {list.toggle && list.enabled != null && <label className="gm-toggle"><Switch label={`${list.label} on`} checked={list.enabled} disabled={!running || !canManage}
        onChange={on => onRun({ action: on ? 'enable' : 'disable', list: list.id }, list.label)} />{list.enabled ? 'On' : 'Off'}</label>}
    </header>
    {list.error && <Notice tone="danger">{list.error}</Notice>}
    {canManage && list.add && <form className="gm-row-form" onSubmit={add}>
      <TextInput value={name} placeholder={isIP ? 'IP address' : 'Player name'} aria-label="Player name" onChange={e => setName(e.target.value)} disabled={!running} />
      {list.id === 'bans' && <TextInput value={reason} placeholder="Reason (optional)" aria-label="Reason" onChange={e => setReason(e.target.value)} disabled={!running} />}
      <Button type="submit" icon={PlusIcon} disabled={!running || !name.trim()}>{list.id === 'bans' ? 'Ban' : 'Add'}</Button>
      {error && <span className="gm-error">{error}</span>}
    </form>}
    {list.entries.length ? list.entries.map(e => <div key={e.name} className="rp-row">
      {!isIP && <img className="gm-head" src={`https://mc-heads.net/avatar/${encodeURIComponent(e.uuid || e.name)}/24`} alt="" onError={ev => { ev.currentTarget.style.visibility = 'hidden'; }} />}
      <span className="rp-row-main"><strong className={cx(isIP && 'mono')}>{e.name}</strong>
        {(e.reason || e.created || e.level) && <span>{[e.level && `Level ${e.level}`, e.reason && e.reason !== 'Banned by an operator.' && e.reason, e.source && `by ${e.source}`, e.created && new Date(e.created.replace(' +', '+').replace(' ', 'T')).toLocaleDateString()].filter(Boolean).join(' · ')}</span>}</span>
      {canManage && list.remove && <button type="button" className="icon-action danger" disabled={!running} aria-label={`Remove ${e.name}`} title={list.id.includes('ban') ? 'Unban' : 'Remove'}
        onClick={() => onRun({ action: 'remove', list: list.id, player: e.name }, `Removed ${e.name}`)}><XMarkIcon /></button>}
    </div>) : <Empty icon={UserGroupIcon} title={`No one on the ${list.label.toLowerCase()}`}>{list.add ? 'Add players by name above.' : ''}</Empty>}
  </div>;
}

function ActionDialog({ act, onClose, onRun }) {
  const { action, player } = act;
  const [input, setInput] = useState('');
  const [value, setValue] = useState(action.options?.[0]?.value ?? '');
  const [busy, setBusy] = useState(false);
  async function submit(e) {
    e.preventDefault();
    setBusy(true);
    await onRun({ action: action.id, player, input, value });
    setBusy(false);
  }
  return <Dialog open onClose={onClose} title={`${action.label}: ${player}`} className="gm-dialog"
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant={action.danger ? 'danger-solid' : 'primary'} disabled={busy || (action.input === 'Message' && !input.trim())} onClick={submit}>{action.label}</Button></>}>
    <form onSubmit={submit}>
      {action.options?.length > 0 && <FormField label="Choose" htmlFor="gm-act-v"><Select id="gm-act-v" value={value} onChange={setValue} options={action.options} /></FormField>}
      {action.input && <FormField label={action.input} htmlFor="gm-act-i"><TextInput id="gm-act-i" value={input} autoFocus maxLength={300} onChange={e => setInput(e.target.value)} /></FormField>}
    </form>
  </Dialog>;
}
