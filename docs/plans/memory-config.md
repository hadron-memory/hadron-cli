# Design as built: `memory config` — a memory's node-role rules (#716 slice 1)

> **Status: built in a draft PR (Jane), against hadron-server#1360 (#1325
> part b) at `058fce63`, stacked on #1345 at `3d41666`.** Neither is merged, so
> the schema snapshot is a candidate; it is re-exported from the actual merge
> before this lands. Part of hadron-concept#74 (CLI item 3). The design handoff,
> Eli's Q1–Q9 answers and the API-readiness audit are on cli#716.

## 1. Scope

In: `memory config get` and `memory config rule add|update|rm`, over the part-(b)
API (`memoryConfig`, `createNodeRoleRule`, `updateNodeRoleRule`,
`deleteNodeRoleRule`).

Out, and not mocked: templates (#1325 part c), applying them and the locked-rule
refusal (#1334), and the visibility warnings themselves (#1327). `locked` and
`sourceTemplate` are displayed because the server returns them; nothing can set
them yet.

## 2. What the server provides (part b)

- **Managers only (H3/H4).** The strict owner of a personal/private memory, else
  its user owner or an org ADMIN/OWNER. For anyone else `memoryConfig` answers
  `MEMORY_NOT_FOUND` and a rule ref answers `NOT_FOUND`, exactly as for a missing
  one.
- **The empty config is a success:** `id: null`, `rules: []`, until the first
  rule write materializes the row.
- **A rule is addressed by its id**; it has no URN. Each carries a `revision`
  (optimistic concurrency): `updateNodeRoleRule` / `deleteNodeRoleRule` take
  `expectedRevision` and refuse a mismatch with `CONFLICT {currentRevision}`,
  writing nothing.
- **References** are stored by node id and returned as a URN plus a state:
  `NONE | OK | BROKEN | UNREADABLE`, the URN null unless OK.
- **Update semantics:** an omitted input field is unchanged; a reference sent as
  `null` or `""` clears it; `validateBy` clears only on an explicit `null`.
- **Errors:** `NODE_ROLE_RULE_EXISTS`, `INVALID_NODE_ROLE {role, reason, maxLength}`
  (`reason` is `EMPTY | TOO_LONG | BAD_SEGMENT`, nothing trimmed or lower-cased),
  `NOT_A_TASK {field}`, `NODE_NOT_FOUND` (missing or unreadable, deliberately the
  same), `BAD_USER_INPUT {field: validateBy, reason: VALIDATION_TASK_REQUIRED}`,
  `CONFLICT`.

## 3. What the CLI does

### 3.1 Reading

`config get <memoryRef>` sends the canonicalized memory ref; the server resolves
it. Exit 4 is never rendered as "no rules": the refusal and the empty config
are different facts, and only the second is the server's claim.

A reference renders by its **state**, never by a guessed URN. BROKEN ("the node
was deleted; a config manager must repoint it") and UNREADABLE ("it exists but
you may not read it") get different text because their remedies differ. The
human output is a labelled block per rule: too many fields, and URNs too long,
for a table row.

### 3.2 Writing

- **Only the flags given change.** Presence comes from cobra's `Changed`, never
  from a flag's value, so `--enabled=false` and `--strict-sub-roles=false` are
  values, and an omitted flag is omitted from the wire (`omitempty` on every
  optional input field; findings:null-vs-omitted-args).
- **An empty value is a clear on `update` and a usage error on `add`** (nothing to
  clear). `--writers ""` is a usage error on both: writers cannot be cleared.
- **Refs are canonicalized and validated locally (`cmdutil.BatchNodeRef`), then
  resolved by the server.** No `resolveUrn` round-trip: the server resolves the
  ref itself, conceals an unreadable node as missing, and has no creation lag.
- **The role is sent verbatim.** The grammar is the server's (#1322), and a
  client-side trim would be the silent repair #1826 rules out.
- **`update` and `rm` read the config first.** That one read yields the rule's id
  (role → id) and its revision, and the write carries that revision. A rule
  changed in between is refused, exit 5, rather than overwritten or removed
  unseen. This is a read-then-guarded-write, not a read-modify-write: nothing
  read is written back.

### 3.3 Clearing `validateBy` in one update

The server clears `validateBy` only on an explicit `null`. A genqlient pointer
field cannot send one: `omitempty` drops nil, and without `omitempty` every
other update would clear it. So a second operation,
`UpdateNodeRoleRuleClearingValidateBy`, writes the `null` as a literal and passes
every other field as a variable in the same input object:

```graphql
input: { validateBy: null, writers: $writers, authorTaskRef: $authorTaskRef, … }
```

An `omitempty` variable left unset is **missing**, and GraphQL coerces an input
field whose variable is missing to **absent**, not null. Measured with the
server's own graphql-js 16.13.1:

| variables sent | the resolver's `input` |
|---|---|
| `{}` | `{validateBy: null}` |
| `{w: "ADMIN"}` | `{validateBy: null, writers: "ADMIN"}` |
| `{a: "", e: false}` | `{validateBy: null, authorTaskRef: "", enabled: false}` |

So a clear plus any other change is **one** update under **one** revision check,
never a clear and a second save (team chat #1888). The command picks this
operation only when `--validate-by ""` is given.

### 3.4 Exit codes

Three new `codeForExtension` rows, each measured falling through to the generic
1 on `c9fa75a`: `NODE_ROLE_RULE_EXISTS` → 5, `INVALID_NODE_ROLE` → 2,
`NOT_A_TASK` → 2. `RULE_LOCKED` → 8 is deliberately **not** mapped yet: nothing
returns it until #1334, and a row for an unobservable code documents an exit no
caller can see.

### 3.5 Warnings

`add`/`update` print `{rule, warnings[]}`. A warning never changes the exit
code; in the human branch it goes to stderr. Today the server returns none.

## 4. Schema refresh check

Regenerating from #1360 added `NodeFilter.role` to an input the CLI already
sends, with a bare `json:"role"` tag, so every `nodes` listing now sends
`role: null`. Checked against the server: its filter applies `role` only when
`f.role != null`, so null is the same as omitted. No regression, and read-path
filters don't need `omitempty`.

## 5. Decisions a reviewer should see

- **An empty flag on `update` CLEARS.** That is the server's contract (a
  reference sent as `""` clears it), and it has repo precedent (`memory set
  --schema ""`). The hazard, per review:an-empty-flag-is-not-an-absent-flag, is
  `--author-task "$VAR"` with `$VAR` unset: on `update` that clears the
  reference. It is kept deliberately. The alternative, a separate
  `--clear-author-task`, doubles the flag surface for one spelling. The result
  prints the rule as saved, so a clear is visible. On `add` an empty value is
  refused, before any request, naming the likely cause (an unset variable).
- **`createdBy` / `updatedBy` are user ids, not labels.** The server returns ids
  only (Q8), and an id is the actionable ref, so there is no display label to
  outgrow (review:entity-fields-not-display-labels). Widening either key to an
  object later would be a `--json` break, and is not planned.
- **An impersonated or unauthenticated write exits 1.** The server throws an
  untyped `Error('Forbidden')` / `Error('Unauthenticated')` there, which reaches
  the CLI as `INTERNAL_SERVER_ERROR`. That is the server-wide pattern (62 sites),
  not part (b)'s. There is no code to map, and matching the prose would break
  `the-error-code-is-the-contract-the-prose-is-not`. Left on the default.
- **Nothing here has been observed on a live server**, since #1360 is neither
  merged nor deployed (review:a-recommended-field-must-be-seen-carrying-a-value).
  Every field is exercised against fixtures shaped from the server's own
  resolver, and §7 adds a live read before this is marked ready.

## 6. Tests

`internal/cmd/memory_config_cmd_test.go`, against the fake GraphQL server:

- A1: the empty config (`rules: []`, `id: null`, "no rules", exit 0); the
  non-manager's concealed not-found (exit 4, never "no rules").
- A2: all four reference states, JSON and text; no URN beside BROKEN or
  UNREADABLE, and the two render differently.
- A3/A6: `NODE_ROLE_RULE_EXISTS` 5, `NOT_A_TASK` 2, `NODE_NOT_FOUND` 4,
  `MEMORY_NOT_FOUND` 4, `VALIDATION_TASK_REQUIRED` 2, `INVALID_NODE_ROLE` 2.
- A4: only given fields on the wire; update under the rule's id and the revision
  read.
- A5: an empty ref is sent as `""`; a false bool is sent as `false`.
- Clearing `validateBy`: the literal-null operation alone, carrying exactly the
  other given fields, and never beside the ordinary update.
- A8: `rm` without `--yes` sends no delete; with it, deletes under the revision
  read. A10: `CONFLICT` → 5 on update and on rm.
- A13: a warning is reported and does not change the exit code. A15: every array
  renders `[]`.
- Local refusals before any request: an empty ref or validator on `add`, an
  unknown `--writers`/`--validate-by`, an unqualified or single-colon ref,
  `update` with no flags, `--writers ""`.

`TestMemoryConfigClearingOperationCarriesLiteralNull` asserts the generated
**operation text**, field-exact: the clearing document carries
`validateBy:null`, and the ordinary update does not. The variable assertions
cannot see a literal (review:assert-the-query-not-the-capture). Deleting the
literal from the `.graphql` and regenerating turned this test, and only this
test, red.

Mutation-checked: 13 compiling mutants, all red. They cover the literal null
deleted from the document, never using the clearing operation, omitting an empty ref, revision 0, BROKEN rendered as
UNREADABLE or as "—", a dropped exit row, skipped confirmation, `rules: null`,
the clearing operation dropping a field, a trimmed role, dropped warnings, and
`--enabled=false` omitted.

## 7. Before merge

1. #1345 → #1360 merged.
2. `make schema` from #1360's merge commit, then `make generate`, plus a diff
   check against the candidate snapshot.
3. The full suite.
4. A read-only `memory config get` against a server running the merge: once on
   a memory you manage (the empty config or its rules), and once on one you do
   not (exit 4).
5. Mark ready and re-request reviews on the final head.
