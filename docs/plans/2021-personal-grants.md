# Personal grants read (#2021)

## Scope and authority

`hadron auth grants` lists one page from the built `myGrants(limit,offset)`
query, as the configured credential, without a worker-session header.
Gate 6875283405 and FIT 6875250416 bind three conditions: person credential,
neutral empty output, and no derived lifecycle labels or top-level grant-name
decision. The existing PrincipalGrant command group is unchanged.

This implements cor:acl:140:05 List and cor:api:120:04 paging.
No new platform rule is decided; Vera owns the Built-line follow-through.
Portal #2134 and server #4168 are separate work. No corpus writes.

## Audience and data

The server lists direct person-held grants across Apps and organizations,
including all states, newest first. Only issuing/requesting a grant for
someone else does not include it. App keys, runs, impersonation, and
person-driven worker sessions receive empty pages by design; transport errors
and server refusals must never become empty success.

The command calls Factory.GraphQLClient directly; it does not read a worker
binding, resolve an App, or attach WithSession. An already bound checkout
therefore still sends the personal read without a worker-session header.
Missing credentials follow the existing auth-required error. Older servers
without myGrants return the usual API compatibility error; there is no
fallback to another grant audience.

The query selects raw timestamps, allows, and signatureStatus.state plus
integrityProblems. It never selects signature records, signer identities,
issuer details, or manager-only holder-departure/revoker details. There is no
clock-based lifecycle classifier. Human cells guard server text with SafeCell.

## Paging and output

One explicit page per call: default limit 20, offset 0; limit 0 returns
only total. Both arguments accept the nonnegative GraphQL Int range.
Requests above 200 pass through, with the server capping returned rows;
they are not refused. This is a page UI, not an authority decision or a claim
to have enumerated the whole scope.

JSON has items (always an array), total, hasMore, nextOffset. Each item has
id, appId, action, values, holderKind, holderId, startsAt, expiresAt, maxUses,
useCount, reason, createdAt, activatedAt, revokedAt, allows, and
signatureStatus {state, integrityProblems}. Nullable values remain null.
Continuation is offset plus the number of returned rows when below total;
nextOffset is null when complete. An empty page before total is inconsistent
and fails rather than generating a non-progressing cursor. Past-end offsets
produce an ordinary empty page. Count-only JSON has exactly {total}.

Text prints those facts plus page metadata and explicitly avoids saying
that an empty page proves the person has no grants. No hidden extra page,
identity preflight, or mutation is sent.

## Capability omissions

cor:api:240:02 omission reasons are stated in help. Signing requires the
person's passkey on the server browser ceremony page; this command cannot
activate grants. Manager revoke and withdrawal have separate authority and
confirmation flows, assigned elsewhere. No write or browser opening here.

## Evidence and review

Red-first c5e811b7 fails on main 0b43d521 because auth grants is absent.
Command tests capture the actual HTTP query in a bound worktree and pin the
absent worker-session header, one read, raw facts, neutral empty result,
count-only, bounds, larger request/server cap continuation, safe human cells,
private-field omission, raw signature states, and failure-vs-empty behavior.
No live grant writes or live server authorization proof is claimed.

Gate schedule: cross-family LOCAL and BLIND (two engineers, not change
managers), compiled-CLI QA, and security at every head including deltas.
UX waived by the stamp. Author evidence and independent receipts will be
linked on the PR at its exact CI-green frozen head.

Author controls: four production mutants (worker header, changed allows,
null items, dropped offset) fail their intended tests; an equivalent default
20 -> 10+10 survives. A fifth mutant adds issuerUserId in the source query,
regenerates, and fails the exact-field AST omission pin; source and generated
client restored. No test-build failure is counted as a killed mutant.

Compiled examples against an isolated 41-row fixture: one request each,
default 20-row response 8,258 bytes / 0.016 s; page offset 20 response 8,268
bytes / 0.015 s; count-only 50 bytes / 0.014 s; past-end offset 99 50 bytes /
0.013 s. These are synthetic local timings, not live-server performance.
The whoami help pointer was followed with a separate fixture AuthContext
read showing the credential's principalType. Login help inspected; no login
or passkey ceremony performed.
