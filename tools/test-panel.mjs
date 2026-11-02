// Real DOM checks in an isolated headless browser. No external packages,
// app process, real account data or production RPC handlers are involved.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {existsSync} from 'node:fs';
import {readFile,writeFile,mkdir,mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {resolve,join,dirname,sep,basename} from 'node:path';
import {fileURLToPath,pathToFileURL} from 'node:url';
import {setTimeout as delay} from 'node:timers/promises';

const root = resolve(dirname(fileURLToPath(import.meta.url)),'..');
const arg = process.argv.indexOf('--out');
const out = resolve(arg >= 0 ? process.argv[arg+1] : join(root,'dist','maintenance','quota-panel-ui'));
await mkdir(out,{recursive:true});
const now = new Date().toISOString();
const reset = new Date(Date.now()+48*3600000).toISOString();
const makeAccount = (id,name,current,values,status,tone) => ({
  id,name,plan:'Max',current,fresh:true,status,tone,problem:'',checkedAt:now,
  reset:current ? 'Fable周 · 周二20:00重置' : '总周 · 周五20:00重置',
  meters:values.map((remaining,i) => ({name:['5小时','总周','Fable周'][i],remaining,
    tone:remaining===0?'empty':remaining<=25?'low':'healthy',resetsAt:reset,...(i===2?{allowance:50}:{})}))
});
const view = {language:'zh-CN',active:'alpha',busy:false,status:'',note:'',autoSwitch:false,spendWeekly:false,threshold:96,autostart:false,updatedAt:now,
  accounts:[makeAccount('alpha','Account Alpha',true,[58,55,18],'Fable周余量偏低','low'),
    makeAccount('beta','Account Beta',false,[83,72,64],'余量充足','healthy'),
    makeAccount('gamma','Account Gamma',false,[0,64,46],'5小时已用尽','empty')]};
view.accounts[2].reset='5小时 · 1小时20分后重置';
let html = await readFile(join(root,'panel.html'),'utf8');
const bridge = `window.__calls=[];window.__hidden=false;window.panelFit=async h=>{window.__fit=h};window.panelHide=async()=>{window.__hidden=true};window.panelSend=async a=>{window.__calls.push(a)};`;
const safeJSON = JSON.stringify(view).replaceAll('<','\\u003c').replaceAll('>','\\u003e').replaceAll('&','\\u0026');
html = html.replace('/*PANEL_DATA*/null',safeJSON)
  .replace('/*PANEL_CSS*/',await readFile(join(root,'panel.css'),'utf8'))
  .replace('/*PANEL_JS*/',bridge+'\n'+await readFile(join(root,'panel.js'),'utf8'));
const fixture = join(out,'fixture.html');
await writeFile(fixture,html);
const browser = [process.env.PANEL_BROWSER,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome','/usr/bin/chromium','/usr/bin/google-chrome']
  .find(p => p && existsSync(p));
if (!browser) throw new Error('Set PANEL_BROWSER to a Chromium/Edge executable');
const profile = await mkdtemp(join(tmpdir(),'claude-quota-ui-'));
const child = spawn(browser,['--headless=new','--disable-gpu','--no-first-run','--no-default-browser-check',
  '--disable-background-networking','--disable-component-update','--disable-sync','--disable-extensions',
  '--remote-debugging-address=127.0.0.1','--remote-debugging-port=0',`--user-data-dir=${profile}`,'about:blank'],
  {stdio:'ignore',windowsHide:true});
let launchError;
child.on('error',error => { launchError=error; });
let socket;
const pending = new Map();
const errors = [];
const checks = [];
let sequence = 0;
let session;
const send = (method,params={},withSession=true) => new Promise((ok,fail) => {
  const id=++sequence;
  const timer=setTimeout(()=>{pending.delete(id);fail(new Error(`CDP timeout: ${method}`));},20000);
  pending.set(id,{ok:result=>{clearTimeout(timer);ok(result)},fail:error=>{clearTimeout(timer);fail(error)}});
  socket.send(JSON.stringify({id,method,params,...(withSession&&session?{sessionId:session}:{})}));
});
const evaluate = async expression => {
  const result=await send('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});
  if(result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
  return result.result.value;
};
const check = async (name,expression) => { assert.equal(await evaluate(expression),true,name); checks.push(name); };
const setView = async next => evaluate(`window.panelReceive({view:${JSON.stringify(next)}});true`);
const stable = () => evaluate(`Promise.allSettled(document.getAnimations().map(a=>a.finished)).then(()=>document.fonts.ready).then(()=>true)`);
const screenshot = async name => {
  await stable();
  const result=await send('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});
  await writeFile(join(out,name),Buffer.from(result.data,'base64'));
};

try {
  let port;
  for(let i=0;i<150;i++) {
    if(launchError) throw launchError;
    try {port=(await readFile(join(profile,'DevToolsActivePort'),'utf8')).split('\n')[0]; if(port)break;}catch{}
    await delay(100);
  }
  if(!port) throw new Error('Headless browser did not become ready');
  const targets=await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  socket = new WebSocket(targets.find(t=>t.type==='page').webSocketDebuggerUrl);
  await new Promise((ok,fail)=>{socket.addEventListener('open',ok,{once:true});socket.addEventListener('error',fail,{once:true})});
  socket.addEventListener('message',event=>{
    const message=JSON.parse(event.data);
    if(message.id && pending.has(message.id)) {
      const item=pending.get(message.id);pending.delete(message.id);
      if(message.error)item.fail(new Error(message.error.message));else item.ok(message.result);
    }
    if(message.method==='Runtime.exceptionThrown')errors.push(message.params.exceptionDetails.exception?.description||message.params.exceptionDetails.text);
  });
  await send('Runtime.enable');await send('Page.enable');
  await send('Emulation.setDeviceMetricsOverride',{width:424,height:720,deviceScaleFactor:2,mobile:false});
  await send('Page.navigate',{url:pathToFileURL(fixture).href});
  let ready=false;
  for(let i=0;i<100;i++){if(await evaluate(`typeof window.panelReceive==='function'`)){ready=true;break;}await delay(50)}
  assert.ok(ready,'page loaded');
  await check('three accounts and nine independent meters',`document.querySelectorAll('.account').length===3 && document.querySelectorAll('.meter').length===9`);
  await check('remaining values and real exhaustion',`document.querySelector('.value').textContent==='58%' && [...document.querySelectorAll('.value')][6].textContent==='0%'`);
  await check('readable, aligned, no horizontal overflow',`document.documentElement.scrollWidth<=innerWidth && [...document.querySelectorAll('.track')].every(n=>Math.abs(n.getBoundingClientRect().left-document.querySelector('.track').getBoundingClientRect().left)<1)`);
  await screenshot('quota-panel.png');
  await check('card click opens details without sending an action',`(()=>{document.querySelector('.account').click();return !!document.querySelector('.details')&&window.__calls.length===0})()`);
  await check('canceling a switch leaves accounts untouched',`(()=>{document.querySelectorAll('.switch-account')[0].click();const shown=!!document.querySelector('.confirm');document.querySelector('.cancel-confirm').click();return shown&&!document.querySelector('.confirm')&&window.__calls.length===0})()`);
  await evaluate(`document.querySelectorAll('.switch-account')[0].click()`);
  await screenshot('quota-panel-confirm.png');
  await check('only explicit confirmation sends one switch command',`(()=>{const b=document.querySelector('.confirm-action');b.click();b.click();return window.__calls.length===1&&window.__calls[0].type==='switch'&&window.__calls[0].account==='beta'&&window.__calls[0].confirmed===true&&[...document.querySelectorAll('.switch-account')].every(n=>n.disabled)})()`);
  await evaluate(`window.panelReceive({requestId:window.__calls[0].requestId,error:'模拟操作被拒绝'});true`);
  await check('operation rejection stays visible and re-enables controls',`document.getElementById('action-error').textContent==='模拟操作被拒绝'&&!document.querySelector('.switch-account').disabled`);
  await evaluate(`window.panelReceive({show:true});document.getElementById('settings').click()`);
  await check('management uses existing settings',`!!document.getElementById('setting-auto')&&document.getElementById('setting-threshold').value==='96'`);
  await evaluate(`document.getElementById('setting-auto').focus();document.getElementById('setting-auto').click()`);
  await check('settings action is explicit and cannot repeat while pending',`window.__calls.length===2&&window.__calls[1].type==='auto'&&window.__calls[1].enabled===true&&document.getElementById('setting-auto').disabled`);
  const changed=structuredClone(view);changed.autoSwitch=true;
  await evaluate(`window.panelReceive({requestId:window.__calls[1].requestId,view:${JSON.stringify(changed)}})`);
  await check('saved setting is read back from the parent',`document.getElementById('setting-auto').checked===true&&!document.getElementById('setting-auto').disabled`);
  await evaluate(`document.querySelector('.settings-actions button').click()`);
  await check('adding an account also requires confirmation',`!!document.querySelector('.confirm')&&window.__calls.length===2`);
  await evaluate(`document.querySelector('.cancel-confirm').click();window.panelReceive({show:true,view:${JSON.stringify(view)}})`);
  const stale=structuredClone(view);stale.accounts[0].fresh=false;stale.accounts[0].tone='unknown';stale.accounts[0].status='用量暂未更新';stale.accounts[0].problem='用量查询受限，等待重试';stale.accounts[0].meters.forEach(m=>m.tone='unknown');
  await setView(stale);
  await check('failed query retains grey last-known data',`document.querySelector('.account').textContent.includes('用量暂未更新')&&document.querySelector('.account .value').textContent==='58%'&&!document.querySelector('.account .meter.low')`);
  await check('footer never describes a partial failure as freshly updated',`document.getElementById('updated').textContent==='部分账号用量待更新'`);
  await screenshot('quota-panel-stale.png');
  const unknown=structuredClone(view);unknown.accounts[0].meters=unknown.accounts[0].meters.slice(0,2).map(m=>({...m,remaining:null,tone:'unknown'}));unknown.accounts[0].fresh=false;unknown.accounts[0].tone='unknown';unknown.accounts[0].status='等待用量更新';
  await setView(unknown);
  await check('missing data is not shown as zero or a fabricated model cap',`document.querySelector('.account .value').textContent==='—'&&document.querySelector('.account').querySelectorAll('.meter').length===2`);
  const malicious=structuredClone(view);malicious.accounts[0].name='</script><img src=x onerror="window.panelInjected=true">';malicious.accounts[0].meters[2].name='<b>Fable</b>';
  await setView(malicious);
  await check('account and model strings cannot inject markup',`!document.querySelector('img')&&!window.panelInjected&&document.querySelector('h2').textContent.startsWith('</script>')`);
  const many=structuredClone(view);many.accounts=Array.from({length:8},(_,i)=>({...structuredClone(view.accounts[i%3]),id:'test-'+i,current:i===0,name:'Very long synthetic account name '+i,status:'非常长的模型额度提示'.repeat(8)}));
  await setView(many);
  await send('Emulation.setDeviceMetricsOverride',{width:350,height:450,deviceScaleFactor:1,mobile:false});
  await check('many accounts and long names stay inside a short work area',`document.documentElement.scrollWidth<=innerWidth&&document.querySelector('.panel-footer').getBoundingClientRect().bottom<=innerHeight+1&&document.getElementById('content').scrollHeight>document.getElementById('content').clientHeight`);
  const english=structuredClone(view);english.language='en';english.accounts.forEach(a=>{a.status='Room available';a.meters.forEach((m,i)=>m.name=['5h','Total week','Fable week'][i])});
  await setView(english);
  await check('English interface and remaining labels',`document.documentElement.lang==='en'&&document.getElementById('title').textContent==='Account quotas'&&document.getElementById('subtitle').textContent.startsWith('Remaining')`);
  const empty=structuredClone(view);empty.accounts=[];
  await setView(empty);
  await check('empty state has no automatic login action',`!!document.querySelector('.empty-state')&&window.__calls.length===2`);
  await setView(view);
  await send('Emulation.setDeviceMetricsOverride',{width:424,height:720,deviceScaleFactor:2,mobile:false});
  await evaluate(`window.panelShown();document.getElementById('close').click()`);
  await check('close only hides the quota window',`window.__hidden===true&&window.__calls.length===2`);
  assert.deepEqual(errors,[],'browser script errors');
  await writeFile(join(out,'results.json'),JSON.stringify({passed:checks.length,checks,browserErrors:errors,realAccountRequests:0,productionActions:0},null,2));
  console.log(JSON.stringify({passed:checks.length,screenshots:out,realAccountRequests:0,productionActions:0}));
} finally {
  if(socket?.readyState===WebSocket.OPEN){try{await send('Browser.close',{},false)}catch{}socket.close()}
  if(child.exitCode===null){await Promise.race([new Promise(ok=>child.once('exit',ok)),delay(3000)]);if(child.exitCode===null)child.kill()}
  // This is only the profile created above, never any user's browser profile.
  const actual=resolve(profile),temporary=resolve(tmpdir());
  if(actual.startsWith(temporary+sep)&&basename(actual).startsWith('claude-quota-ui-')){
    await rm(actual,{recursive:true,force:true,maxRetries:3,retryDelay:200}).catch(()=>{});
  }
}
