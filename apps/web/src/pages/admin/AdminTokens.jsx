import { useState } from 'react';
import { ArrowPathIcon, ClipboardDocumentIcon, KeyIcon, PlusIcon, TrashIcon, BookOpenIcon } from '@heroicons/react/16/solid';
import { api } from '../../api/client';
import { useQuery } from '../../api/hooks';
import { Button, Checkbox, Dialog, FormField, Notice, TextInput, Status, Select } from '../../ui';
import { useAccountAction } from '../settings/Profile/AccountConfirm';
import '../settings/settings.css';
import './AdminTokens.css';

export function AdminTokens() {
  const query=useQuery(api.adminTokens,[]);
  const action=useAccountAction();
  const [creating,setCreating]=useState(false);
  const [secret,setSecret]=useState(null);
  const [remove,setRemove]=useState(null);
  const [rotate,setRotate]=useState(null);
  const [docs,setDocs]=useState(false);
  const [copied,setCopied]=useState(false);
  async function created(fn){const out=await fn();setCreating(false);setRotate(null);setCopied(false);setSecret(out);await query.refetch();}
  return <div className="settings-page admin-tokens">
    <p className="account-muted">Instance admin tokens let external systems manage customers, teams, products and resources. Permissions are checked against the creator’s current access on every request.</p>
    {(query.error||action.error)&&<Notice tone="danger" title="Couldn't manage admin tokens">{action.error||query.error?.message}</Notice>}
    <div className="admin-token-toolbar"><Button icon={BookOpenIcon} onClick={()=>setDocs(true)}>API reference</Button><Button icon={PlusIcon} variant="primary" disabled={!query.data} onClick={()=>{action.clearError();setCreating(true);}}>Create admin token</Button></div>
    <div className="settings-card admin-token-list">
      {!query.data?<p className="account-muted">Loading tokens…</p>:!query.data.tokens.length?<p className="account-muted">No admin tokens yet. Create a token for each integration and select only the permissions it needs.</p>:query.data.tokens.map(token=>{
        const creatorInactive=token.creatorActive===false;
        const revoked=!!token.revokedAt,expired=!!token.expiresAt&&new Date(token.expiresAt)<=new Date();
        return <article className="admin-token-row" key={token.id}><KeyIcon/><div className="admin-token-main"><strong>{token.name} <Status tone={revoked||expired||creatorInactive?'neutral':'success'}>{revoked?'Revoked':expired?'Expired':creatorInactive?'Creator inactive':'Active'}</Status></strong><code>{token.prefix}…</code><small>{token.permissions.length} permissions · {token.expiresAt?`Expires ${new Date(token.expiresAt).toLocaleDateString()}`:'Never expires'} · {token.lastUsedAt?`Last used ${new Date(token.lastUsedAt).toLocaleString()}`:'Never used'}</small>{token.networks.length>0&&<small>Allowed networks: {token.networks.join(', ')}</small>}<details><summary>Permissions</summary><p>{token.permissions.join(', ')}</p><small>Creator ID: {token.createdBy}{token.lastIp?` · Last IP: ${token.lastIp}`:''}</small></details></div>
        {!revoked&&!expired&&<div className="admin-token-actions"><Button icon={ArrowPathIcon} disabled={action.pending||creatorInactive} onClick={()=>{action.clearError();setRotate(token);}}>Rotate</Button><Button icon={TrashIcon} disabled={action.pending} onClick={()=>{action.clearError();setRemove(token);}}>Revoke</Button></div>}</article>;
      })}
    </div>
    {creating&&<CreateToken permissions={query.data.permissions} pending={action.pending} error={action.error} onClose={()=>setCreating(false)} onCreate={body=>action.run(()=>created(()=>api.createAdminToken(body)))}/>}
    <Dialog open={!!secret} title="Copy your admin token" description="This secret is shown once. Store it in your integration’s secret manager; it cannot be recovered from Ignition." onClose={()=>setSecret(null)} footer={<Button onClick={()=>setSecret(null)}>Done</Button>}>
      <div className="admin-token-secret"><code>{secret?.token}</code><Button icon={ClipboardDocumentIcon} onClick={async()=>{try{await navigator.clipboard.writeText(secret.token);setCopied(true);}catch{setCopied(false);}}}>{copied?'Copied':'Copy token'}</Button></div>
      <Notice tone="info">Use Authorization: Bearer &lt;token&gt; with {query.data?.baseUrl}. This token cannot be used as a browser session or create other admin tokens.</Notice>
    </Dialog>
    <Dialog open={!!remove} title={`Revoke ${remove?.name||'token'}?`} description="The token stops authorizing new requests immediately. Integrations using it will need another token." onClose={()=>setRemove(null)} footer={<><Button onClick={()=>setRemove(null)}>Cancel</Button><Button variant="danger-solid" disabled={action.pending} onClick={()=>action.run(async()=>{await api.revokeAdminToken(remove.id);setRemove(null);await query.refetch();})}>Revoke token</Button></>}>
      {action.error&&<Notice tone="danger">{action.error}</Notice>}
    </Dialog>
    <Dialog open={!!rotate} title={`Rotate ${rotate?.name||'token'}?`} description={`Creates a replacement with the same creator, permissions and networks. The previous secret is revoked immediately after the replacement is created. ${rotate?.expiresAt?'The replacement expires in 30 days.':'The replacement never expires.'}`} onClose={()=>setRotate(null)} footer={<><Button onClick={()=>setRotate(null)}>Cancel</Button><Button disabled={action.pending} onClick={()=>action.run(()=>created(()=>api.rotateAdminToken(rotate.id,rotate.expiresAt?{}:{expiresAt:null})))}>Rotate token</Button></>}>
      {action.error&&<Notice tone="danger">{action.error}</Notice>}
    </Dialog>
    <Dialog open={docs} title="Instance Admin API" description="Version 1 · Bearer admin tokens only. Customer/resource tokens and browser sessions do not authenticate these endpoints." onClose={()=>setDocs(false)} footer={<Button onClick={()=>setDocs(false)}>Done</Button>} className="admin-token-docs">
      <div className="admin-token-reference"><code>{query.data?.baseUrl}</code><p>GET /defaults reports account and provisioning defaults. Create a customer, reuse its automatically created personal team when enabled, then provision POST /resources with a product and plan, or a manual configuration. Requests support Idempotency-Key; list endpoints accept limit and cursor.</p><pre>{`curl "${query.data?.baseUrl}/products" \
  -H "Authorization: Bearer $IGNITION_ADMIN_TOKEN"`}</pre><p>Download the OpenAPI contract using your token: GET /openapi.json. Setup/reset links and webhook signing secrets are shown only by their creation endpoints. Email delivery is handled by your integration.</p>
      {query.data?.routes.map(route=><div className="admin-token-reference-route" key={route.Pattern}><code>{route.Pattern}</code><small>{route.Permission||'Valid admin token'}</small></div>)}</div>
    </Dialog>
    {action.confirmation}
  </div>;
}
function CreateToken({permissions,pending,error,onClose,onCreate}){
 const [name,setName]=useState('');const [selected,setSelected]=useState([]);const [networks,setNetworks]=useState('');
 const [expires,setExpires]=useState('30');
 const groups=permissions.reduce((out,p)=>{(out[p.group]??=[]).push(p);return out;},{});
 const selectPreset=keys=>setSelected(permissions.filter(p=>keys.includes(p.Key||p.key)).map(p=>p.Key||p.key));
 return <Dialog open title="Create admin token" onClose={onClose} description="Select explicit permissions and an expiry. Tokens cannot grant customer accounts instance administrator privileges." footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="admin-token-create" disabled={pending||!name.trim()||!selected.length}>Create token</Button></>}>
 <form id="admin-token-create" className="admin-token-form" onSubmit={e=>{e.preventDefault();onCreate({name:name.trim(),permissions:selected,networks:networks.split(/[\s,]+/).filter(Boolean),expiresAt:expires==='never'?null:new Date(Date.now()+Number(expires)*86400000).toISOString()});}}>
 {error&&<Notice tone="danger">{error}</Notice>}
 <FormField label="Name" htmlFor="admin-token-name"><TextInput id="admin-token-name" value={name} onChange={e=>setName(e.target.value)} maxLength={80} required placeholder="Billing integration"/></FormField>
 <FormField label="Expires on" htmlFor="admin-token-expiry"><Select id="admin-token-expiry" label="Expires on" value={expires} onChange={setExpires} options={[{value:'7',label:'7 days'},{value:'30',label:'30 days'},{value:'90',label:'90 days'},{value:'365',label:'365 days'},{value:'never',label:'Never'}]}/></FormField>
 <FormField label="Allowed networks" htmlFor="admin-token-networks" hint="Optional IPs or CIDRs, separated by commas. Empty allows any network."><TextInput id="admin-token-networks" value={networks} onChange={e=>setNetworks(e.target.value)} placeholder="203.0.113.10, 2001:db8::/32"/></FormField>
 <div className="admin-token-presets"><Button type="button" onClick={()=>selectPreset(['settings.view','servers.view','resources.view','usage.view'])}>Monitoring</Button><Button type="button" onClick={()=>selectPreset(['users.view','users.create','users.setup','teams.view','products.view','resources.view','resources.provision','resources.suspend','resources.change-plan'])}>Hosting</Button><Button type="button" onClick={()=>setSelected([])}>Clear</Button></div>
 {Object.entries(groups).map(([group,list])=><fieldset key={group}><legend>{group}</legend>{list.map(p=>{const key=p.Key||p.key;return <div key={key} className="admin-token-permission" onClick={()=>setSelected(old=>old.includes(key)?old.filter(v=>v!==key):[...old,key])}><Checkbox label={p.Label||p.label} checked={selected.includes(key)} onChange={on=>setSelected(old=>on?[...old,key]:old.filter(v=>v!==key))}/><span><strong>{p.Label||p.label}</strong><small>{key}</small></span></div>;})}</fieldset>)}
 </form></Dialog>;
}
