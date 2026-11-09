# Server-owned `skill lint` judgment (as built)

Issue: hadron-memory/hadron-cli#1393 (CLI tail of hadron-server#1177).

## Decision

`hadron skill lint` has one judge: the server's `skillPlan` with `intent: LINT`.
The CLI no longer calls the local `skilldoc` lint, collision, or unknown-host
rule engines for its verdict. The local package remains as the parity oracle for
the server port and for declaration-shape coverage checks, not as an alternative
lint mode. There is no `--offline` flag and no fallback to a local verdict when
the server is unreachable, refuses the plan, or returns an incomplete plan.

## Query boundary

A dedicated `SkillLintPlan` operation selects only the fields lint consumes:
entry identity/class/findings, host-free `unrecognized` findings, and the
`judged` count used for completeness validation. It never selects a rendered body. The existing `SkillPlan` operation is
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
Identical findings merge across hosts without duplicating a host after repeated
findings within one host. Host-free unknown-key findings follow hosted rows,
and batch-unavailable refs remain last. JSON keeps the existing DTO shape and
non-null arrays. Valid, malformed, and unknown-host declaration shapes all count
as skill-declaring nodes in the human summary. Errors exit 5; warnings exit 0
unless `--strict` promotes them.

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
- host-free findings that differ between host plans, whose contract requires
  identical rows.

A selected declaration the local parsers expect but the server's plan does not
judge (omitted from the host's plan, or an unknown-host key with no host-free
row or no findings) is **not** an abort: the CLI's skilldoc view (host registry,
alias and shape rules) can legitimately differ from the server's plan scoping
under registry or version skew, and failing the whole lint would make it
unusable for a corpus until the CLI is upgraded. Such a node gets one host-free
`skill-node-not-judged` warning (selection order, after the judged rows), so it
is never reported clean, and `--strict` still fails on it. The server remains
the only judge; the local view only decides whether to *warn* about a gap.

## Validation

Command fixtures cover differing server/local verdicts, text and JSON grouping,
include preservation, strict mode, refusal mapping, host-free rows, locked
entries, malformed and inconsistent plans, and the absence of offline mode.
The transport control fails the `SkillLintPlan` request after successful node
selection, proving that this path cannot silently use the local engine. A query
selection-set test pins `unrecognized` to lint only. The full `go test ./...`,
golangci-lint, generated-client freshness, help-inventory, and diff checks are
the merge gates.
