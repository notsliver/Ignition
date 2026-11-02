import {chromium} from 'playwright';
import {readFileSync} from 'node:fs';
import assert from 'node:assert/strict';
// Disposable instance only: changes provider applications and the global source policy.
// Fixture: adminSession, userSession, team slug, teamId UUID, password.
if (!process.env.IGNITION_TEST_INTEGRATIONS_FIXTURE) throw new Error('IGNITION_TEST_INTEGRATIONS_FIXTURE must name disposable integration fixtures');
const f=JSON.parse(readFileSync(process.env.IGNITION_TEST_INTEGRATIONS_FIXTURE,'utf8'));
const base=process.env.IGNITION_URL || 'http://localhost:55465';
const browser=await chromium.launch({executablePath:process.env.IGNITION_BROWSER_EXECUTABLE || undefined,headless:true});
const admin=await browser.newContext({viewport:{width:1280,height:1000},reducedMotion:'reduce'});await admin.addCookies([{name:'ignition_session',value:f.adminSession,url:base}]);
const page=await admin.newPage();page.setDefaultTimeout(12000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
try{
 await page.goto(base+`/teams/${f.team}/admin/integrations`);
 await page.getByRole('heading',{name:'Integrations',exact:true}).waitFor();assert.equal(await page.locator('.integration-provider-row').count(),8);
 const github=page.locator('.integration-provider-row').filter({has:page.getByText('GitHub',{exact:true})});await github.getByRole('button',{name:'Configure',exact:true}).click();
 const d=page.getByRole('dialog',{name:'Configure GitHub',exact:true});
 await d.getByRole('switch',{name:'Allow account sign-in and linking'}).click();await d.getByRole('switch',{name:'Allow teams to connect repositories via OAuth'}).click();
 await d.getByLabel('Sign-in client ID',{exact:true}).fill('qa-login-client');await d.getByLabel('Sign-in client secret',{exact:true}).fill('qa-login-secret');await d.getByLabel('Repository client ID',{exact:true}).fill('qa-source-client');await d.getByLabel('Repository client secret',{exact:true}).fill('qa-source-secret');
 assert.equal(await d.getByRole('checkbox',{name:'Include private repositories (GitHub repo grants read and write)'}).isChecked(),false);
 await d.getByRole('button',{name:'Save provider',exact:true}).click();
 const confirm=page.getByRole('dialog',{name:'Confirm your identity',exact:true});await confirm.getByLabel('Current password').fill(f.password);await confirm.getByRole('button',{name:'Confirm',exact:true}).click();await d.waitFor({state:'hidden'});
 await page.getByRole('button',{name:'Connection mode',exact:true}).click();await page.getByRole('option',{name:'Hosting · instance OAuth connections',exact:true}).click();await page.getByRole('button',{name:'Save connection mode',exact:true}).click();await page.getByText('Integration settings saved.',{exact:true}).waitFor();
 const settings=await (await admin.request.get(base+'/api/settings/integrations')).text();assert.ok(!settings.includes('qa-login-secret')&&!settings.includes('qa-source-secret'));
 assert.equal(JSON.parse(settings).sourceMode,'managed');
 if(process.env.IGNITION_INTEGRATIONS_SCREENSHOTS) await page.screenshot({path:process.env.IGNITION_INTEGRATIONS_SCREENSHOTS+'/integrations-admin.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);if(process.env.IGNITION_INTEGRATIONS_SCREENSHOTS) await page.screenshot({path:process.env.IGNITION_INTEGRATIONS_SCREENSHOTS+'/integrations-admin-mobile.png',fullPage:true});
 const user=await browser.newContext({viewport:{width:1280,height:1000},reducedMotion:'reduce'});await user.addCookies([{name:'ignition_session',value:f.userSession,url:base}]);await user.route('https://github.com/**',route=>route.fulfill({status:200,contentType:'text/html',body:'<html><p>Mock provider authorization screen</p></html>'}));
 const customer=await user.newPage();customer.setDefaultTimeout(12000);customer.on('pageerror',e=>errors.push(e.message));
 await customer.goto(base+`/teams/${f.team}/settings/profile`);await customer.getByRole('heading',{name:'Account Integrations',exact:true}).waitFor();
 assert.equal(await customer.locator('.sidebar').getByRole('button',{name:'Team Sources',exact:true}).count(),0);assert.equal((await user.request.get(base+'/api/settings/integrations')).status(),403);
 if(process.env.IGNITION_INTEGRATIONS_SCREENSHOTS) await customer.screenshot({path:process.env.IGNITION_INTEGRATIONS_SCREENSHOTS+'/integrations-customer.png',fullPage:true});
 await customer.goto(base+`/teams/${f.team}/settings/team-sources`);await customer.getByRole('heading',{name:'Profile',exact:true}).waitFor();assert.ok(customer.url().endsWith('/settings/profile'));
 const denied=await user.request.post(base+`/api/teams/${f.teamId}/sources`,{headers:{Origin:base},data:{name:'bypass',provider:'dockerhub',authType:'none'}});assert.equal(denied.status(),403);
 await customer.goto(base+`/teams/${f.team}/projects`);await customer.getByRole('button',{name:'New project',exact:true}).click();await customer.getByRole('menuitem',{name:'Import from Git repository',exact:true}).hover();await customer.getByRole('menuitem',{name:'Continue with GitHub',exact:true}).click();
 const draft=customer.getByPlaceholder('owner/repository',{exact:true});await draft.fill('team/keep-this-draft');await customer.getByRole('button',{name:'Connect Git provider',exact:true}).click();const connect=customer.getByRole('dialog',{name:'Connect a Git provider',exact:true});
 const popupPromise=user.waitForEvent('page');await connect.getByRole('button',{name:'Connect GitHub',exact:true}).click();const popup=await popupPromise;await popup.waitForURL('https://github.com/**');assert.equal(await popup.evaluate(()=>window.opener===null),true);await popup.close();await connect.getByRole('button',{name:'Connect GitHub',exact:true}).waitFor();await connect.getByRole('button',{name:'Done',exact:true}).click();assert.equal(await draft.inputValue(),'team/keep-this-draft');
 assert.deepEqual(errors,[]);console.log('PASS administrator provider settings, secret protection, managed hosting policy, hidden source routes, API enforcement, OAuth popup isolation, preserved deployment drafts and mobile layout; no JS errors');
}finally{await browser.close();}
