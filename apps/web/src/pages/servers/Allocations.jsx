import { useMemo, useState } from 'react';
import { CheckIcon, PencilIcon, PlusIcon, TrashIcon, XMarkIcon } from '@heroicons/react/16/solid';
import { Button, Checkbox, FormField, Notice, SearchField, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { useQuery } from '../../api/hooks';
import './Allocations.css';

/*
 * A server's allocations: the IP:port pairs it hands out, the way hosting panels do it. A new game
 * server takes the lowest free one and shows its alias (or IP) as the join address; deleting the
 * resource frees it. Without any allocations, game servers use the next free port instead.
 */

export function Allocations({ server }) {
  const q = useQuery(() => api.serverAllocations(server.id), [server.id]);
  const addresses = useQuery(() => api.networkAddresses().catch(() => null), []);
  const [selected, setSelected] = useState(() => new Set());
  const [filter, setFilter] = useState('');
  const [form, setForm] = useState({ ip: '', alias: '', ports: '' });
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(null); // { id, alias }
  const list = q.data?.allocations ?? [];
  const shown = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return list.filter(a => !f || a.ip.includes(f) || String(a.port).includes(f) || a.alias.toLowerCase().includes(f) || a.resourceName?.toLowerCase().includes(f));
  }, [list, filter]);
  const free = list.filter(a => !a.resourceId).length;
  const suggestions = [...new Set([addresses.data?.public?.ipv4, ...(addresses.data?.interfaces ?? []).map(i => i.address?.split('/')[0]), '0.0.0.0'].filter(Boolean))];

  async function add(e) {
    e.preventDefault();
    setBusy(true); setErrors({}); setFeedback(null);
    try {
      const out = await api.addServerAllocations(server.id, { ip: form.ip.trim(), alias: form.alias.trim(), ports: form.ports });
      setFeedback({ tone: 'success', text: `Added ${out.added} ${out.added === 1 ? 'allocation' : 'allocations'}${out.skipped ? `; ${out.skipped} already existed` : ''}.` });
      setForm(f => ({ ...f, ports: '' }));
      q.refetch();
    } catch (err) { setErrors(err.fields ?? {}); if (!err.fields) setFeedback({ tone: 'danger', text: err.message }); }
    setBusy(false);
  }
  async function remove(ids) {
    if (!window.confirm(`Delete ${ids.length === 1 ? 'this allocation' : `${ids.length} allocations`}? Ones a resource is using are kept.`)) return;
    setBusy(true); setFeedback(null);
    try {
      const out = await api.deleteServerAllocations(server.id, ids);
      setFeedback({ tone: out.inUse ? 'warning' : 'success', text: `Deleted ${out.deleted}.${out.inUse ? ` ${out.inUse} ${out.inUse === 1 ? 'is' : 'are'} in use and stayed.` : ''}` });
      setSelected(new Set());
      q.refetch();
    } catch (err) { setFeedback({ tone: 'danger', text: err.message }); }
    setBusy(false);
  }
  async function saveAlias() {
    try { await api.updateServerAllocation(server.id, editing.id, { alias: editing.alias.trim() }); setEditing(null); q.refetch(); } catch (err) { setFeedback({ tone: 'danger', text: err.message }); }
  }
  const allChecked = shown.length > 0 && shown.every(a => selected.has(a.id));

  return <div className="rp-page al-page">
    {feedback && <Notice tone={feedback.tone}>{feedback.text}</Notice>}
    <div className="al-grid">
      <section className="al-card">
        <header className="al-head">
          <h2>Existing allocations</h2>
          <span className="al-muted">{list.length ? `${free} free of ${list.length}` : ''}</span>
          <span className="al-spacer" />
          <SearchField value={filter} onChange={setFilter} placeholder="Filter" />
          {selected.size > 0 && <Button icon={TrashIcon} disabled={busy} onClick={() => remove([...selected])}>Delete {selected.size}</Button>}
        </header>
        <div className="al-table-wrap">
          <table className="al-table">
            <thead><tr>
              <th className="check"><Checkbox checked={allChecked} indeterminate={!allChecked && shown.some(a => selected.has(a.id))} label="Select all"
                onChange={on => setSelected(on ? new Set(shown.map(a => a.id)) : new Set())} /></th>
              <th>IP address</th><th>Alias</th><th className="num">Port</th><th>Assigned to</th><th />
            </tr></thead>
            <tbody>
              {shown.map(a => <tr key={a.id} className={cx(selected.has(a.id) && 'selected')}>
                <td className="check"><Checkbox checked={selected.has(a.id)} label={`Select ${a.ip}:${a.port}`} onChange={on => setSelected(s => { const n = new Set(s); on ? n.add(a.id) : n.delete(a.id); return n; })} /></td>
                <td className="mono">{a.ip}</td>
                <td>{editing?.id === a.id
                  ? <form className="al-alias-edit" onSubmit={e => { e.preventDefault(); saveAlias(); }}>
                    <TextInput value={editing.alias} autoFocus aria-label="Alias" placeholder="play.example.com" onChange={e => setEditing(x => ({ ...x, alias: e.target.value }))} onKeyDown={e => e.key === 'Escape' && setEditing(null)} />
                    <button type="submit" className="icon-action" aria-label="Save alias"><CheckIcon /></button>
                    <button type="button" className="icon-action" aria-label="Cancel" onClick={() => setEditing(null)}><XMarkIcon /></button>
                  </form>
                  : <button type="button" className="al-alias" onClick={() => setEditing({ id: a.id, alias: a.alias })} title="Edit alias">{a.alias || <span className="al-muted">Add alias</span>}<PencilIcon /></button>}</td>
                <td className="num mono">{a.port}</td>
                <td>{a.resourceId ? <span className="al-assigned"><strong>{a.resourceName || 'A resource'}</strong>{a.teamName && <small>{a.teamName}</small>}</span> : <span className="rp-chip success">Free</span>}</td>
                <td className="actions">{!a.resourceId && <button type="button" className="icon-action danger" aria-label={`Delete ${a.ip}:${a.port}`} title="Delete" disabled={busy} onClick={() => remove([a.id])}><TrashIcon /></button>}</td>
              </tr>)}
              {q.data && !shown.length && <tr><td colSpan={6} className="al-empty">{list.length ? 'Nothing matches.' : 'No allocations yet. Game servers on this machine use the next free port until you add some.'}</td></tr>}
              {!q.data && <tr><td colSpan={6} className="al-empty">{q.error ? q.error.message : 'Loading…'}</td></tr>}
            </tbody>
          </table>
        </div>
      </section>

      <form className="al-card al-form" onSubmit={add}>
        <header className="al-head"><h2>Assign new allocations</h2></header>
        <div className="al-form-body">
          <FormField label="IP address" htmlFor="al-ip" error={errors.ip} hint="The address players connect to. 0.0.0.0 means every address on this machine.">
            <TextInput id="al-ip" className="mono" list="al-ip-suggestions" value={form.ip} placeholder="203.0.113.10" onChange={e => setForm(f => ({ ...f, ip: e.target.value }))} />
            <datalist id="al-ip-suggestions">{suggestions.map(s => <option key={s} value={s} />)}</datalist>
          </FormField>
          <FormField label="IP alias" htmlFor="al-alias" error={errors.alias} hint="Optional. Shown to players instead of the IP, like play.example.com.">
            <TextInput id="al-alias" value={form.alias} placeholder="play.example.com" onChange={e => setForm(f => ({ ...f, alias: e.target.value }))} />
          </FormField>
          <FormField label="Ports" htmlFor="al-ports" error={errors.ports} hint="Single ports or ranges, separated by commas or spaces: 25565, 25570-25580. Up to 1,000 at once.">
            <TextInput id="al-ports" className="mono" value={form.ports} placeholder="25565-25570, 27015" onChange={e => setForm(f => ({ ...f, ports: e.target.value }))} />
          </FormField>
        </div>
        <footer className="al-foot"><Button type="submit" variant="primary" icon={PlusIcon} disabled={busy || !form.ip.trim() || !form.ports.trim()}>{busy ? 'Adding…' : 'Add allocations'}</Button></footer>
      </form>
    </div>
  </div>;
}
