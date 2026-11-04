# Automation team snapshot grants (#1578)

Status: implemented in this change; not a release claim.

## Contract and source

This exposes the server's terminal per-automation grant from hadron-server
`ecdde802def240d52a503b3977792d5e8001c128` (PR #3363). The schema was exported
from a clean detached worktree at that exact revision, then genqlient regenerated.
The removed App-wide `runTeamSnapshotEnabled` permission is not exposed.
The read-grant sibling (#1566) is still open; this group stands alone.

A schedule or webhook can receive read-only access to its own App's roster and
tasks, default off. A manual run has no automation. The CLI makes no authority
decision: the server validates the person, App, trigger and grant. `set` does
not attach a worker session, including in a bound worktree. No command here
implements `--instruction`; that companion waits on server #3361.

## Commands and requests

- `automation team-snapshot-grant set <app-ref> --trigger-kind SCHEDULE|WEBHOOK
  --trigger-id <id> [--yes]`: show the selected App/trigger in confirmation and
  send one mutation. An identical repeat returns the same live row.
- `automation team-snapshot-grant list <app-ref> [--include-revoked]
  [--limit n] [--offset n]`: forward the explicit App, preserving server order.
  Default page size 20; absent `--limit` walks all pages, explicit 1..200 reads
  one page. `--offset` is nonnegative. Unset includeRevoked is omitted; explicit
  false is sent. Continuation guidance goes to stderr.
- `automation team-snapshot-grant revoke <grant-id> [--yes]`: App from `--app`,
  else `hadron app use`. Read that App's history with includeRevoked, find the
  exact ID, show its returned App and trigger in confirmation, then one revoke.
  The human scope line and prompt say where the App context came from.

Revoke's history lookup is the no-targeted-read exception approved by Xanthos
(chat #72422). There is no grant-by-ID read in the pinned SDL. An absent row
returns uniform NotFound (4), without inferring anything about other Apps, and
sends no mutation. An ended row is shown with a stderr note, no repeated revoke.
Patrick filed server #3381 for a narrower read; it does not block this build.

For N rows, a complete list costs ceil(N/20) GraphQL requests (one for N=0),
with eight scalar fields per row and three page fields; an explicit page costs
one request. Revoke has the same history-read cost plus at most one mutation.
For 100 history rows, that is five reads plus at most one write, not 100 lookups.
Each normal row is a few hundred JSON bytes; response size scales with rows and
server ID lengths. No task content, worker prompts or credentials are selected.
List walks are not atomic: concurrent changes may alter totals or cause a
repeated row; the latter is refused rather than presented as complete.

## Output and failures

A package-local eight-key DTO carries `id`, `appId`, `triggerKind`, `triggerId`,
`grantedBy`, `createdAt`, `revokedAt`, `revokedBy`. These are stored IDs, not
parsed display labels. Revoked fields are explicit null on a live row. List
is `{items,total,hasMore}`, with `items: []` for empty. JSON preserves server
values; human server-derived cells use SafeCell. No generated type is marshalled.

FORBIDDEN uses the existing MapError mapping (8), with preserved serverCode.
No request is retried. The write paths are: invalid arguments/client setup or
confirmation refusal (no mutation); server refusal (mapped error); transport or
internal failure (unknown outcome, list before retrying); null/incomplete or
mismatched write receipt (error, no success); valid matching receipt (output).
A lost answer does not prove the write failed. List errors on any page produce
no partial stdout. Nil pages/rows, negative totals, oversized/nonprogressing
pages and repeated IDs fail instead of looking like a complete result.

## Snapshot drift and limits

The pinned refresh also imports existing main's comments and the unrelated
`setAssetPublicLink(instruction)` argument. It adds no optional fields to an
existing generated input. The asset instruction CLI companion is #1610; the
coverage matrix records that gap rather than implementing it here. The tool manifest is regenerated from the same pinned source (nine tools already
on server main; no MCP implementation in this PR). The three
snapshot-grant operations have command rows; the removed App-wide gap vanishes.

Author checks use fake GraphQL transport and do not prove live server authority.
The gate additionally requires independent compiled-binary private-stack QA:
give/repeat/list/revoke/list ended/non-manager exit 8. No production mutation,
release, deployment or install is authorized by this build.
