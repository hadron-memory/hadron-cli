# Design as built: the false zero — `--where`'s column and the listing's projection (#602, #603)

Two issues, filed an hour apart off the same session, and they are two halves of
one defect. Fixing either alone leaves the failure intact, which is why they
were taken together.

## The defect

The coordinator measured whether `hadron chat`'s `--identity` envelope was still
in use, to decide whether the flag could be deprecated. The measurement returned
**zero**, and the conclusion — "the envelope is dead" — was an inch from being
published as a finding that would have deprecated two flags.

The corpus was not empty. Both halves of the instrument were broken, and both
point the reader the same way:

| | |
|---|---|
| **#603** | `--where` reads the `properties` JSONB column unless a leaf sets `"field":"data"`. A node's envelope lives in `data`. The obvious spelling therefore asks the wrong column and matches nothing. |
| **#602** | `node ls` **filters and sorts** on `data`/`properties` and **cannot return them**. So even a *correct* predicate hands back rows whose `data` the listing will not show — there is nothing to notice the mistake against. |

The dangerous part is the shape of the failure. Not an error, not an
empty-with-caveat: a plain `0`, which reads as evidence and gets published as
one. Two people hit it within an hour; the only thing that separated a report
from a finding was running a **positive control** on a key already seen with
`node get` — asking for `data.authorName`, getting `0` for that too, and
recognising an instrument fault rather than an empty corpus.

## #603 — say the default, and speak up on the silent zero

**The help was actively misleading, not merely thin.** It read *"structured
predicate over properties/data"*, which a reader takes to mean the predicate
searches **both** — and the example it gave omitted `field` entirely, so copying
it aimed the predicate at `properties` without ever surfacing that there was a
choice. The `field` key was discoverable only from the GraphQL SDL, which a CLI
user has no reason to read.

Both flag strings now live in `cmdutil` (`WhereFlagUsage`,
`SortPropertyFlagUsage`) so the two commands offering them cannot drift, and
both name the default column and show `field` in the example.

Help alone does not reach someone who already learned the short form, so the
silent zero itself is annotated. `cmdutil.WhereDefaultColumnNote` fires on
exactly one shape:

- a `--where` was given, **and**
- the predicate **matched** nothing — see below; this is not the same as the
  result being empty, **and**
- **no leaf anywhere in the tree** named a column.

The third clause is "any leaf", not "every leaf", on purpose: a predicate mixing
an explicit `"field":"data"` leaf with a bare one was written by someone who has
met the key, and warning them is noise. Noise is how a useful warning gets
ignored.

### It counts what the predicate MATCHED, not what you were shown

Caught by @codex in review, and it is the same defect class as the one being
fixed. The note was keyed off the length of the result slice — which is the
count *after* `--offset`, `--limit` and `node list`'s client-side `--seq-gt`
have narrowed it. So `--where <field-less> --offset 500` over a 7-row match
displays nothing, and the note fired: *"the properties predicate matched
nothing, try data"*, to someone whose predicate was fine and whose paging was
not. A confident wrong answer about their data — this note's own failure mode,
aimed the other way.

The obvious source is `findNodes.total`, and it is **not usable**. The field is
nullable and this server leaves it null even when rows match — measured live on
both the browse and the ranked path, *after* a first version of this fix was
built on it. Every test passed, because the fakes supply a total; the note was
simply dead on `node list` against the real server. That is this repo's standing
lesson arriving on schedule: **run the thing.**

So the row count is all there is, and each caller decides whether it answers the
question — it does exactly when nothing narrowed the result after the predicate:

| path | trustworthy when |
|---|---|
| `node list`, seq mode (`--seq-gt` / `--sort-seq`) | **always** — it pages to exhaustion under its own offsets, so the fetched set is the whole match set and `--seq-gt` / `--limit` / `--offset` are applied client-side afterwards |
| `node list`, otherwise | `--offset` is 0 — the server did the paging, so an empty page past the last row says nothing |
| `search` | `--offset` is 0, same reason |

`--limit` disturbs neither arm: it cannot empty a non-empty match, so zero rows
under a limit really is zero matches.

Where the count cannot answer, `matched` is nil and the note stays **silent** —
deliberately rather than defensively. The failure modes are not symmetric:
silence leaves the caller with the bare zero they already had, while a guess
leaves them with a note that may be false, and a warning that fires wrongly is
how a reader learns to ignore the one that is right.

### The note goes to stderr in `--json` mode too

This deliberately differs from `search`'s neighbouring degraded note, which is
text-only. That one can be text-only because `--json` already carries
`degraded`/`reason` **inside the envelope**. This one has nowhere to go: `node
ls --json` marshals a bare array, and adding an envelope to it would be a
breaking change to a contract agents parse.

So suppressing it under `--json` would leave an agent holding precisely the
unqualified `0` the note exists to qualify — and agents are the callers most
likely to turn one into a published finding. stderr keeps the stdout contract
untouched while the caveat still arrives.

## #602 — project the column you just filtered on

`--with-properties` / `--with-data` on **`node list` and `search`**. Named after
the two columns `--where`'s `field` key names, because the symmetry *is* the
fix: a command that lets you ask about a field should let you see it — and both
commands offer `--where`, so both owed the answer.

