# `coding review run` — buckets the checklist against a diff

Design-as-built for the second half of **#551**, on top of
[`coding-resolves-its-memory-from-the-repository.md`](coding-resolves-its-memory-from-the-repository.md).

## The decision that shaped it

#551 asks `run` to *"evaluate each checklist edge's `Applies when …` trigger"*
and return only applicable checks. Triggers are English, this binary has no
model, and **`bodytrigger.go` had already settled the neighbouring question the
other way**: condensing a scope paragraph into a trigger is *"a judgement call
the linter hands over rather than makes."* Building the issue as literally
specified would have reversed a recorded decision silently, so it went to Holger.

**Ruling (2026-09-05): structural filtering only, three buckets, and never
exclude on a guess.** The asymmetry is the whole design —

> a false positive costs the reviewer a skim; a false negative ships the defect.

| bucket | meaning | returned? |
| --- | --- | --- |
| `matched` | a concrete path the check names was changed, with the (pattern, file) pair that fired | yes, in full |
| `undecided` | the check names no paths, so nothing structural can speak to it | **yes, in full** |
| `excluded` | the check names paths and the diff touches none | no — but listed with its patterns |

Only the third removes anything, and only on positive evidence.

## Two layers of evidence, because inclusion and exclusion differ

Extracting patterns from backticked spans is easy to get *nearly* right and
dangerous to get slightly wrong: every admitted pattern can push a check into
`excluded`. So there are two gates.

**`looksLikeAPath`** admits a span only if it is a glob (`*.graphql`), carries a
known source extension (`main.dart`), ends in a slash
(`internal/api/queries/`), or is a slash-separated path whose every segment is
at least two characters. `--json`, `WORKER_TAKEN` and `cor:agt:020:03` are
rejected.

**`strongPattern`** then decides what may *exclude*. A trailing slash, a glob or
a file extension is unmistakably a path — nobody writes `*.graphql` by accident.
A bare `word/word` is not: `and/or` is structurally identical to `internal/api`,
and telling them apart needs a dictionary, which is the prose interpretation
this design refuses. **Weak patterns can therefore match, but a check whose only
patterns are weak comes back `undecided`.** The asymmetry applied to the
evidence, not only to the verdict.

## Running it found the bug the fixtures did not

The unit tests passed. Then the command was pointed at the real 34-check corpus,
and it **excluded `review:a-new-meaning-for-an-existing-glyph` on a path called
`n/a`** — extracted from the check's own *"Pass / fail / n-a"* section, which
every check in the corpus ends with.

That is the exact false exclusion the file is built to prevent: a phantom
pattern, matching nothing, silently dropping a check that had no path criterion
at all. It survived because the fixtures were written by the same person who
wrote the matcher, and neither of them had ever seen a real check's footer.

The minimum-segment-length rule closes it, and `strongPattern` closes the class
it belongs to.

## The honest yield

Measured across the four corpora that have checklists, with an empty diff (so
the excluded count is exactly the number of checks naming a strong path):

| corpus | checks | name concrete paths | left to the reader |
| --- | --- | --- | --- |
| hadron-cli | 33 | 3 (9%) | 30 |
| **mm-app** (the issue's own repo) | 39 | 7 (17%) | 32 |
| hadron-server | 30 | 1 (3%) | 29 |
| hadron-portal | 53 | 0 (0%) | 53 |

**So the filtering half of #551 delivers modestly**, and that is worth stating
rather than discovering later. These checklists describe CODE SHAPES — "a guard
verified by mutation", "a claim outrunning its evidence" — far more often than
file locations, and no amount of structural matching reaches those.

What does deliver for 100% of checks is the other half: **one response carrying
the bodies.** #551 measured that cost directly — the reviewer selected checks,
fetched each node individually, and a multi-node read exceeded the surrounding
agent's output budget. That is removed regardless of bucket.

**If the filtering is to earn more, the corpus has to say more** — a structured
`appliesTo` on the nodes (option 3 when this was scoped) would raise the yield a
lot, at the cost of a migration across four memories and a change to the
authoring task. Not proposed here; raised so the number above is not mistaken
for the ceiling of the idea.

## Shape of the output

`--json` is an OBJECT, unlike `review list`'s array — a different command, so no
existing consumer moves, and the scope, counters and buckets have somewhere to
live. It carries `memory` + `memorySource`, the diff's provenance
(`base`/`head`/`diffSource`), `changedFiles`, `total`/`returned`/`nextOffset`,
the checks with `content` and `matchedOn`, the `excluded` list with its reasons,
and `unavailable`.

`nextOffset` is non-nil **only** when checks were withheld, so a caller can tell
"that is everything" from "there is more" without comparing counts — including
the exact-fit case a naive `offset+limit < total` gets wrong.

`portalUrl` is the server's own link, carried through from `NodeBatch` (which
already selected it) and **omitted when absent**. Never composed from the URN:
the repo's rule is that a node read prints its URL and you copy that.

## Tests

Four mutations on the matcher, each confirmed applied and compiling first: no
patterns meaning excluded rather than undecided; any backticked span counting as
a path; substring matching instead of segment boundaries; and the body scope not
being read at all. Each red on exactly the intended tests.

Both directions verified live against the real corpus: a diff with no `.graphql`
excludes the three `.graphql`-scoped checks, and a diff touching
`internal/api/queries/team.graphql` matches all three **with the pattern/file
pair as evidence**.

One harness note worth keeping: the corpus measurement above first came back as
a JSON parse error, and the output was fine — **zsh's `echo` interprets `\n`
escapes without `-e`**, so piping the response through `echo "$out"` turned every
escaped newline into a real one. Third zsh idiom to misfire in this repo's
tooling; the memory on it now says the class is "bash idioms degrade to silence
here", and this one at least failed loudly.
