import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { mock } from 'node:test';
const dir = process.env.HADRON_SERVER_DIR;
assert(dir, 'Set HADRON_SERVER_DIR to the deliberately chosen server checkout');
assert(process.execArgv.includes('--experimental-test-module-mocks'), 'Pass --experimental-test-module-mocks');
const revision = execFileSync('git', ['-C', dir, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
assert.equal(execFileSync('git', ['-C', dir, 'status', '--porcelain', '--untracked-files=no'], { encoding: 'utf8' }), '', 'tracked server source must be clean');
const url = p => pathToFileURL(resolve(dir, p)).href;
const helperPath = 'src/lib/agentTasks/sweepRuns.ts';
let helpers;
try { helpers = await import(url(helperPath)); }
catch (cause) { throw new Error('Prepare server dependencies/Prisma client and run node --experimental-test-module-mocks --import tsx /path/to/check-s10-replay-contract.mjs', {cause}); }
const stored = new Map();
let inserts = 0, sqlCalls = 0, summaries = 0;
const tx = {
 async $queryRaw(parts, ...v) {
  sqlCalls++;
  const sql = parts.join('?').replace(/\s+/g, ' ').trim();
  if (sql.includes('FOR SHARE')) return [];
  if (sql.includes('INSERT INTO host_sweep_runs')) {
   assert(/ON CONFLICT\s*\(app_id,\s*host_id,\s*run_key\)\s*DO NOTHING/.test(sql));
   const [id, app, host, runKey, startedAt, endedAt, mode, planned, applied, skipped, failed, lines] = v;
   const key = JSON.stringify([app,host,runKey]);
   if (stored.has(key)) return [];
   inserts++;
   stored.set(key, {id,hostId:host,runKey,startedAt,endedAt,mode,planned,applied,skipped,failed,lines:JSON.parse(lines),recordedAt:new Date()});
   return [{id}];
  }
  assert(sql.includes('SELECT id FROM host_sweep_runs'), `unexpected SQL: ${sql}`);
  const row=stored.get(JSON.stringify(v));
  assert(row);return [{id:row.id}];
 },
 async $executeRaw() { return 0; },
 hostSweepRun: { async findUniqueOrThrow({where}) {
  const row=[...stored.values()].find(r=>r.id===where.id);assert(row);return structuredClone(row);
 } },
};
// Replace only authorization/audit/notification plumbing: this probe is not an
// authority or delivery test. The real resolver owns validation/insert ordering.
mock.module(url('src/api/graphql/schema/resolvers.agentTask.ts'), {namedExports:{
 taskError: (code,message)=>Object.assign(new Error(message),{extensions:{code}}), withReadableApp:()=>{throw Error('unused read');},
}});
mock.module(url('src/api/graphql/schema/resolvers.hostOperation.ts'), {namedExports:{
 asHost: async (_ctx,_ref,fn)=>fn(tx,{id:'app'},{hostId:'host'}),
 recordingRefusal: async (_ctx,_action,_subject,fn)=>fn(),
}});
mock.module(url('src/lib/appKeyScopes/scopes.ts'), {namedExports:{appKeyCaller:()=>null}});
mock.module(url('src/lib/hostOperations/hostKey.ts'), {namedExports:{
 keyEverHeldHostOperate:()=>{throw Error('unused read');},keyHostId:()=>{throw Error('unused read');},
 recordHostKeyUse:async()=>{},recordHostKeyUseApart:async()=>{},
}});
mock.module(url(helperPath), {namedExports:{...helpers,postSweepSummaryDeferred:()=>{summaries++;}}});
const {hostSweepRunMutations}=await import(url('src/api/graphql/schema/resolvers.hostSweepRuns.ts'));
const record=input=>hostSweepRunMutations.recordHostSweepRun({}, {input}, {db:tx});
const now=new Date();const older=new Date(now.getTime()-2*60*60_000);
const original={appRef:'app',runId:'orca-down:lost-answer',mode:'APPLY',startedAt:older,endedAt:older,planned:0,applied:0,skipped:0,failed:1,lines:[{worker:'first',outcome:'FAILED',reason:'ORCA_DOWN'}]};
const first=await record(original);const saved=structuredClone(first);
const retry={...original,startedAt:now,endedAt:now,failed:2,lines:[...original.lines,{worker:'second',outcome:'FAILED',reason:'ORCA_DOWN'}]};
assert.deepEqual(await record(retry),saved);
assert.equal(inserts,1);assert.equal(summaries,1);
for(const bad of [{...retry,startedAt:older},{...retry,failed:-1}]) {
 const calls=sqlCalls;
 let refusal;
 try { await record(bad); } catch(error) { refusal=error; }
 assert.equal(sqlCalls,calls,'invalid retry must not reach SQL');
 assert.equal(refusal?.extensions?.code,'BAD_USER_INPUT');
}
// Positive control: a different key must be a new run, not a fake conflict.
const next=await record({...retry,runId:'orca-down:another'});
assert.notEqual(next.id,first.id);assert.equal(inserts,2);assert.equal(summaries,2);
console.log(`PASS S10 actual resolver at ${revision}: valid changed retry returns original row once; invalid retry stops before SQL; distinct run inserts. Auth/audit/notification stubs and SQL seam, no live DB.`);
