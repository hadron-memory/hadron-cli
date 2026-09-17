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
- the result was empty, **and**
- **no leaf anywhere in the tree** named a column.

The third clause is "any leaf", not "every leaf", on purpose: a predicate mixing
an explicit `"field":"data"` leaf with a bare one was written by someone who has
met the key, and warning them is noise. Noise is how a useful warning gets
ignored.

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

`--with-properties` / `--with-data` on `node list`. Named after the two columns
`--where`'s `field` key names, because the symmetry *is* the fix: a command that
lets you ask about a field should let you see it.

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
- **`search` gets #603 but not #602.** The note and help apply — the reported
  reproduction was a `search --where`. Projection on a *relevance* surface is
  neither issue's ask, and widening scope unilaterally is not mine to do; raised
  for the coordinator instead.

## Verification

Unit-tested where the logic is pure (`cmdutil`), command-tested against the fake
server, and **run against the live server**, because the standing lesson in this
repo is that neither of the first two catches a guard that never fires.

Every test was mutation-checked, and each mutation was confirmed to **build**
first — a non-compiling mutant reports zero failures, which reads exactly like
"the test did not catch it". Twelve mutations, twelve caught, each by the test
that claims to cover it. The one worth naming: keying the DTO off the wire value
instead of the flag — the design above, before it was corrected — is caught by
the test written for that exact case.

Live, against `hrn:mem:hadronmemory.com:hadron-dev-team-shared`:

```
--where '{"path":["authorName"],"exists":true}'                 -> 0 hits + the note on stderr
--where '{"field":"data","path":["authorName"],"exists":true}'  -> 2 hits, quiet
```

Which is the original finding, now with the instrument telling you which of the
two you are looking at.