(This shipped on `node list` first, with `search` raised for the coordinator
rather than widened unilaterally. @Holger dispatched it directly.)

**Opt-in**, per the issue's own preference. A listing is a thin index, and a
500-row chat listing with full envelopes is a much larger payload. Not projected
unconditionally, and not projected implicitly whenever `--where` is passed —
output shape changing because of an unrelated flag is a different surprise.

**Two independent GraphQL variables**, not one toggle, so `--with-data` fetches
`data` alone and nobody pays for a column they did not ask for.

### The three states, and why the DTO field is not a pointer

This is the part that was designed wrong first and corrected by measurement.

The key has to distinguish three states:

| | rendered as |
|---|---|
| not requested | key **absent** |
| requested, has a value | key = the value |
| requested, column is null | key = `null` |

A `*json.RawMessage` **cannot express the third**. `encoding/json` sets the
pointer to nil for a JSON `null` — measured against the real decoder, not
assumed — so a requested-but-null column would vanish from the payload exactly
as an unrequested one does. A caller who asked for `data` and got no `data` key
could not tell *"this node has none"* from *"you did not ask"*.

That is #602's own defect reintroduced inside its own fix, and it is a lesson
this repo had already paid for once: `nodeDetailDTO.AbstractOriginHash` carries
a comment explaining that **`jq` returns null for a key that is not there**,
indistinguishable from a key whose value is null (#306).

So the field is a **non-pointer** `json.RawMessage` with `omitempty` — the same
omit-vs-null mechanism `gqltypes.NodeWhereInput` already documents for its
operands: nil (len 0) is dropped, the 4-byte `null` literal survives. And
because an unselected column and a selected-null one are *indistinguishable on
the wire*, the **flag** decides what is present, never the returned value.

### Text output

Either flag replaces the table with a per-node block. A table column would have
to truncate an arbitrarily long JSON value, which is the same defect as not
showing it, in a costume that looks like an answer. A requested-but-null column
prints `null` rather than being skipped, for the reason above.

## Scope, and what was deliberately left alone

- **`node get` is untouched.** It already returns both columns; #602 is specific
  to the listing, and `nodeDTO` was not extended (a new `nodeListDTO` embeds it)
  so `node get`'s payload cannot move.
- **The default listing's payload is byte-identical.** Verified against the live
  server: the key set is exactly `id, isRunnable, loc, memoryId, name, nodeType,
  seq, tags, updatedAt`, which is what #602 measured. The flags are additive.
- **`object find --where` gets neither half.** Its predicate goes to
  `findObjects`, whose objects **are** `properties` by construction ("the object
  store's flat projection of a node") and which already returns those fields
  flat. Defaulting to `properties` is correct there, so the note would be a
  false warning and the projection is already present.
- **`search` carries both halves**, on @Holger's dispatch. One extra design
  question there: `--long` and the projection flags reach the same block
  renderer but ask for different things, so each half is gated separately.
  `--with-data` alone prints the columns and NOT abstracts — a flag that quietly
  turns on a neighbour's output is the "output shape changed because of an
  unrelated flag" surprise this change declined to introduce elsewhere.
- **`rawOrNull` became `cmdutil.ProjectedColumn`** once a second command needed
  it. The three-state rule is subtle enough that two copies would drift, and the
  helper deliberately does NOT take the flag — so every call site has to decide
  for itself that the column was requested, which is the decision that must not
  be made from the wire value.

## Verification

Unit-tested where the logic is pure (`cmdutil`), command-tested against the fake
server, and **run against the live server**, because the standing lesson in this
repo is that neither of the first two catches a guard that never fires.

Every test was mutation-checked, and each mutation was confirmed to **build**
first — a non-compiling mutant reports zero failures, which reads exactly like
"the test did not catch it". Two of those mutations are worth naming.

Keying the DTO off the wire value instead of the flag — the design above, before
it was corrected — is caught by the test written for that exact case.

Two mutations were **not** caught, and both were more useful than the ones that
were. Each exposed a test that agreed with the code for the wrong reason:

- Deleting the seq path's match count left every seq test green. It had to —
  with no count the note can never fire, and the test there asserted it does
  *not* fire. Fixed by a **positive** seq case: a genuine zero must still be
  qualified.
- Narrowing `seqMode || offset == 0` to `offset == 0` also left everything
  green, because that positive seq case runs at offset 0 and passes either way.
  Pinning the arm needs seq mode **with** an offset — where the offset is
  client-side, so the zero is real and the note must still fire.

Neither was reachable by re-reading the tests; the mutation is the only thing
that said so.

The live run is what caught the larger one. Seven `node list` cases and two
`search` cases, each asserting WARN or QUIET, against the real server — and the
first attempt at that harness measured nothing at all, because an unquoted
`$P=(--prefix …)` does not word-split in zsh, so every case reported the same
row count. A verification that cannot vary is not a verification.

Live, against `hrn:mem:hadronmemory.com:hadron-dev-team-shared`:

```
--where '{"path":["authorName"],"exists":true}'                 -> 0 hits + the note on stderr
--where '{"field":"data","path":["authorName"],"exists":true}'  -> 2 hits, quiet
```

Which is the original finding, now with the instrument telling you which of the
two you are looking at.
