// Run from a prepared hadron-server checkout: node --import tsx /path/to/this.mjs.
// Exercises real exported helpers, with an in-memory SQL transaction seam.
// No database, resolver authorization, concurrency or summary delivery claim.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const dir = process.env.HADRON_SERVER_DIR;
assert(dir, 'Set HADRON_SERVER_DIR to the deliberately chosen server checkout');
const file = 'src/lib/agentTasks/sweepRuns.ts';
const revision = execFileSync('git', ['-C', dir, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
assert.equal(execFileSync('git', ['-C', dir, 'status', '--porcelain', '--', file], { encoding: 'utf8' }), '', 'contract source must be clean');
const { validateSweepRun, insertSweepRun } = await import(pathToFileURL(resolve(dir, file)));
let row;
let inserts = 0;
let sqlCalls = 0;
const tx = {
 async $queryRaw(parts, ...values) {
  sqlCalls++;
  const sql = parts.join('?');
  if (sql.includes('FOR SHARE')) return [];
  if (sql.includes('INSERT INTO host_sweep_runs')) {
   assert(sql.includes('ON CONFLICT (app_id, host_id, run_key) DO NOTHING'), 'insert must retain first-row-wins conflict policy');
   if (row) return [];
   inserts++;
   row = { id: values[0], app: values[1], host: values[2], token: values[3], values };
   return [{ id: row.id }];
  }
  assert(sql.includes('SELECT id FROM host_sweep_runs'), `unexpected SQL: ${sql}`);
  assert.deepEqual(values, [row.app, row.host, row.token]);
  return [{ id: row.id }];
 },
 async $executeRaw() { return 0; },
};
// Mirrors the resolver's explicit validate-before-insert ordering. Source review
// of resolvers.hostSweepRuns.ts remains necessary if that ordering changes.
async function record(input, now) {
 const checked = validateSweepRun(input, now);
 if (!checked.ok) throw new Error(checked.message);
 return insertSweepRun(tx, 'app', 'host', checked.run);
}
const now = new Date('2026-01-01T00:00:00Z');
const original = { runId: 'orca-down:lost-answer', mode: 'APPLY', startedAt: now, endedAt: now,
 planned: 0, applied: 0, skipped: 0, failed: 1, lines: [{ worker: 'first', outcome: 'FAILED', reason: 'ORCA_DOWN' }] };
const first = await record(original, now);
assert.equal(first.created, true);
const saved = structuredClone(row);
const later = new Date(now.getTime() + 2 * 60 * 60_000);
const retry = { ...original, startedAt: later, endedAt: later, failed: 2,
 lines: [...original.lines, { worker: 'second', outcome: 'FAILED', reason: 'ORCA_DOWN' }] };
assert.deepEqual(await record(retry, later), { id: first.id, created: false });
assert.deepEqual(row, saved);
assert.equal(inserts, 1);
// Age alone does not invalidate an original report; invalid new input is refused
// even if its run token already exists, without reaching the insert seam.
assert.deepEqual(await record(original, later), { id: first.id, created: false });
const calls = sqlCalls;
await assert.rejects(record({ ...retry, startedAt: now, endedAt: later }, later), /one hour/);
await assert.rejects(record({ ...retry, failed: -1 }, later), /whole number/);
assert.equal(sqlCalls, calls);
console.log(`PASS S10 replay helper contract at ${revision}: valid changed retry returns original; malformed retry refused before insert (SQL seam, no live DB)`);
