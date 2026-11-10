# Server-owned `skill lint` judgment (as built)

Issue: hadron-memory/hadron-cli#1393 (CLI tail of hadron-server#1177).

## Decision

`hadron skill lint` has one judge: the server's `skillPlan` with `intent: LINT`.
The CLI no longer calls the local `skilldoc` lint, collision, or unknown-host
rule engines for its verdict. The local package remains as the parity oracle for
the server port and for identifying declaration subjects in the human summary
and known-shape completeness checks, not as an alternative lint mode or verdict.
There is no `--offline` flag and no fallback to a local verdict when the server
is unreachable, refuses the plan, or returns an incomplete plan.

## Query boundary

A dedicated `SkillLintPlan` operation selects only the fields lint consumes:
entry identity/class/findings, host-free `unrecognized` findings and their
structured `keys`/`keyCount`, and the `judged` count used for completeness
validation. It never selects a rendered body. The existing `SkillPlan` operation is
unchanged for `skill status`, so status does not acquire a dependency on the
newer `unrecognized` schema field.

Selection still resolves the requested memories and nodes, deduplicates node
IDs, reports batch-unavailable refs, and counts declarations for the existing
human summary. The two host plans use the sorted selected memory IDs. Results
are filtered back to exact selected node IDs because the server judges the
memory-wide corpus; consequently, a selected node can carry a collision caused
by an unselected node.

## Result and failure contract

Hosted findings retain host declaration order and server entry/finding order.
The Nth identical finding merges across hosts into the Nth row, so repeated
findings within one host remain distinct and no host is printed twice on a row.
Host-free unknown-key findings follow hosted rows, node-scoped
not-judged warnings come next, then server-unavailable rows and
batch-unavailable refs.
JSON keeps the existing DTO shape and non-null arrays. Valid, malformed, and
unknown-host declaration shapes all count as skill-declaring nodes in the human
summary. Errors exit 5; warnings exit 0 unless `--strict` promotes them.

A locked encrypted entry has `class: unavailable` and no findings by design. If
either host returns that entry, it produces one host-free
`skill-node-unavailable` warning rather than a false clean result or a verdict
about ciphertext.

The CLI rejects, without printing a partial or clean result:

- transport and GraphQL failures, mapped through the usual exit codes;
- null/missing plans, entries, findings, or host-free collections;
- a `judged` count that disagrees with the entry count;
- malformed entries anywhere in the memory-wide plan, including unselected
  nodes;
- an unavailable entry carrying purported content findings;
- host-free findings or structured key rows for SELECTED nodes that differ
  between host plans (their serialization order may vary). Unselected changes
  do not abort a node-scoped lint.

A syntactically valid plan that omits a selected declaration for a known host
is different: the CLI adds a host-specific `skill-node-not-judged` WARNING and
preserves the findings the server did return. Coverage is tracked per node
AND per host, so an entry for one known host cannot conceal an omitted
declaration for another, and a host-free unknown-key warning never covers a
known declaration beside it. The row's `hosts` names the missing host and its
`memory` preserves the selected memory URN, including when flattened node URNs
collide. Local declaration classification is only a coverage hint: under
registry/version skew it never turns the server's valid empty result into a
blocking error.

Unknown exports keys use the structured `keys` and `keyCount` fields, not
finding prose. When the list is untruncated (`keyCount == len(keys)`), each
locally-unknown key absent from that list receives a non-blocking
`skill-node-not-judged` warning. A missing entry or one with no findings is no
judgment and produces the same warning. Truncated lists retain their own
aggregate warning and are not over-interpreted. All such findings remain
warnings under `cor:agt:030:06`; the result is not falsely clean, but ordinary
lint still exits 0 unless `--strict` promotes warnings.

For a persistent known-host omission, the warning and `skill lint --help` direct
the reader to retry once (the corpus can change between host plans) and then
report a server `skillPlan` defect with the node and host.

`SkillPlan.scanned` is likewise not selected-node coverage: plans are
memory-wide even for `--node`, and the count includes intentional no-finding
rows such as `exports: {}`. Treating the aggregate gap as proof that an
arbitrary selected property is a server-only declaration would false-fail
ordinary nodes and unselected declarations. The server must expose
`SkillPlan.unrecognized`; older servers fail through the normal GraphQL mapping.

## Validation

Command fixtures cover differing server/local verdicts, text and JSON grouping,
include preservation, strict mode, refusal mapping, host-free rows, locked
entries, malformed and inconsistent plans, and the absence of offline mode.
The transport control fails the `SkillLintPlan` request after successful node
selection, proving that this path cannot silently use the local engine. A query
selection-set test pins `unrecognized` to lint only. The full `go test ./...`,
golangci-lint, generated-client freshness, help-inventory, and diff checks are
the merge gates.
