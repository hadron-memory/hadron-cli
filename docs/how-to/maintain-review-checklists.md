# How to maintain review checklists and the preflight router

`hadron coding` treats the `review:*` checklist tree and the `preflight` router
as **executable infrastructure** rather than prose, because that is what they
are:

- `tasks:review-changes` discovers a check by reading the label on its edge back
  to the `review` parent — `Applies when a .graphql file changed` — and matches
  that text against the diff under review.
- `preflight` routes symptom → finding along its outgoing edges, scanned as a
  table of contents.

When one of those edge labels is empty, or says `child-of` instead of a
condition, the check is **silently skipped**. The node still exists, still looks
maintained, and never fires again. Nothing in a code diff shows it. That is what
this command group detects, reports, and (for the mechanical subset) repairs.

Every subcommand takes `-m/--memory <org::memory>`.

```
hadron coding review    list | create | lint
hadron coding preflight list | lint
```

## What counts as a check

A node is a checklist item when it sits **under the review parent's loc prefix**
(`review:` by default) and is **neither tagged `meta` nor runnable**.

That rule is doing real work. The `review` parent legitimately has non-checklist
neighbours — the router, the tasks that consume the tree, a meta backlog,
pattern nodes, findings — and linting every inbound edge produced more false
positives than findings on the first live corpus. A `review` *tag* is not the
rule either: a third of real checks carry no tags at all, and findings nodes
carry the tag because they are review-relevant. See
[docs/plans/coding-command-group.md](../plans/coding-command-group.md),
Decision 1.

`--root <loc>` points every subcommand at a differently-named parent; the
membership prefix follows it.

## See the checklist

```sh
hadron coding review list -m hadronmemory.com::hadron-cli
```

```
CHECK                                 SEQ  STATUS  TRIGGER
review:canonical-ref-handling         —    ok      Applies when a command resolves/composes a memory or node reference
review:stable-json-dto                —    ok      Applies when adding or changing a command's --json output
```

The `TRIGGER` column is the edge label — the text a diff is matched against, not
the node's description. `STATUS` is the linter's verdict on that row: `ok`,
`broken` (an error-severity finding), or `unavailable` (the node listed but
could not be read — surfaced rather than dropped).

`--broken` narrows to what needs attention, which is the triage view:

```sh
hadron coding review list -m micromentor.org::mmdata --broken
```

```
CHECK                                        SEQ  STATUS  TRIGGER
review:input-type-graphql-type               40   broken  —
review:role-vs-group-ident-vocabulary        104  broken  child-of
review:posthog-backend-vs-app-event-routing  105  broken  child-of
```

`list` is read-only: a broken check is a row, not an exit code, so a listing
full of them still exits 0 (a usage, auth or not-found error exits non-zero as
everywhere else). `lint` is the command whose exit code reflects findings.

The router has the same pair:

```sh
hadron coding preflight list -m hadronmemory.com::dev
hadron coding preflight list -m hadronmemory.com::dev --broken   # dead routes only
```

## Add a check

```sh
hadron coding review create thin-resolver-field -m acme.com::kb \
  --trigger "adding or modifying a GraphQL resolver" \
  --description "Resolver fields stay thin — applies when adding or modifying a resolver." \
  --link conventions:graphql-codegen \
  --tag graphql
```

- The node and the edge that makes it discoverable are written in **one
  mutation**, then read back to confirm the edge landed. Creating them
  separately is how checks end up invisible; if the edge is missing anyway the
  command reports it and exits 1 (a partial write), never 0.
- `--trigger` is the condition. `Applies when` is prepended if you leave it off,
  so `"a resolver changes"` and `"Applies when a resolver changes"` produce the
  same label. A bare stem is rejected.
- `--description` is required — list and search output show the description, so
  a check without one is invisible there.
- The body is scaffolded Scope-first: a `> **Scope.**` blockquote (from
  `--scope`, or derived from the trigger) that lets a reviewer skip the check
  without reading it, then `TODO(...)` sections to fill in. `--content` /
  `--content-file` supply your own instead.
- `--link <node-ref>[=<label>]` cross-links the canonical `conventions:*` /
  `findings:*` node that explains the rule in full (default label `details`).
  Prefer that over adding a `preflight` route: preflight is a lean action-router
  scanned on every change, and per-check routes bloat it.
- Tags always include `review` and `review-criteria`; `--tag` adds more.

A check created this way passes `coding review lint` as written.

## Lint

```sh
hadron coding review lint -m acme.com::kb
hadron coding preflight lint -m acme.com::kb
```

| Rule | Severity | Catches |
|---|---|---|
| `parent-edge-exists` | error | check invisible to `tasks:review-changes` |
| `label-present` | error | an empty edge label |
| `label-is-condition` | error | `child-of`, `applies-when`, `related`, a bare `Applies when` |
| `check-node-resolves` | warning | an endpoint that listed but could not be read |
| `description-present` | warning | a second blind spot, in list/search output |
| `duplicate-trigger` | warning | a cloned check that was never re-pointed |
| `seq-unique` | warning | non-deterministic sibling ordering |
| `foreign-toolchain` | warning | a Dart trigger in a TypeScript memory |
| `route-target-resolves` | error | a route to a node that can't be read |
| `route-label-phrasing` | warning | a route not phrased as an action (`to do X …`) |
| `route-target-retired` | warning | a route to a retired/superseded node |
| `route-target-moved-memory` | warning | a route that left the router's memory |

**Errors exit 5** (`exitcode.Conflict`, as `spec lint` does); warnings alone
exit 0. `--strict` promotes warnings to errors. That split is deliberate: a
third of a healthy router fails the action-phrasing convention, and a linter
that turns the build red for it gets ignored.

Useful knobs:

- `--suggest` prints the body's scope paragraph in full instead of truncated.
  Label findings already quote it — when a label goes missing, the text that
  belongs there is usually a few lines away in the node body.
- `--toolchain ts` pins the toolchain heuristic (`-` disables it). Left alone it
  is inferred from the whole member corpus and stays silent when no family wins.

## Repair

```sh
hadron coding review lint -m acme.com::kb --fix --yes
```

`--fix` handles exactly one mechanical case: **promote the check's description
into an edge label that carries no condition**, when the description already
states the trigger. It never invents a condition, and it writes with
`updateEdge` — one edge, one field. It must never go through
`updateNode(edges:)`, which replaces a node's whole outgoing edge set and would
destroy the check's sibling `documented-by` / `relates-to` edges. (That hazard
is why the MCP tool surface cannot do this repair at all; the CLI can.)

Everything else is a judgement call and is reported for a human:

```sh
hadron edge update <edge-id> --name "Applies when <condition>"
```

The edge id is in `coding review list --json` (`edgeId`) and in `hadron edge
list <node> --direction outgoing`.

## Gate it in CI

Both linters emit `--json` and carry a documented exit code, which is enough to
gate a repo's memory hygiene. This repo does it in
[`.github/workflows/memory-hygiene.yml`](../../.github/workflows/memory-hygiene.yml):
nightly, gated on a `HADRON_TOKEN` secret, filing a de-duped tracking issue when
an error-severity finding appears.

It is deliberately **not** a pull-request gate. Memories are edited out of band
from the repo, so a break rarely coincides with a PR; a PR gate would mostly
no-op and would fail an unrelated PR when it didn't.

```sh
hadron coding review lint -m "$MEMORY" --json > review.json   # exit 5 on errors
jq 'map(select(.severity == "error"))' review.json
```
