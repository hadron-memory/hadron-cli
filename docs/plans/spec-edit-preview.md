# Design as built: `spec edit --dry-run` shows the change (cli#737)

> **Status: built in the cli#737 PR (Jane).** Assigned by Ada (team chat #2126),
> transferred from Jonas's queue. Jonas keeps cli#736 (raw reads for the `node`
> commands) and cli#738 (the original-revision guard on `spec edit` saves).

## 1. The problem, measured

- **The dry run showed that something changed, not what.** v0.17.0 printed
  `body: 71 → 71 lines` and `abstract: updated`. A human asked to approve an
  edit could not review it, and the agent had to fetch and diff it separately
  (Tove, docs#327, team chat #1985).
- **The read under it was rendered.** `spec edit` read through `GetNode`, the
  single-reference `node(ref:)` query, which compiles the body's Mustache unless
  `raw` is set. On production, `cor:agt:040:01` at revision 1 reads with
  **0** `{{` that way and **4** raw. So the interactive path opened the rendered
  body in `$EDITOR`, and any save wrote the placeholders away. Any preview
  would have diffed text that is not stored.

## 2. What the server provides

`node(ref: ID!, memoryRef: ID, raw: Boolean): Node`. `raw` skips
`compileTemplate` (`resolvers.query.node.ts`). It has been on the server since
`7b5f66d6` (2026-04-12) and is in the committed snapshot, so there is **no
server gap** and nothing is invented; the CLI simply never asked for it.

## 3. What the CLI does

- **One raw read for the edit:** `GetSpecNodeRaw` (`internal/api/queries/spec.graphql`),
  with `raw: true` as a **literal**, so no caller can forget it. It selects only
  what the edit uses. It is a new operation rather than a `$raw` variable on
  `GetNode` because that would change the signature for 19 callers in 12 files,
  including `node get`/`node update`, which are #736's. `spec get`/`lint` keep
  their current read; whether they go raw belongs to #736.
- **One proposal:** `editProposal` holds the stored and proposed body and
  abstract, plus whether the abstract is re-affirmed. `changes()` renders it and
  `input()` builds the write from it, so the changes a run reports are exactly
  what that run writes. A dry run and a later real run are two separate reads.
- **`--json` gains `changes[]`,** one entry per field written:
  `{field: content|abstract, change: replaced|cleared|reaffirmed, before, after, diff}`.
  `before`/`after` are byte-exact, and `diff` is a unified diff (`go-udiff`, a
  port of Go's internal diff with no dependencies, BSD/MIT). A re-affirm is
  metadata: the same text is re-sent, so `before == after` and `diff` is `""`.
  An abstract that is empty **or whitespace-only** is `cleared`, because the
  server stores it as null (#740 review, Copilot).
  It is additive; every existing key keeps its meaning. A no-op is
  `changed: false, changes: []`. A real edit reports the same `changes[]`.
- **The terminal dry-run** prints the summary lines as before, then the
  abstract-stale consequence (the reminder used to be suppressed on a dry run,
  but it is a consequence the reviewer should see before approving), then each
  diff **verbatim and unindented**, so it stays a diff, and finally: "nothing
  was written, and this preview is not an approval". A no-op dry run closes
  with the same line (#740 review, Copilot).
- **Zero writes:** the dry run returns before any mutation is built.

**Not in scope, and reported:** two sibling write paths in this group still
read the body rendered and write it back, so they delete placeholders too.
`spec supersede` always rewrites the retired spec's content (the stored body
plus the "Superseded by" note), and with `--copy-body` it copies the rendered
body into the successor. `spec extract --strip-source` writes a body computed
from the rendered source back to it. They are pre-existing, and the fix is the
same read; routed to the coordinator (team chat) rather than widened in here.

**Not in scope:** revision provenance. The preview carries no revision:
`GetNode` never selected one, and `node get`'s bracketed revision read (#724)
is the design #738 extends. The disclaimer says that applying recomputes
against the spec as stored at that moment, which is true today and is the gap
#738 closes.

## 4. Tests

`internal/cmd/spec_edit_preview_test.go`. Each test fails on `main`:

- the edit reads `GetSpecNodeRaw`, whose operation text carries `raw: true`;
  the editor gets placeholders and the save keeps them;
- a multiline content change with placeholders on the changed line and in
  context: `before`/`after` byte-exact, and the diff carries them verbatim;
- the text dry-run: the diff, the abstract-stale note and the not-an-approval
  line;
- abstract replaced and cleared; a re-affirm with no diff;
- a no-op: `changes: []` on the raw output;
- the preview matches the write: the same flags, run for real, send the
  previewed `after` texts and report identical `changes`.

Every dry-run test asserts **no operation beyond the two reads** was sent. The
old `TestSpecEditDryRun` checked `CreateSpecNode`, which `spec edit` never
sends, so it could not fail. It now uses the same assertion.

**Mutation-checked: 13 compiling mutants, all killed by their intended tests**
(11, plus 2 from the #740 review: the whitespace-only clear, and the no-op
disclaimer).
Each was applied from a committed checkpoint and confirmed to have landed
(a non-empty `git diff`, and `go build` passing) before its tests ran; the
checkpoint was restored after each.
They are: `raw: true` dropped; the write sending other text than previewed;
`changes` nil; before/after swapped; the dry run writing; no text diff; no
disclaimer; no re-affirm entry; "cleared" never used; no abstract-stale note;
the read back on `GetNode`.

**Live, read-only, on production:** `spec edit cor:agt:040:01 --content-file
<raw body with one line changed> --dry-run` printed the one-line diff with
`{{name}}`/`{{role}}` intact. `--json` gave `before`/`after` with 4 placeholders
each, and the node was still at revision 1 afterwards.
