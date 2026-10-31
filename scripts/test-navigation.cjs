// SPDX-License-Identifier: AGPL-3.0-only
// Run with: node --test scripts/test-navigation.cjs
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {readFileSync}=require('node:fs');
const {runInContext,createContext}=require('node:vm');
const source=readFileSync(require('node:path').join(__dirname,'../internal/app/web/app.js'),'utf8');
function fixture(){
 const timers=[],node={addEventListener(){},classList:{toggle(){}},close(){}};
 const context=createContext({URLSearchParams,console,Date:class extends Date { static now(){return 1793400000000;} },localStorage:{getItem(){return null;}},location:{hash:'#instances',search:''},window:{addEventListener(){}},document:{addEventListener(){},querySelector(){return node;},querySelectorAll(){return [];}},fetch:()=>new Promise(()=>{}),setTimeout:fn=>timers.push(fn),clearTimeout(){}});
 runInContext(source,context);
 const run=code=>runInContext(code,context);
 run("user={id:1,role:'admin'};cloudAccounts=[{id:1,provider:'alibaba',name:'Test',regions:['cn-hangzhou']}];selectedProvider='alibaba';selectedAccountIDs=[1];selectedAccount=cloudAccounts[0];edsRegion='cn-hangzhou';edsRegionsAccountID=1;edsRegions=[{id:'cn-hangzhou',available:true,desktops:1}];");
 return {run,timers,context};
}
test('five-minute boundary, missing and invalid timestamps',()=>{
 const {run}=fixture();
 assert.equal(run('snapshotStale(new Date(Date.now()-snapshotMaxAge).toISOString())'),false);
 assert.equal(run('snapshotStale(new Date(Date.now()-snapshotMaxAge-1000).toISOString())'),true);
 for(const value of ['null',"'invalid'",'undefined'])assert.equal(run(`snapshotStale(${value})`),true);
});
for(const [view,loader] of Object.entries({instances:'inventory',eds:'edsDesktopsPage',edsusers:'edsUsersPage',oss:'ossPage',securitygroups:'securityGroupsPage'})){
 test(`${view}: fresh navigation does not POST; stale navigation does`,async()=>{
  const {run,timers}=fixture();
  run(`page='${view}';location.hash='#${view}';globalThis.posts=[];api=async()=>({data:[],fetched_at:new Date(Date.now()).toISOString()});startScopedRefresh=async(scope,path)=>posts.push(path);`);
  await run(`${loader}()`);assert.equal(timers.length,0);
  run('api=async()=>({data:[],fetched_at:new Date(Date.now()-snapshotMaxAge-1000).toISOString()})');
  await run(`${loader}()`);for(const fn of timers.splice(0))await fn();
  assert.equal(run('posts.length'),1);
 });
 test(`${view}: refresh button bypasses freshness and does not render`,async()=>{
  const {run}=fixture(),action={instances:'refresh-inventory',eds:'refresh-eds',edsusers:'refresh-eds',oss:'refresh-oss',securitygroups:'refresh-security-groups'}[view];
  run(`page='${view}';location.hash='#${view}';globalThis.posts=[];startScopedRefresh=async(scope,path)=>posts.push(path);render=()=>{throw Error('full render');};`);
  await run(`performAction('${action}',null,{dataset:{}})`);
  assert.equal(run('posts.length'),1);
 });
}
test('rapid leave/return and repeated refresh requests share one in-flight job',async()=>{
 const {run,timers}=fixture();
 run("globalThis.posts=0;globalThis.patches=0;api=async(path,options={})=>{if(options.method==='POST'){posts++;return {id:'job',status:'running'};}return {id:'job',status:'completed'};};updateRefreshStatus=()=>{};updateSnapshotRows=async()=>patches++;");
 const running=run("startInventoryRefresh(inventoryScope(),[1])");await Promise.resolve();
 run("page='oss';location.hash='#oss'");
 await run("startInventoryRefresh('alibaba:1',[1])");
 run("page='instances';location.hash='#instances'");
 await run("startInventoryRefresh(inventoryScope(),[1])");
 assert.equal(run('posts'),1);
 timers.shift()();await running;assert.equal(run('patches'),1);
});
test('a completed job for a departed scope cannot alter the screen',async()=>{
 const {run,timers}=fixture();
 run("globalThis.release=null;globalThis.patches=0;globalThis.statuses=0;api=async(path,options={})=>({id:'job',status:options.method==='POST'?'running':'completed'});updateRefreshStatus=()=>statuses++;updateSnapshotRows=async()=>patches++;");
 const running=run("startInventoryRefresh(inventoryScope(),[1])");await Promise.resolve();
 const before=run('statuses');run("selectedAccountIDs=[]");timers.shift()();await running;
 assert.equal(run('patches'),0);assert.equal(run('statuses'),before);
});
test('late list responses do not overwrite another page records',async()=>{
 for(const loader of ['inventory','edsDesktopsPage','edsUsersPage','ossPage','securityGroupsPage']){
  const {run}=fixture();
  run("globalThis.resolveLists=[];api=()=>new Promise(resolve=>resolveLists.push(resolve));records=['keep'];ossBuckets=['keep'];securityGroups=['keep'];");
  const pending=run(`${loader}()`);await Promise.resolve();await Promise.resolve();
  run("page='activity';renderID++;resolveLists.forEach(resolve=>resolve({data:['late'],fetched_at:new Date(Date.now()).toISOString()}));");
  await pending;
  assert.equal(run('records[0]'),'keep');assert.equal(run('ossBuckets[0]'),'keep');assert.equal(run('securityGroups[0]'),'keep');
 }
});
test('queued automatic refresh is discarded if the scope changes before it starts',async()=>{
 const {run,timers}=fixture();run("globalThis.posts=0;queueSnapshotRefresh(null,false,()=>posts++);selectedAccountIDs=[];");
 for(const fn of timers)await fn();assert.equal(run('posts'),0);
});

test('snapshot completion discards a list response after navigation',async()=>{
 const {run}=fixture();
 run("globalThis.release=null;inventory=()=>new Promise(resolve=>release=resolve);document.createElement=()=>{throw Error('must not touch the DOM');};");
 const pending=run('updateSnapshotRows()');
 run("page='oss';renderID++;release('<div data-snapshot-rows>late rows</div>');");
 await pending;
});

test('post-action inventory and EDS refreshes bypass snapshot age',async()=>{
 for(const [action,flag] of [['start','inventoryRefreshRequested'],['eds-stop','edsRefreshRequested.eds']]){
  const {run,context}=fixture();context.confirm=()=>true;
  run("api=async()=>({});render=async()=>{};toast=()=>{};");
  await run(`performAction('${action}',1,{dataset:{region:'cn-hangzhou'}})`);
  assert.equal(run(flag),true);
 }
});
test('deleting a security-group rule immediately queues refresh and keeps the dialog',async()=>{
 const {run,timers,context}=fixture();context.confirm=()=>true;
 run("page='securitygroups';location.hash='#securitygroups';globalThis.posts=0;globalThis.dialogs=0;api=async()=>({});toast=()=>{};securityGroupRulesForm=()=>dialogs++;startScopedRefresh=async()=>posts++;");
 await run("performAction('security-group-rule-delete','rule-1',{dataset:{group:'sg-1',region:'cn-hangzhou',direction:'ingress'}})");
 for(const fn of timers)await fn();
 assert.equal(run('posts'),1);assert.equal(run('dialogs'),1);
});
