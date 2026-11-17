# Help review rubric

The standard every help page is judged against in the help audit (#826). It is
used with the command-tree inventory in [help-inventory.md](help-inventory.md):
the inventory says which pages exist and who has read them, this says what
"read" means.

Help is the product for a person who cannot read the source and for an agent
that only has `--help` and the contract text. Judge it as a person who has the
help page and nothing else.

## What a page must let a user do

For each page, a non-technical user must be able to:

1. **Identify the right command.** The one-line description and the group page
   say what the command is for, in words a user would use, and how it differs
   from its neighbours (`node update` vs `node move`, `memory rm` vs `node rm`).
2. **Understand prerequisites and defaults.** What must exist or be set first
   (sign-in, an active org, App or memory, a worker binding), and what happens
   when a flag is left out.
3. **Choose arguments.** What each argument and flag takes, with the accepted
   forms (id, URN, `<org>::<slug>`), a unit or example value where it is not
   obvious, and which flags exclude each other.
4. **Anticipate effects.** What is read, what is written, what is destroyed,
   what asks for confirmation, what needs `--yes`, and what other people or
   agents will see.
5. **Recognize success.** What the command prints or returns when it worked,
   and what `--json` adds.
6. **Recover from common failures.** What the likely errors mean and what to do
   next, including the exit code where the contract defines one.

## Flags to raise

Record a finding wherever you see:

- **Unexplained jargon:** a term (a loc, a URN, a lens, a ticker, a casting)
  used before or without being defined on the page or linked from it.
- **Assumed context:** the page only makes sense if you already know a workflow,
  a ticket number, or a convention that is not on the page.
- **Unclear active org / App / memory / scope behavior:** the page does not say
  which of these the command uses, how it is chosen, or how to override it
  (`--app`, `-m`, a bound worktree).
- **Incomplete examples:** an example that omits a required argument, uses a
  placeholder that is not explained, or has no output or effect described.
- **Guidance that cannot accomplish its stated goal:** following the page
  literally fails, or reaches a different result than the description claims.
  Run every example against the real CLI before passing a page.
- **Drift between the description, the flags and the behavior,** and text that
  is correct for one audience (maintainers, an old release) and wrong for the
  reader.

## Severity

- **P1:** following the page makes the user do the wrong thing or lose data,
  or the goal the page states cannot be reached from it.
- **P2:** a user of reasonable skill is likely to stall, guess or get a wrong
  result: missing prerequisite or default, undefined key term, example that
  does not run.
- **P3:** wording, order or polish that costs a moment but does not block.

A page with any P1 or P2 is `fail`.

## Assessment format

One assessment per page, saved where the child issue says, with:

- **Severity** (P1, P2 or P3) per finding.
- **Confusing wording:** the exact text, quoted.
- **Suggested revision:** replacement text, or the structure to change.
- **Missing supported workflow:** a thing the CLI can do for the stated goal
  that the page does not lead the user to. This becomes a linked follow-up
  issue, not a wording patch.

The page's flags are assessed inside the page's assessment. The `(root)` row
covers `hadron --help` and the global flags (`--app`, `--json`, `--server`,
`-v`) once; they are not repeated on every row.

## Results

The result column of the inventory is a closed vocabulary:

- `unread`: nobody independent has assessed the page. The initial value.
  **`unread` is never counted as passing.**
- `pass`: an independent evaluator, named in the evaluator column, read the page
  and found no P1 or P2.
- `fail`: at least one P1 or P2; the finding column links the assessment.

## The evaluator packet

The evaluator is not the author of the text under review, and is named by the
coordinator at dispatch. The evaluator is handed only:

1. the page's live output, `hadron <path> --help`, at the commit pinned in the
   inventory;
2. the group's **goals list**: 5 to 10 lines of the form "a user who wants to X
   should find command Y and succeed", written by the coordinator from the
   group's documentation and not by the author of the help text;
3. this rubric.

No source, no pull request, no author notes. If the page cannot be understood
from those, that is the finding.

The author then revises the help and runs each example against the real CLI;
the same evaluator reassesses the revised pages and the result is recorded.
Remaining problems stay recorded.
