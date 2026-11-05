# Complete key inventories (cli#1547)

Status: implementation awaiting review and merge. Refs #1547; leaves it open.

## Contract and scope

Implements the existing envelope contract from server#3189 (27671b4b),
cor:api:120:02/:04, and gate 6654765233. Snapshot export was performed from an
owned detached server checkout at 05a67ec3611300794a5a0bc06eff36b13ce45606;
the exported SDL is byte-identical to the committed snapshot. Generated code
changes only for the migrated operations; no generated file was edited by hand.

| Consumer | Paged field | Preserved selection/arguments |
| --- | --- | --- |
| auth token list, default / --include-hidden | myUserApiKeysPage | UserApiKeyFields; includeHidden omitted unless true |
| app key list | appKeysPage | metadata only |
| app key list --scopes | appKeysPage | metadata + current scopes |
| app key list --scopes --host-uses N | appKeysPage | metadata + scopes + newest N host uses per key |
| all three App-key variants with --instruction | appKeysPage | separate instructed documents; same claim and session each page |

No subscription-list consumer exists: agentSubscriptionsPage and
myAgentSubscriptionsPage remain gaps under #890. This PR adds no commands.
Legacy unpaged spellings represent the same capability as their paged siblings;
server field retirement is #2213, outside this change.

## Traversal and output

Each request asks for 20 rows (the normal platform default; no user page-size
flag). Advance the offset by the number of items actually served. Only
hasMore=false ends collection: a short page does not; total is not selected or
used. An empty page with hasMore=true is a protocol error, not an empty inventory
or a reason to spin. Missing/null page envelopes fail rather than render [].

All collection completes before DTO conversion/output. Every later fetch error,
missing page and no-progress error discards collected rows; no partial inventory
is printed. App-key null-row refusal and personal-token null-row skipping are
unchanged. Existing explicit DTOs, nullable fields, nested [] versus omitted
hostUses, sanitization and server-error mappings are unchanged. --json remains
an array with the same row fields, not a page envelope.

Instruction validation/client setup still precedes reads. An ordinary operation
contains no instruction argument and sends no worker session. Instructed
operations reuse the same instruction/session for each page; each admitted page
records one app.credential-act event on the server (existing contract).

For a stable 100-row inventory: 5 requests of at most 20 metadata rows, with
only the existing selected fields plus hasMore; scopes/hostUses add their existing
per-key payload. An empty inventory takes one request. No performance claim:
QA records actual request count, total response bytes and process wall time for
its seeded corpus. A changing inventory is offset-paged, not a transactional
snapshot. Old providers lacking the Page fields fail with the existing schema
skew remedy; there is no unpaged retry/fallback.

## Verification

Red-first ef6eaa1e fails on the pre-migration implementation with 1/3 rows for
all five ordinary variants against the synthetic prefix-only fixture (not a
claim that the still-full legacy server roots truncate today). The same test now covers three instructed variants,
a short first page, deliberately wrong total=0, exact offsets 0/1/2, per-page
limit/host-use/include-hidden/instruction/session arguments. Later-page tests
cover GraphQL refusal, missing page and empty hasMore=true for all four query
shapes, asserting an error, exactly two requests and no partial stdout.
Existing DTO, sanitization, projection, instruction and include-hidden pins are
adapted to the envelope without weakening their underlying output contract.

Independent LOCAL/BLIND/QA receipts at one CI-green frozen head are required.
QA must run compiled commands against a private server with more than one page;
unit/fake-server results alone are not that runtime claim.

Author mutation verification: each edit was read back before running the focused
CLI tests. Five real mutants were killed (stop after one page; stop on a short
page; advance by requested limit; disable no-progress refusal; return collected
rows after a later fetch error). The equivalent offset += len(items) + 0 control
survived. Build/setup failures were not counted as killed mutants.
