# `coding` resolves its memory from the repository

Design-as-built for the first half of **#551**. The second half — `coding review
run`, which reads a diff and buckets the checklist against it — is a separate PR;
this one removes the discovery friction underneath it.

## What #551 measured

Reviewing `micromentor-team/mm-app#1629`, the path from *"review this repo's
diff"* to *"read the applicable checks"* took four steps before any checklist was
loaded:

1. `config list --json` had no memory configured;
2. `coding review list` required `-m`, so the repository could not identify its
   own memory;
3. `memory list --json`, then scanning a cross-organization result;
4. inferring `hrn:mem:micromentor.org:mm-app` from the git remote **by eye**.

Step 4 is the tell. **The repository already knew which memory it belonged to;
nothing was asking it.**

## The chain

Four sources, first hit winning:

| # | source | network |
| --- | --- | --- |
| 1 | `-m/--memory` | no |
| 2 | `memory` / `coding.memory` in `.hadron/config.json`, searched upward | no |
| 3 | the configured memory (`hadron config set memory …`) | no |
| 4 | the git remote's repository NAME, matched against readable memories | yes |

`.hadron/config.json` is the file `chat` already reads, so a repo configures
itself once. This only adds keys.

### Two decisions inside step 4

**The match is slug equality, never fuzzy.** A near-match that resolved silently
would run the review against another team's checklist and look exactly like
success — a wrong answer with no artifact to be suspicious of. `widget` does not
resolve `widget-app`. Anything short of an exact match falls through to a
refusal that lists candidates, which is what #551 asked for: an ambiguous repo
should cost one `-m`, not a full `memory list`.

**A failed lookup is not an answer about the memory.** The step is
opportunistic, so a client that will not build (no token) or a query that does
not come back (server down) falls through to the ordinary usage refusal.
Returning it would hand a signed-out reviewer `AuthRequired`, and an offline one
exit 7, **for a question entirely about their arguments** — the defect #556 fixed
by hoisting a guard above the client build, and the one `alreadyBoundError`
records the mirror of. Nothing is hidden: if the network really is the problem,
the reader passes `-m` and meets the identical failure one line later, at the
read that needed it.

**The existing test suite caught this**, which is worth recording: the first
version surfaced the lookup error, and `TestCodingLintRequiresMemory` — written
for #533, about a completely different rule — went red with *"should be a usage
error, got 7"*.

## Reporting the source, per the checklist that governs it

`review:ambient-scope-must-report-its-source` applies to this diff by its own
scope line, and it sets the requirements rather than commenting on them:

- The resolved memory **and the branch that answered** print before the payload.
- The line survives an **empty** result — the node calls that the worst case,
  not the mildest, because with zero rows there is not even data to cross-check
  the scope against.
- `--json` **does not move**. `coding review list --json` is a top-level array;
  adding a scope field would make it an object, which is a break for every agent
  parsing it. The line goes to **stderr**, so a human sees it, a pipeline
  ignores it, and an agent that wants it can read it.

One deliberate departure: **it stays quiet for an explicit `-m`.** The check
exists because the same bare command in two checkouts does different things with
identical output, and that reasoning does not apply to a value the reader typed
on the line they are looking at. Narrating it is noise; the mutation that makes
it narrate anyway reds a test written for exactly that.

## What had to move with it, and would have rotted quietly

`-m` was `MarkFlagRequired` on all seven subcommands (#533). Unrequiring it is
three edits per site, and two of them are prose:

- **Five copies of a comment saying "there is no fallback".** True when written;
  the exact sentence this change falsifies. Replaced rather than left beside the
  new behaviour — `review:sweep-the-subject-not-the-removed-rule`.
- **Seven usage strings ending `(required)`.** This is not cosmetic: #533
  measured that cobra's help template does **not** annotate required flags at
  all, so the parenthetical was the *only* thing telling a reader the flag was
  compulsory. Leaving it would have gone on saying so after it stopped being
  true, with nothing to contradict it.
- **A test asserting `memory` appears among the missing required flags.** It
  encoded the old rule; `memory` leaving that list is the change. The batching
  property #533 wanted is untouched — both writers still have two required flags
  and still report them together.
- `codingMemoryURN` became unused. Rather than deleting it, its construction
  collapsed into `newCodingMemory`, which the resolver already needed four
  times. Its refusal moved to the resolver, which is now the only thing that can
  say a memory is missing: **emptiness stopped meaning "the caller forgot" the
  moment three other sources could answer.**

## Also fixed: the help text #551 called out

`hadron coding --help` said `tasks:review-changes` triages the checks — and the
issue found that node absent in `mm-app`, so the documented entry point was not
universally available. The group help now says triage is the reader's, names the
task node as a convenience, and states explicitly that nothing here depends on
it existing.

## The fallback made an old input dangerous

`review:an-empty-flag-is-not-an-absent-flag` fired on this diff, and the
interesting part is that **the input did not change — its consequence did.**

Cobra records `-m ""` as CHANGED with an empty value. Before #551 that met the
required-flag refusal and stopped. With a fallback behind it, the same input
silently resolves a **different memory** from the repository and reports it as
though the reader had asked for it — the substitution the check exists to
prevent, arriving not because anyone touched that path but because something was
added underneath it.

So the guard ships with the fallback rather than after it: `Changed` is the only
thing that can tell an empty value from an absent flag, which is precisely why
the emptiness test alone cannot.

## Tests

Seven mutations, each confirmed **applied and compiling** before its result was
read, each red on the intended tests: the project config no longer beating the
configured memory; `coding.memory` no longer beating the general key; a lookup
failure surfaced instead of becoming usage; ambiguity picking the first match;
narrating an explicit `-m`; never narrating at all; and dropping the empty-flag guard.

The resolver's sources are **injected** (`memorySources`) rather than reaching
for the filesystem, git and server directly. That is not only for isolation: the
config walk continues to the filesystem **root**, so a stray
`.hadron/config.json` in a home directory would answer for every repository
under it — and, in a test, would silently change which branch the assertions
were exercising. `projectCodingConfigFrom` takes the directory to start from for
the same reason.

## Not in this PR

`coding review run` — reading the diff and bucketing checks into
matched / undecided / excluded. Holger's call on that shape (2026-09-05):
structural filtering only, and **never exclude on a guess**, since a false
positive costs a skim and a false negative ships the defect.
