# `spec new` adopts the provider allocator (cli#398)

Design as built. Refs #398, #579; hadron-server#1178 slice 1 (`allocateSpecNode`,
merged f4b78b28); cor:spc:020:13 (draft, unminted, Built slice 1), 020:06, 020:11,
cor:dmo:120:07, cor:api:240:01. This change mints no citation.

## What changed

The two number-allocating modes of `spec new` no longer read sibling numbers and
pick the next one client-side. The server picks it.

| Mode | Path |
|---|---|
| `--new-feature` | `allocateSpecNode(parentLoc = module)` |
| `--feature F` (no `--rule`/`--flow`), `--rule-after N` | `allocateSpecNode(parentLoc = F, after = N)`; `after` is a floor, it never lowers the start |
| `--rule`, `--flow`, positional `<loc>`, `--contract`, `--new-module`, `--new-product`, `--new-path` | unchanged: explicit loc, `createSpecNode`, no allocation |

## Protocol (`internal/cmd/spec/new_allocate.go`)

1. Everything that can fail on input runs first: the copied-title-prefix refusal
   (against the parent's shape, since the number is unknown), content and abstract
   reads, and edge-target resolution. A refusal here reserves nothing.
2. ONE `allocateSpecNode` call, never retried. It picks the number and creates the
   unminted placeholder in one transaction (the unique row is the allocator, so no
   "taken" refusal exists and none is handled).
3. ONE `updateSpecNode` fill on the returned id with `expectedRevision` = the
   returned revision. Name (`<loc> — <title>`), tags, role, data, seq, abstract,
   content and the ToC/inheritance edges all travel inline; the placeholder has no
   outgoing edges, so `edges` replaces an empty set. No `createSpecNode`, no
   `createEdge`, no client-computed number on this path.
4. A new feature's general-provisions contract (`<feature>:00`) stays an explicit
   `createSpecNode`: the allocator never hands out rule 00.

## Outcomes

| Event | Result |
|---|---|
| Allocation refused (`SPEC_PARENT_NOT_FOUND`, `SPEC_PARENT_NOT_NUMBERED`, `SPEC_NUMBER_SPACE_EXHAUSTED`, `BAD_USER_INPUT`, or schema skew) | `api.MapError`, zero further writes; skew is a plain refusal, there is no fallback |
| Allocation answer lost / unusable (no id, a different memory, revision <= 0, not a placeholder, not a numbered child of the requested parent at the expected tier, at or below the `--rule-after` floor) / error below `allocateSpecNode` | exit 7; no fill, no second allocation; names the parent and title and says to check `spec ls --prefix <parent>` before rerunning |
| Fill refused (incl. `NODE_WRITE_CONFLICT`) | exit 1; the number stays reserved; `--json` `{citation, allocated: true, fillStatus: "refused", reason, recovery}`; read the node first (another writer may have filled it), then the `recovery` commands restore title, abstract, body, tags, role, edges and a feature's contract |
| Fill answer lost | exit 7; `{allocated: true, fillStatus: "unknown"}`; an error below `updateSpecNode` is also unknown; read the node first |
| Feature filled, contract refused / lost | exit 1 / 7; `{citation, contractCitation, contractStatus, fillStatus: "filled", recovery}`; inspect the contract loc, never rerun |
| Blank `--content`/`--content-file` (an explicit empty value included) | usage error before any allocation |

Recovery commands are built from argument vectors and quoted with `cmdutil.ShellQuote` (a title, label or tag is one literal word when pasted); the contract command keeps the run's tags and `--no-edges`. A fill that may have committed says to read the node first and create only what is missing.

Both fill outcomes say not to rerun `spec new` (it would allocate another number)
and that an unfilled placeholder blocks the corpus's next mint (cor:dmo:120:07).
Definite-refusal vs unknown is `api.IsDefiniteRefusalAt(err, field)`: coded, and pathless or exactly at the mutation field (cli#2108); an error below the field follows the write.

## Not promised

A number is not reused *while a row or alias of it remains*. Under R219 = C a
minted node hard-deleted in place, leaving neither, can be allocated again; the
durable high-water record is a later slice. Help and docs say only the former.

## Decisions to review (not in the gate text)

- **`--dry-run`.** Allocation is a write, so a dry run cannot name the number and
  does not guess one. It reports `allocatesUnder` (and `afterFloor`) instead of a
  citation, and shows the scaffold against a `NNN`/`NN` placeholder. `citation` is
  empty in that JSON. This is the one `--json` shape change.
- **`spec extract`** also plans a rule through `planTarget` and still allocates
  locally; it is outside this gate's scope ("`spec new`") and keeps its behavior.
  `spec supersede` and `spec register` keep `allocateChild` for the same reason
  (supersede's allocator is the follow-up on #579).
- The matrix line `allocateSpecNode` flips to `command spec new` (Mutation 329
  command / 18 gap). #2791 also edits `internal/api/coverage-matrix.txt`; whichever
  lands second regenerates (`make coverage-matrix`).

## Tests

`internal/cmd/spec_new_allocate_398_test.go` (fake transport only; all eight red on
`main`): one allocation + one guarded fill, no create/edge; `after` sent only with
`--rule-after`; explicit modes send no allocation; each of the four refusals writes
nothing; refused/conflict/lost fills report the reservation without a second
allocation; a lost allocation sends no fill; schema skew has no fallback; the
feature contract stays explicit; a copied prefix is refused before anything is
reserved. Existing `spec new` tests were moved from create to allocate + fill.
