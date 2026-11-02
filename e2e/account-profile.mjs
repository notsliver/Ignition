import {chromium} from 'playwright';
import {readFileSync} from 'node:fs';
import {createHmac} from 'node:crypto';
import assert from 'node:assert/strict';
// Disposable fixtures only: this test changes credentials and permanently deletes the fixture account.
// Fixture: session, user (UUID), team (slug), password, email; another team owner must remain.
if (!process.env.IGNITION_TEST_ACCOUNT_FIXTURE) throw new Error('IGNITION_TEST_ACCOUNT_FIXTURE must name disposable account fixtures');
const f=JSON.parse(readFileSync(process.env.IGNITION_TEST_ACCOUNT_FIXTURE,'utf8'));
const base=process.env.IGNITION_URL || 'http://localhost:55463';
const browser=await chromium.launch({executablePath:process.env.IGNITION_BROWSER_EXECUTABLE || undefined,headless:true});
const context=await browser.newContext({viewport:{width:1280,height:1000},reducedMotion:'reduce'});
await context.addCookies([{name:'ignition_session',value:f.session,url:base}]);
const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(12000);
const cdp=await context.newCDPSession(page);await cdp.send('WebAuthn.enable');await cdp.send('WebAuthn.addVirtualAuthenticator',{options:{protocol:'ctap2',transport:'internal',hasResidentKey:true,hasUserVerification:true,isUserVerified:true,automaticPresenceSimulation:true}});
const updatedEmail=`updated-${f.user.slice(0,8)}@example.test`;
const dialog=title=>page.getByRole('dialog',{name:title,exact:true});
async function confirmWithPasskey(){const d=dialog('Confirm your identity');await d.getByRole('button',{name:'Confirm with a passkey',exact:true}).click();await d.waitFor({state:'hidden'});}
function totp(secret){const abc='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';let bits='';for(const c of secret.toUpperCase().replace(/\s/g,''))bits+=abc.indexOf(c).toString(2).padStart(5,'0');const key=Buffer.from(bits.match(/.{8}/g).map(b=>parseInt(b,2)));const counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const hash=createHmac('sha1',key).update(counter).digest();const offset=hash.at(-1)&15;return ((hash.readUInt32BE(offset)&0x7fffffff)%1000000).toString().padStart(6,'0');}
try{
 await page.goto(base+`/teams/${f.team}/settings/profile`);
 await page.getByLabel('Name',{exact:true}).fill('Updated profile');
 await page.getByRole('button',{name:'Save changes',exact:true}).click();await page.getByText('Account information saved.',{exact:true}).waitFor();
 await page.getByLabel('Email',{exact:true}).fill(updatedEmail);await page.getByRole('button',{name:'Save changes',exact:true}).click();
 await dialog('Confirm your identity').getByLabel('Current password').fill(f.password);await dialog('Confirm your identity').getByRole('button',{name:'Confirm',exact:true}).click();await dialog('Confirm your identity').waitFor({state:'hidden'});
 await page.reload();await page.getByLabel('Email',{exact:true}).waitFor();assert.equal(await page.getByLabel('Email',{exact:true}).inputValue(),updatedEmail);
 await page.getByRole('button',{name:'Connect',exact:true}).waitFor();assert.equal(await page.getByRole('button',{name:'Connect',exact:true}).isDisabled(),true);
 if(process.env.IGNITION_ACCOUNT_SCREENSHOTS) await page.screenshot({path:process.env.IGNITION_ACCOUNT_SCREENSHOTS+'/profile-page.png',fullPage:true});
 await page.locator('.sidebar').getByRole('button',{name:'Security',exact:true}).click();
 await page.getByRole('button',{name:'Add passkey',exact:true}).click();await page.getByLabel('Passkey name').fill('QA virtual device');await dialog('Add a passkey').getByRole('button',{name:'Continue',exact:true}).click();
 await page.getByText('Passkey added.',{exact:true}).waitFor();await page.getByText('QA virtual device',{exact:true}).waitFor();
 // Make a real assertion but tamper with its origin. It must fail and consume the challenge.
 const begin=await context.request.post(base+'/api/auth/passkeys/begin',{headers:{Origin:base}});assert.equal(begin.status(),200);const flow=await begin.json();
 const credential=await page.evaluate(async opts=>{
  const dec=v=>Uint8Array.from(atob(v.replace(/-/g,'+').replace(/_/g,'/')),c=>c.charCodeAt(0));const enc=v=>btoa(String.fromCharCode(...new Uint8Array(v))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
  const k={...opts.publicKey,challenge:dec(opts.publicKey.challenge)};const c=await navigator.credentials.get({publicKey:k});return {id:c.id,rawId:enc(c.rawId),type:c.type,clientExtensionResults:c.getClientExtensionResults(),response:{clientDataJSON:enc(c.response.clientDataJSON),authenticatorData:enc(c.response.authenticatorData),signature:enc(c.response.signature),userHandle:enc(c.response.userHandle)}};
 },flow.options);
 const forged=structuredClone(credential);const data=JSON.parse(Buffer.from(forged.response.clientDataJSON,'base64url'));data.origin='https://attacker.test';forged.response.clientDataJSON=Buffer.from(JSON.stringify(data)).toString('base64url');
 assert.equal((await context.request.post(base+'/api/auth/passkeys/finish',{headers:{Origin:base},data:{flowId:flow.flowId,credential:forged}})).status(),400);
 assert.equal((await context.request.post(base+'/api/auth/passkeys/finish',{headers:{Origin:base},data:{flowId:flow.flowId,credential}})).status(),400);
 // Revoke the current session through the real UI.
 await page.locator('.account-session').filter({hasText:'This session'}).getByRole('button',{name:'Sign out',exact:true}).click();await dialog('Sign out this session?').getByRole('button',{name:'Sign out',exact:true}).click();
 await page.getByRole('button',{name:'Sign in with a passkey',exact:true}).click();
 await page.getByRole('heading',{name:'Security',exact:true}).waitFor();
 await page.goto(base+`/teams/${f.team}/settings/security`);
 await page.getByRole('button',{name:'Set up authenticator',exact:true}).click();await confirmWithPasskey();
 const secret=await page.locator('.tf-manual code').innerText();await page.getByLabel('Code',{exact:true}).fill(totp(secret));await page.getByRole('button',{name:'Turn on',exact:true}).click();
 await page.getByText('Two-factor authentication is on.',{exact:true}).waitFor();const codes=await page.locator('.tf-codes code').allTextContents();assert.equal(codes.length,10);await page.getByRole('button',{name:"I've saved these codes",exact:true}).click();
 if(process.env.IGNITION_ACCOUNT_SCREENSHOTS) await page.screenshot({path:process.env.IGNITION_ACCOUNT_SCREENSHOTS+'/security-page.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});if(process.env.IGNITION_ACCOUNT_SCREENSHOTS) await page.screenshot({path:process.env.IGNITION_ACCOUNT_SCREENSHOTS+'/security-mobile.png',fullPage:true});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);await page.setViewportSize({width:1280,height:1000});
 // Passkey sign-in still honors the user's second factor.
 await page.locator('.account-session').filter({hasText:'This session'}).getByRole('button',{name:'Sign out',exact:true}).click();await dialog('Sign out this session?').getByRole('button',{name:'Sign out',exact:true}).click();await page.getByRole('button',{name:'Sign in with a passkey',exact:true}).click();
 await page.getByRole('button',{name:'Use a recovery code',exact:true}).click();await page.getByRole('textbox').fill(codes[0]);await page.getByRole('button',{name:'Verify',exact:true}).click();await page.getByRole('heading',{name:'Security',exact:true}).waitFor();
 await page.goto(base+`/teams/${f.team}/settings/profile`);await page.getByRole('button',{name:'Delete account',exact:true}).click();await dialog('Delete your account?').getByRole('textbox').fill(updatedEmail);await dialog('Delete your account?').getByRole('button',{name:'Delete account permanently'}).click();await confirmWithPasskey();await page.getByRole('heading',{name:'Sign in',exact:true}).waitFor();
 assert.deepEqual(errors,[]);console.log('PASS profile edit/reauth/persistence, SSO state, passkey registration/login/reauth, wrong-origin and replay rejection, TOTP/recovery login, sessions, deletion, responsive layout; no JS errors');
}finally{await browser.close();}
