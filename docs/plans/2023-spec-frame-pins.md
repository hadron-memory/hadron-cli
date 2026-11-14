# Spec frame causal controls (#2023)

## Gate and scope

Gate 6875693434 / FIT 6875559427 clears test-only F2 controls and F4
fixture/test hygiene after #2022 merges. Based on main fa94e087, whose exact
second parent is #2022 reviewed c0a2251b. No production, parser, rubric,
error-map, diagnostic, exit-code, template metadata, or cutoff change.

Refs #2023: this build handles the cleared test-only portion. The tracker
remains open for the separate behavior/authoring-owner decisions. F3
abstract-report dedup and additional malformed-role behavior are excluded.
Revision-0 and rescind-preview notes remain accepted scope facts. No new law:
these tests pin existing CLI behavior; production still reads live templates.

## Pin-versus-remove decisions

The current source and intended scope were checked before selecting each
pin. All seven are retained, with no rule widened or removed.

| Original F2 item | Current source / intended scope | Evidence |
|---|---|---|
| Template cache | lintFrames local templates map, one raw template read per type per invocation | Two same-type nodes; two independent invocations, cumulative reads 1 then 2 |
| Unresolved-target cache | local byLoc map caches missing as Unavailable, but each link still reports its own error | Two nodes, two repeated missing links each: one read and four errors; readable-target control is clean; another invocation reads afresh |
| Kind-role mismatch refusal | fetched template type must match requested spec.role before checking its structure | Valid Flow template returned for Rule request refuses, nil findings, one read; normal Rule fixture remains valid |
| Retired frame-role exemption | retired structures do not require a typed role; links remain corpus-wide | Same framed untyped body active vs retired gives exactly one vs zero role findings; no template I/O |
| Host-specific portal links | existing corpus-link rule, including historical nodes; not permission to skip links | Existing #2022 historical-link test already kills removal; retain and re-measure |
| Legacy cutoff | existing 2026-10-04 nonframed-body boundary; empty UpdatedAt is not legacy | Before/boundary/empty-date controls and a framed pre-boundary positive; no date parser or rubric change |
| Duplicate frameBasics guard | generic platform lint owns basics, custom-memory explicit frame pass owns it there | Combine actual lintNode + lintFrames call sites for both memories; exactly one frame-name and one frame-tags |

## Red-first measurement

On fresh main before adding the tests, precise production mutations in
frame.go survive for template cache, unresolved-target cache, kind mismatch,
retired role exemption, and duplicate-basics guard. That is the gate's
specified red control (missing discrimination), not an intentionally failing
normal test. Portal-rule and cutoff mutants are already killed by #2022's
historical-link controls, so no claim they remain uncovered.

An equivalent template-cache rewrite survives as a harness control. Every
mutation is a single exact replacement asserted to match once; actual edited
text and full test output are recorded. Compile/setup failures do not count
as killed. Production source is restored in finally; none is committed.

## Fixture and hygiene choice

Reuse the existing Rule snapshot in tests and derive one wrong-kind response
from it. The mismatch fixture is deliberately not a new canonical Flow
template. No production list of sections, copied role vocabulary, fresh
template marker, or corpus write. Ten full type snapshots are optional and
not added: live parsing of all ten was already evidenced in #2022; these
controls exercise runtime fetching and cache behavior without freezing ten
independent copies of the rubric. Existing invalid/null-template fail-closed
tests remain in the measured suite.

## Reviews

T1 cross-family LOCAL and BLIND, different engineers, not change managers.
QA is not a separate seat by the gate's explicit test-only deviation: CI and
causal mutation kills verify this artifact. UX waived; security n/a. Exact
green head and independently recorded seat receipts belong on the PR.
