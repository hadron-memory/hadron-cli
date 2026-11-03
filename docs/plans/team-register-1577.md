# Read the action register

`team register` wraps `actionRegisterIndex` in one data read. Human output adds one slim, best-effort App identity read
for the scope label; JSON never sends that decoration. App selection reuses
`resolveTeamAppScope` (--app, configured App, worktree binding); Agent selection
stays entirely on the server. No Agent query, client authorization, fallback,
execution, mutation, cache or URL fetch is added.

## Contract

The JSON DTO is `{entries, total, hasMore}`. Each entry has code, state,
actionUrn, entryUrn, definedBy, taskLoc and taskUrn. Nullable fields remain null,
empty entries is [], and server order and counts are preserved. Human text
shows code/state for every row and safe cells for every server value. Unresolved
means no runnable task found at the given loc; unmapped means malformed entry.
An empty page never diagnoses why. Every page prints shown/total/more.

`--agent`, `--task-loc`, `--code`, `--limit` and `--offset` become optional,
pointer-valued, omitempty GraphQL variables. Unset flags have no variable key;
there are no variable defaults in the document, so the server applies its own
argument defaults. Explicit zero is sent. Limits over 200 pass through for
server clamping. Empty strings and negative paging values are local usage errors.
APP_NOT_FOUND/AGENT_NOT_FOUND exit 4, BAD_USER_INPUT exits 2; server messages and
codes survive human and JSON error rendering. No retry or local Agent guess.

## Source and verification

Schema refresh is isolated at server 3de80a65a73e5642bbca6229ec2f128d975de384,
which contains the gated minimum 956f86ae. Snapshot refresh and matrix classification
are explicit steps; registerRoleCandidates/registerRolePick remain outside this slice.

Red-first fake HTTP tests pin flag presence, nullable rows, all states, count-only,
paging, refusals, opaque empty and safe text/original JSON. The first test commit
had a wrong exit-helper name; the following test-only commit corrected it and
confirmed missing-command failures before implementation. It is not claimed as
a behavioral red until that correction. Independent compiled synthetic and
bounded Sonnet private-stack QA is required at the frozen head, including person
explicit/absent Agent, worker implicit Agent and unreadable-chain empty parity.
No production writes or reads are authorized by this plan.

## Fetch budget

JSON and refusals use one GraphQL request (apart from the legacy binding's
team-memory App fallback if that old binding lacks AppID). Normal human success
adds one three-scalar App identity request; failure degrades to the selected ref.
For a team with a few hundred codes, default 20 rows should be a few KB; capped
200-row pages are tens of KB depending on URN/loc length. No node body/prompt is
selected. QA records actual response bytes, request counts and process wall time
on its private seeded corpus. Schema refresh also imports main's checkpointNode
and reportHostOperation description additions, unused by this command.

## QA correction: bound-worker inference

At57b2b6bf the data read lacked X-Hadron-Session, so a worker-bound CLI was
seen as its underlying person and got BAD_USER_INPUT with no --agent. Celine's
private-stack QA P2 is retained. A separate red pin reproduces the missing
header; the fix adds command-local api.WithSession for a full, same-server,
unchanged binding only. The binding is freshly re-read immediately before the
data call. Unbound, missing-server, empty-session and other-server controls
remain headerless. The Agent variable remains omitted; the server alone uses
the authenticated worker's Agent. App identity decoration remains headerless.
