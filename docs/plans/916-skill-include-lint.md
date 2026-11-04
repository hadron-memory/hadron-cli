# #916 C5: provider-owned include findings in skill lint

S11d is already merged: skillPlan resolves includes as the exporter and returns
`skill-include-broken` in its per-host findings; export already writes the
provider's rendered body. The CLI's lint command still judges stored node text
locally, so it cannot see the provider's include-resolution result. Read from
selected clean server main27455a482, resolvers.query.skill.ts and lintIncludes.ts;
rule cor:dmo:150:03 (R9). Its count/literal-notice warning refinement is built
but still labelled Proposed; this is a read of that shipped provider judgment,
not a new Decided law, body parser or client lint vocabulary.

Keep existing local per-host/declaration/collision/unknown-host rules, selectors,
raw source reads and strict behavior. Add two existing SkillPlan queries with
intent LINT (one per supported host), explicit sorted/deduplicated memory IDs
from the selected nodes, no files, no force and no renderedBody field. No query
or schema regeneration is needed. Empty node selection adds no plan calls.

Only the server's `skill-include-broken` findings are added. Because the plan
is memory-wide, filter its entries by the actual selected node IDs before
reading their findings, preserving --node and its duplicate/spelling rules.
Never use URN equality for membership or merge identity. Merge the same node's
identical warning across hosts, keeping two distinct nodes with coincident
legacy URNs separate. Preserve server message/severity/URN/memory; no guessed
target, count, host or missing-versus-unreadable diagnosis. Other server rules
are outside this bounded bridge and existing local rules remain authoritative
for their unchanged surfaces. The no-shared-identity key is local to new include
warnings; this PR does not refactor old findingSet behavior.

A literal broken-include notice in stored or successfully included text is not
a finding: this client never scans text for a notice. Null/malformed/failed plan
is not a clean report; errors return before emitting findings. Warning remains
exit0, --strict promotes it to existing exit5. This does not block any write or
export; lint writes no files. No fallback to a provider-less all-clear.

Author red be5a323 reports [] despite a provider warning. Tests cover real
command output/strict, no notice heuristic, no widened node selection, failed
plans, exact LINT/memory/host inputs, no body field, stable-ID grouping and
preserved corpus rules. Existing export tests characterize unchanged renderer
I/O; independent server/CLI warning and resolved-export QA remains owed. Author
uses fake transport only, no stack or production claim. Separate PR/Refs916.

Main C7 7d5a0e7 merged without rebase, help tree regenerated. Provider include
bridge code/tests are unchanged by integration. Help now distinguishes literal
ordinary variables from resolved include tags; the old pure finding-message
copy/parity cleanup is deferred to #1166, no policy/message change here.


## Review contract readback

Provider source: selected server main 27455a482, typeDefs.ts SkillPlan and
SkillPlanEntry; resolvers.query.skill.ts candidate cap, host classification and
returned counts. entries/findings are non-null lists of non-null objects. Null
checks defend malformed or incompatible wire replies; null is never clean.
scanned counts ANY host declaration, judged counts THIS host, so a gap and a
selected node omitted on another host are expected. Plans have no paging: node
and byte ceilings throw BAD_USER_INPUT, explicitly never silently truncate.
The client must not turn these counts into invented missing findings.

The whole combined lint remains fail-closed when include judgment is unavailable.
It preserves the provider error mapping and emits no valid-looking partial JSON
list or human OK line. Local findings are printed together with include findings
only after both checks complete. No degraded/offline mode was dispatched; an
older provider without skillPlan fails explicitly, never an include-free clean
lint. Old-server query compatibility in the checklist means the operation is
unchanged; it does not claim offline lint success on a pre-skillPlan provider.

New review tests cover null entry/findings/finding, empty selection with zero
requests, missing memory, sorted deduplicated multi-memory input, expected host
coverage gaps, non-include rule exclusion, severity retention, distinct messages,
human local+provider rendering, and failure output with existing local findings.
The null-finding negative was red before the guard fix. C5's schema has no task
Project input field; the shared omission defect on C1b/C3b does not occur here.
