// SPDX-License-Identifier: AGPL-3.0-only
// Run with: node --test scripts/test-navigation.cjs
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {readFileSync}=require('node:fs');
const {runInContext,createContext}=require('node:vm');
const source=readFileSync(require('node:path').join(__dirname,'../internal/app/web/app.js'),'utf8');
const flush=async()=>{for(let i=0;i<12;i++)await Promise.resolve();};
function fixture(){
 const timers=[],windowEvents={},documentEvents={},node={addEventListener(){},classList:{toggle(){}},close(){}};
 let now=1793400000000;
 const listen=events=>(name,fn)=>(events[name]??=[]).push(fn);
 const timeout=(fn,delay=0)=>{const timer=()=>{if(!timer.cancelled&&!timer.fired){timer.fired=true;return fn();}};Object.assign(timer,{at:now+delay,delay});timers.push(timer);return timer;};
 const context=createContext({URLSearchParams,console,Date:class extends Date { static now(){return now;} },localStorage:{getItem(){return null;},setItem(){}},location:{hash:'#instances',search:''},window:{addEventListener:listen(windowEvents)},document:{visibilityState:'visible',addEventListener:listen(documentEvents),querySelector(){return node;},querySelectorAll(){return [];}},fetch:()=>new Promise(()=>{}),setTimeout:timeout,clearTimeout:timer=>{if(timer)timer.cancelled=true;}});
 runInContext(source,context);
 const run=code=>runInContext(code,context);
 const advance=async milliseconds=>{
  const end=now+milliseconds;await flush();
  for(;;){const next=timers.filter(timer=>!timer.fired&&!timer.cancelled&&timer.at<=end).sort((a,b)=>a.at-b.at)[0];if(!next)break;now=next.at;next();await flush();}
  now=end;await flush();
 };
 const emit=async(surface,name,event={})=>{for(const fn of (surface==='window'?windowEvents:documentEvents)[name]||[])await fn(event);await flush();};
 run("user={id:1,role:'admin'};cloudAccounts=[{id:1,provider:'alibaba',name:'Test',regions:['cn-hangzhou']}];selectedProvider='alibaba';selectedAccountIDs=[1];selectedAccount=cloudAccounts[0];edsRegion='cn-hangzhou';edsRegionsAccountID=1;edsRegions=[{id:'cn-hangzhou',available:true,desktops:1}];");
 return {run,timers,context,advance,emit};
}
function completedRefreshes(run){
 run("globalThis.posts=[];globalThis.patches=0;api=async(path,options={})=>{if(options.method==='POST'&&path.includes('/refresh'))posts.push({path,at:Date.now()});return {id:'job',status:'completed'};};updateRefreshStatus=()=>{};updateSnapshotRows=async()=>patches++;render=async()=>{};toast=()=>{};");
}
for(const [view,loader] of Object.entries({instances:'inventory',eds:'edsDesktopsPage',edsusers:'edsUsersPage',oss:'ossPage',securitygroups:'securityGroupsPage'})){
 test(`${view}: navigation never refreshes fresh, stale, missing or invalid snapshots`,async()=>{
  const {run,advance}=fixture();
  run(`page='${view}';location.hash='#${view}';globalThis.posts=[];startScopedRefresh=async(scope,path)=>posts.push(path);`);
  for(const timestamp of ['new Date(Date.now()).toISOString()','new Date(Date.now()-360000).toISOString()','null',"'invalid'"]){
   run(`api=async()=>({data:[],fetched_at:${timestamp}})`);await run(`${loader}()`);await advance(360000);
  }
  assert.equal(run('posts.length'),0);
 });
 test(`${view}: refresh button sends one targeted POST and does not render`,async()=>{
  const {run}=fixture(),action={instances:'refresh-inventory',eds:'refresh-eds',edsusers:'refresh-eds',oss:'refresh-oss',securitygroups:'refresh-security-groups'}[view];
  completedRefreshes(run);run(`page='${view}';location.hash='#${view}';render=()=>{throw Error('full render');};`);
  await run(`performAction('${action}',null,{dataset:{}})`);
  assert.equal(run('posts.length'),1);
  const expected={instances:'/inventory/refresh?account_ids=1',eds:'/accounts/1/eds/refresh?service=desktops&region=cn-hangzhou',edsusers:'/accounts/1/eds/refresh?service=users&region=cn-hangzhou',oss:'/accounts/1/oss/refresh',securitygroups:'/accounts/1/ecs/security-groups/refresh'};
  assert.equal(run('posts[0].path'),expected[view]);
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
test('snapshot completion discards a list response after navigation',async()=>{
 const {run}=fixture();
 run("globalThis.release=null;inventory=()=>new Promise(resolve=>release=resolve);document.createElement=()=>{throw Error('must not touch the DOM');};");
 const pending=run('updateSnapshotRows()');await flush();
 run("page='oss';renderID++;release('<div data-snapshot-rows>late rows</div>');");
 await pending;
});

test('post-action inventory and EDS refreshes bypass snapshot age',async()=>{
 for(const [action,view,path] of [['start','instances','/inventory/refresh?account_ids=1'],['eds-stop','eds','/accounts/1/eds/refresh?service=desktops&region=cn-hangzhou']]){
  const {run,context}=fixture();context.confirm=()=>true;completedRefreshes(run);
  run(`page='${view}';location.hash='#${view}';`);
  await run(`performAction('${action}',1,{dataset:{region:'cn-hangzhou'}})`);await flush();
  assert.equal(run('posts.length'),1);assert.equal(run('posts[0].path'),path);
 }
});
test('deleting a security-group rule immediately queues refresh and keeps the dialog',async()=>{
 const {run,context,advance}=fixture();context.confirm=()=>true;completedRefreshes(run);
 run("page='securitygroups';location.hash='#securitygroups';globalThis.dialogs=0;securityGroupRulesForm=()=>dialogs++;");
 await run("performAction('security-group-rule-delete','rule-1',{dataset:{group:'sg-1',region:'cn-hangzhou',direction:'ingress'}})");await flush();
 assert.equal(run('posts.length'),1);assert.equal(run('dialogs'),1);
 await advance(90000);assert.equal(run('posts.length'),4);assert.equal(run('dialogs'),1);
});

for(const [view,path] of Object.entries({instances:'/inventory/refresh?account_ids=1',eds:'/accounts/1/eds/refresh?service=desktops&region=cn-hangzhou',edsusers:'/accounts/1/eds/refresh?service=users&region=cn-hangzhou',oss:'/accounts/1/oss/refresh',securitygroups:'/accounts/1/ecs/security-groups/refresh'})){
 test(`${view}: action refreshes at 0/10/30/90 seconds, then stops`,async()=>{
  const {run,advance}=fixture();completedRefreshes(run);run(`page='${view}';location.hash='#${view}';prepareActionRefresh()();`);await flush();
  assert.equal(run('posts.length'),1);
  for(const [elapsed,count] of [[9999,1],[1,2],[19999,2],[1,3],[59999,3],[1,4],[300000,4]]){await advance(elapsed);assert.equal(run('posts.length'),count);}
  assert.deepEqual(JSON.parse(run('JSON.stringify(posts.map(p=>p.at-posts[0].at))')),[0,10000,30000,90000]);
  assert.equal(run(`posts.every(p=>p.path==='${path}')`),true);
  assert.equal(run('actionRefreshes.size'),0);assert.equal(run('pendingRefreshes.size'),0);
 });
}
test('action requests during a queued/running refresh coalesce into exactly one extra job',async()=>{
 const {run,advance}=fixture();
 run("globalThis.posts=0;globalThis.resolvePost=null;globalThis.done=false;api=async(path,options={})=>{if(options.method==='POST'){posts++;if(posts===1)return new Promise(resolve=>resolvePost=resolve);return {id:'second',status:'completed'};}return {id:'first',status:done?'completed':'running'};};updateRefreshStatus=()=>{};updateSnapshotRows=async()=>{};");
 const first=run('startInventoryRefresh(inventoryScope(),[1])');await flush();
 run('prepareActionRefresh()();');await advance(10000);assert.equal(run('posts'),1);assert.equal(run('pendingRefreshes.size'),1);
 run("resolvePost({id:'first',status:'running'})");await advance(80000);assert.equal(run('posts'),1);
 run('done=true');await advance(900);await first;await flush();
 assert.equal(run('posts'),2);assert.equal(run('pendingRefreshes.size'),0);
 await advance(300000);assert.equal(run('posts'),2);assert.equal(run('refreshInFlight.size'),0);
});
test('hash navigation cancels timers and pending work, including a quick return',async()=>{
 const {run,advance,emit}=fixture();
 run("globalThis.posts=0;api=async(path,options={})=>{if(options.method==='POST'){posts++;return {id:'job',status:'running'};}return {id:'job',status:'completed'};};updateRefreshStatus=()=>{};updateSnapshotRows=async()=>{};render=async()=>{};");
 const first=run('startInventoryRefresh(inventoryScope(),[1])');await flush();run('prepareActionRefresh()();');
 run("location.hash='#oss';page='oss'");await emit('window','hashchange');
 run("location.hash='#instances';page='instances'");await emit('window','hashchange');
 await advance(300000);await first;assert.equal(run('posts'),1);assert.equal(run('pendingRefreshes.size'),0);assert.equal(run('actionRefreshes.size'),0);
});
test('provider, account, select-all and EDS-region changes cancel follow-ups',async()=>{
 for(const change of ['provider','account','all','region']){
  const {run,context,advance,emit}=fixture();completedRefreshes(run);
  run("page='eds';location.hash='#eds';persistCloudScope=()=>{};syncCloudSidebar=()=>{};prepareActionRefresh()();");await flush();
  if(change==='provider')await emit('document','change',{target:{name:'cloud-provider',value:'aws'}});
  if(change==='account')await emit('document','change',{target:{type:'checkbox',closest:selector=>selector==='#account-selector'}});
  if(change==='all')await run("performAction('toggle-provider-accounts')");
  if(change==='region')await run("selectEDSRegion('cn-shanghai')");
  // Returning to the original selection must not revive an old schedule.
  run("selectedProvider='alibaba';selectedAccountIDs=[1];selectedAccount=cloudAccounts[0];edsRegion='cn-hangzhou';");
  await advance(300000);assert.equal(run('posts.length'),1,change);assert.equal(run('actionRefreshes.size'),0,change);
 }
});
test('a mutation that completes after navigation cannot schedule refreshes for either page',async()=>{
 const {run,context,emit,advance}=fixture();context.confirm=()=>true;completedRefreshes(run);
 run("globalThis.finishMutation=null;api=async(path)=>{if(path.endsWith('/actions'))return new Promise(resolve=>finishMutation=resolve);posts.push(path);return {id:'job',status:'completed'};};");
 const action=run("performAction('start',1,{dataset:{}})");await flush();
 run("location.hash='#oss';page='oss'");await emit('window','hashchange');
 run('finishMutation({})');await action;await advance(300000);assert.equal(run('posts.length'),0);
});
test('same-page rendering preserves follow-ups and a newer action replaces older timers',async()=>{
 const {run,advance}=fixture();completedRefreshes(run);run('prepareActionRefresh()();');await advance(5000);
 run('renderID++;prepareActionRefresh()();');await advance(300000);
 assert.deepEqual(JSON.parse(run('JSON.stringify(posts.map(p=>p.at-posts[0].at))')),[0,5000,15000,35000,95000]);
});
test('failed mutations do not schedule refreshes',async()=>{
 const {run,context,advance}=fixture();context.confirm=()=>true;completedRefreshes(run);
 run("api=async()=>{throw Error('mutation failed');};");
 await assert.rejects(run("performAction('start',1,{dataset:{}})"),/mutation failed/);
 await advance(300000);assert.equal(run('actionRefreshes.size'),0);assert.equal(run('posts.length'),0);
});
for(const [formID,view,path] of [
 ['eds-desktop-form','eds','/accounts/1/eds/refresh?service=desktops&region=cn-hangzhou'],
 ['eds-user-create-form','edsusers','/accounts/1/eds/refresh?service=users&region=cn-hangzhou'],
 ['oss-create-form','oss','/accounts/1/oss/refresh'],
 ['security-group-rule-form','securitygroups','/accounts/1/ecs/security-groups/refresh'],
]){
 test(`${formID}: successful creation schedules follow-ups only for its originating scope`,async()=>{
  const {run,context,emit,advance}=fixture();completedRefreshes(run);
  context.FormData=class { constructor(form){return new URLSearchParams(form.fields);} };
  run(`page='${view}';location.hash='#${view}';`);
  await emit('document','submit',{preventDefault(){},target:{id:formID,dataset:{groupId:'sg-1',region:'cn-hangzhou'},fields:{bundle_id:'bundle-1'},querySelector(){return null;}}});
  assert.equal(run('posts.length'),1);await advance(300000);
  assert.equal(run('posts.length'),4);assert.equal(run(`posts.every(p=>p.path==='${path}')`),true);
 });
}
test('logout cancels the remaining action refreshes',async()=>{
 const {run,advance}=fixture();completedRefreshes(run);run('prepareActionRefresh()();');await flush();
 run('showLogin()');await advance(300000);
 assert.equal(run('posts.length'),1);assert.equal(run('actionRefreshes.size'),0);assert.equal(run('pendingRefreshes.size'),0);
});

for(const view of ['instances','eds','edsusers','oss','securitygroups']){
 test(`${view}: hash navigation and reload only read stored data`,async()=>{
  const {run,emit,advance}=fixture();
  run("globalThis.posts=[];api=async(path,options={})=>{if(options.method==='POST')posts.push(path);return {data:[],instances:0,running:0,accounts:1,regions:0};};updateSnapshotRows=async()=>{};");
  run(`location.hash='#${view}'`);await emit('window','hashchange');await advance(360000);
  await run("performAction('reload')");await advance(30000);
  assert.equal(run('posts.length'),0);
 });
}

test('provider, account, region and service switches do not POST',async()=>{
 const {run,emit,advance}=fixture();
 run("globalThis.posts=[];api=async(path,options={})=>{if(options.method==='POST')posts.push(path);return {data:[]};};updateSnapshotRows=async()=>{};syncCloudSidebar=()=>{};persistCloudScope=()=>{};");
 await emit('document','change',{target:{name:'cloud-provider',value:'aws'}});
 await emit('document','change',{target:{type:'checkbox',closest:s=>s==='#account-selector'}});
 run("selectedProvider='alibaba';selectedAccountIDs=[1];selectedAccount=cloudAccounts[0];page='eds';location.hash='#eds'");
 await run("selectEDSRegion('ap-southeast-1')");
 run("location.hash='#oss'");await emit('window','hashchange');await advance(360000);
 assert.equal(run('posts.length'),0);
});

test('manual compute refresh targets selected accounts and region exactly once',async()=>{
 const {run}=fixture();
 run("region='cn-hangzhou';globalThis.posts=[];startScopedRefresh=async(scope,path)=>posts.push(path);render=()=>{throw Error('full render');}");
 await run("performAction('refresh-inventory')");
 assert.equal(run('posts.length'),1);assert.equal(run('posts[0]'),'/inventory/refresh?account_ids=1&region=cn-hangzhou');
});

test('visible polling patches changed data, keeps unchanged data, and pauses while hidden',async()=>{
 const {run,emit,advance}=fixture();
 run("globalThis.gets=0;globalThis.patches=0;globalThis.posts=0;globalThis.success=new Date(Date.now()-120000).toISOString();globalThis.jobStatus='idle';api=async(path,options={})=>{gets++;if(options.method==='POST')posts++;return {data:[{account_id:1,service:'compute',region:'cn-hangzhou',last_success_at:success,job_status:jobStatus,last_error:'',last_error_code:''}]};};updateRefreshStatus=()=>{};updateSnapshotRows=async()=>patches++;render=()=>{throw Error('full render');};startSyncPolling();");
 await flush();assert.equal(run('gets'),1);assert.equal(run('patches'),1);
 await advance(29999);assert.equal(run('gets'),1);
 await advance(1);assert.equal(run('gets'),2);assert.equal(run('patches'),1);
 run("jobStatus='running'");await advance(30000);assert.equal(run('patches'),2);
 assert.match(run("inventoryRefreshMarkup(inventoryScope())"),/Syncing…/);
 run("jobStatus='idle';success=new Date(Date.now()).toISOString()");await advance(30000);assert.equal(run('patches'),3);
 assert.doesNotMatch(run("inventoryRefreshMarkup(inventoryScope())"),/Syncing…/);
 run("document.visibilityState='hidden'");await emit('document','visibilitychange');await advance(120000);assert.equal(run('gets'),4);
 run("document.visibilityState='visible'");await emit('document','visibilitychange');assert.equal(run('gets'),5);assert.equal(run('posts'),0);
 run('showLogin()');await advance(120000);assert.equal(run('gets'),5);
});

test('late sync-state responses cannot patch a departed scope',async()=>{
 const {run,emit,advance}=fixture();
 run("globalThis.release=null;globalThis.patches=0;api=()=>new Promise(resolve=>release=resolve);updateSnapshotRows=async()=>patches++;updateRefreshStatus=()=>{throw Error('stale status');};startSyncPolling();");
 run("page='oss';selectedAccountIDs=[];release({data:[]})");await flush();assert.equal(run('patches'),0);assert.equal(run('syncStates.size'),0);
 run("document.visibilityState='hidden'");await emit('document','visibilitychange');await advance(60000);assert.equal(run('patches'),0);
});

test('sync status labels accounts and regions, escapes errors and shows relative success',async()=>{
 const {run}=fixture();
 run("syncStates.set(syncSelection().key,{rows:[{account_id:1,region:'cn-hangzhou',last_success_at:new Date(Date.now()-120000).toISOString(),last_error:'Permission denied <script>',job_status:'queued'}]})");
 const html=run('inventoryRefreshMarkup(inventoryScope())');
 assert.match(html,/Last synced 2m ago/);assert.match(html,/Test \/ cn-hangzhou/);assert.match(html,/Permission denied &lt;script&gt;/);assert.match(html,/Syncing…/);
});

test('EDS users poll desktop assignment completions and normalize the directory scope',async()=>{
 const {run,advance}=fixture();
 run("page='edsusers';location.hash='#edsusers';edsRegion='cn-hongkong';globalThis.paths=[];globalThis.patches=0;globalThis.desktopSuccess=null;api=async path=>{paths.push(path);return {data:[{account_id:1,service:'eds.users',region:'ap-southeast-1',last_success_at:null,job_status:'idle'},{account_id:1,service:'eds.desktops',region:'cn-hangzhou',last_success_at:desktopSuccess,job_status:'idle'}]};};updateSnapshotRows=async()=>patches++;updateRefreshStatus=()=>{};startSyncPolling();");
 await flush();assert.equal(run('syncStates.get(syncSelection().key).rows.length'),1);assert.equal(run('patches'),1);
 run('desktopSuccess=new Date(Date.now()).toISOString()');await advance(30000);assert.equal(run('patches'),2);assert.doesNotMatch(run('paths[0]'),/service=/);
});

test('row patches keep unchanged nodes and update, reorder and remove changed rows',()=>{
 const {run,context}=fixture();
 class Row {
  constructor(key,value){this.dataset={rowKey:key};this.outerHTML=value;this.parent=null;}
  remove(){if(this.parent){this.parent.children.splice(this.parent.children.indexOf(this),1);this.parent=null;}}
 }
 class Parent {
  constructor(rows){this.children=rows;rows.forEach(r=>r.parent=this);}
  insertBefore(row,before){row.remove();const index=before?this.children.indexOf(before):this.children.length;this.children.splice(index,0,row);row.parent=this;}
  get lastElementChild(){return this.children.at(-1);}
 }
 const keep=new Row('a','unchanged'),changed=new Row('b','old'),removed=new Row('c','removed');
 const target=new Parent([keep,changed,removed]);const replacement=new Row('b','new');const source=new Parent([replacement,new Row('a','unchanged')]);
 context.tree={target,source};run('patchResourceRows(tree.target,tree.source)');
 assert.equal(target.children.length,2);assert.equal(target.children[0],replacement);assert.equal(target.children[1],keep);assert.equal(removed.parent,null);
});

test('a failed row reload is retried even when sync metadata stays unchanged',async()=>{
 const {run,advance}=fixture();
 run("globalThis.loads=0;api=async()=>({data:[]});updateRefreshStatus=()=>{};updateSnapshotRows=async()=>{if(++loads===1)throw Error('temporary read error');};startSyncPolling();");
 await flush();assert.equal(run('loads'),1);
 await advance(30000);assert.equal(run('loads'),2);
 await advance(30000);assert.equal(run('loads'),2);
});

test('a status response arriving after the tab is hidden does not patch data',async()=>{
 const {run,emit,advance}=fixture();
 run("globalThis.release=null;globalThis.patches=0;globalThis.gets=0;api=()=>{gets++;return new Promise(resolve=>release=resolve);};updateSnapshotRows=async()=>patches++;startSyncPolling();");
 run("document.visibilityState='hidden'");await emit('document','visibilitychange');run('release({data:[]})');await advance(60000);
 assert.equal(run('patches'),0);assert.equal(run('gets'),1);assert.equal(run('syncStates.size'),0);
});

test('security-group refresh button targets the selected account and region once',async()=>{
 const {run}=fixture();run("page='securitygroups';location.hash='#securitygroups';securityGroupRegion='cn-hangzhou';globalThis.posts=[];startScopedRefresh=async(scope,path)=>posts.push(path);");
 await run("performAction('refresh-security-groups')");assert.equal(run('posts.length'),1);assert.equal(run('posts[0]'),'/accounts/1/ecs/security-groups/refresh?region=cn-hangzhou');
});

test('manual and scheduled row reads are serialized to prevent older data overwriting newer rows',async()=>{
 const {run}=fixture();
 run("globalThis.reads=[];patchSnapshotRows=()=>new Promise(resolve=>reads.push(resolve));");
 const first=run('updateSnapshotRows()'),second=run('updateSnapshotRows()');await flush();assert.equal(run('reads.length'),1);
 run('reads[0](true)');await flush();assert.equal(run('reads.length'),2);
 run('reads[1](true)');assert.equal(await first,true);assert.equal(await second,true);
});
