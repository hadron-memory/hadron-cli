# Personal-grants wording and controls (#2028)

## Scope

Gate 6876629164 (not the mistyped finish URL), selection 6876266836 and
FIT 6876173635 clear a bounded follow-up to merged #2025. Base main is
1b4c4342, whose second parent is reviewed 2a78bf07. Existing raw-fact and
human signature-state controls are extended, not rebuilt in another suite.

Runtime changes are only personalTime -> personalCell (same body), the
specified human count/footer text, and two help sentences. Requests,
credentials, projection, DTOs, flags, bounds, pagination and exit mapping
are unchanged. No query, schema, new lifecycle label, null normalization,
layout redesign, warning stream or corpus edit.

## User-visible behavior

Count-only text is total matching: N. A page footer is:

    total matching: N; rows on this page: R; offset: O; requested limit: L; has more: B

The next-offset line and all table columns remain. N is the server's total
for this caller, R is this page's row count. The neutral empty-audience text
still prevents zero/empty from proving that the person has no grants.
Count-only JSON remains {total}; normal JSON remains the existing page DTO.

Help and agentic usage state exactly:
- Limit 0 returns the total matching and no rows.
- Offset pages are not a snapshot; grants added or removed between calls can shift later pages.

Offset is still forwarded, including with limit 0. Default20, server cap200
and no automatic page scan remain. The helper rename does not turn a
signature state into a timestamp or change nil/empty-string behavior.

## Controls

Red-first 61db5bd7 on main fails the count/footer/help wording assertions
for the old wording; its JSON continuation control stays green.

The existing raw-decision test now has independent cases: allowed CURRENT,
spent CURRENT (allows false), before-start CURRENT (allows false), EARLIER
with nonzero integrity count, PENDING, CANCELLED and null. Dates are raw
fixture data, not a client clock. A checked single-match replacement helper
makes fixture formatting/duplicate-token changes fail explicitly.

JSON assertions require exact key spelling, presence, non-null bool/count
values even at false/0, and the raw signature state/null. Human assertions
include a positive USE COUNT and the existing final three decision cells.

Additional root/HTTP controls cover:
- Empty in-memory credential store: exit3 and no request, independent of a
  host keychain and distinct from a server UNAUTHENTICATED response.
- Past-end offset99 with total41: success, items[], hasMorefalse,
  nextOffsetnull; non-progress before total still fails.
- Explicit limit2/offset7/two rows/total10: nextOffset9, real rows, exact
  footer and unchanged JSON. Existing over-cap/requested-vs-returned pin stays.
- Bound and unbound private binding caches with explicit other App:
  one MyGrants, no App or worker-session header/variable/resolution/preflight.
- Cleaned unsafe reason present as well as controls absent; empty arrays
  discriminate [] from null and missing.

Author checks: command/API race suites pass (root command package 156.743s),
lint reports 0 issues, generated client stays clean, coverage matrix and
help inventory checks pass. The inventory refresh changes only its revision pin.
All three help examples execute against a compiled CLI and isolated loopback
fixture; extra count/footer and past-end cases pass, one request each.

Causal controls: 22 precise compiling non-equivalent mutants fail their
intended assertions; an equivalent helper rewrite survives. The driver checks
one exact source match, records each diff and named failing test, separates
build errors, and restores source bytes. Diffs, failure sets and output hashes
are in hrn:node:hadronmemory.com:hadron-dev-team-jane:work:cli-2028:author-checks.
Independent exact-head LOCAL/FULL BLIND/compiled-fixture QA is required before
readiness. No live server proof is claimed.

## Preserved boundaries

This is an observer of raw server allows/timestamps/integrity/signature
state. It never derives any of the seven grant lifecycle states or confuses
them with the four signature states. Signing/activation/revoke remain out.
#1832 remains the separate worker audience; the existing artifact pointer stays.

The #1052 help inventory column owns the audit, not feature implementation.
No new command page is introduced. Cosmetic max/constant/key-order/file
renames and values-null/table-width redesign were declined by the gate.
No platform rule or Built fold is owed: existing contracts are preserved.

T1 LOCAL + FULL BLIND (distinct cross-family engineers, not CMs) and
compiled synthetic HTTP/config QA. UX waived, security n/a per the stamp
because the credential gates and query projection are unchanged.
