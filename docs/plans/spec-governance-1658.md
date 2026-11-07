# Spec read governance (#1658)

Governance uses the existing role-base-kind rule, cor:acl:130:01: `spec` and
`spec.*` are governed; a `spec` tag alone is a discovery marker, not law.
This is diligence, not a new access gate. The CLI does not alter stored nodes
while listing, describing or linting them.

## Read surfaces

- `spec list` retains both candidate streams, their complete pagination,
  deduplication, filters and final user window. Each JSON row gains `governed`.
  Governed text is unchanged; a tag-only row gains `[not governed]`.
- `spec describe` counts only governed roles in specs, roots, depth,
  numbering and approval/mint aggregates. It separately reports `notGoverned`
  and `notGovernedNodes` (citation and `governed: false`), with a non-null empty
  array when there are none. Human output names the tag-only citations.
- `spec lint` still discovers tag-only candidates at freeform or numbered
  locs. They receive just warning `not-governed`, without spec-shape,
  corpus, draft/reference or unwired checks. Existing `--strict` promotes
  warnings as before. A numbered node with neither marker retains `tag-spec`.
  Findings gain `governed` only on this command's JSON; other users of the
  shared lint-result DTO keep their shapes.

One shared `isGovernedSpec` predicate supplies the three decisions. The legacy
`isSpec` candidate/write predicate stays unchanged. `get`, `find`, `link`,
`edit`, register and other legacy users are not narrowed in this issue.
Existing query projections already include role; no new GraphQL selection,
SDL, generated client or server change is needed. Candidate scans remain
complete and permission-filtered by the server. No extra per-node query was
introduced; describe status reads exclude tag-only IDs. Lint still reads
candidate details to classify their actual current roles.

## Remedy and gate correction

The original issue assumed `spec edit` supplied governance. Source inspection
showed it omits role, and server update doors preserve an omitted role.
Xanthos amended Delta B: `hadron node update <node-urn> --role spec` explicitly
sets the role and routes through the existing spec door. The warning composes
the node reference through the existing reference helpers and shell-quotes it.
No edit/server implementation is changed. The unit flow simulates role
assignment; independent compiled-CLI/private-stack QA must prove the real
server result and read-back (`governedKind: spec`, list `governed: true`,
describe's governed count increases and not-governed count decreases).

## Verification boundaries

Red commits precede implementation for list marking, warning-only lint and
separate inventory totals. The first listing fixture omitted its memory
lookup; a second red-only commit corrects that before implementation, and its
clean failure is retained separately. Existing corpus-shape fixtures now carry
roles so those checks remain live; tag-only regressions stay explicit.
Focused command/spec suites, race runs and lint are author evidence; hosted CI
and named LOCAL/BLIND/QA receipts establish the frozen-head review schedule.
No production execution or latency claim is made.
