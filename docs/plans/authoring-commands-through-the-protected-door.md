# Design as built: the authoring commands write through the door (#606, create half)

`Memory.protectedLocs` (hadron-server #1180, PR #1190) made the protected-loc
write gate take a second predicate source, and gave memory-specific authoring
tools a door to write through: **`authorProtectedNode`**. It ships **empty by
default**, so nothing is refused today.

The next step is @Vera declaring `protectedLocs` — `['*']` on the specs corpus,
`['review','tasks']` on a repo memory. **Declaring today would refuse this CLI's
own authoring commands**, because every one of them writes through the generic
`createNode`. This moves them, so the declaration can land.

This is the **create half**. The four update sites wait on hadron-server#1192;
the create half is independently correct and changes nothing observable while
`protectedLocs` is empty.

## What the door is, and what it is not

**Pure routing.** The server validates nothing here that `createNode` does not,
and knows nothing about specs, reviews or tasks. Numbering, rubric and templates
stay in this repo, which `cor:api:240:02` permits: both command groups target an
audience that can be required to install a CLI.

**A guardrail, not a security boundary.** What it prevents is the *accidental*
generic write, refused and told which tool to use. A determined caller can still
author a malformed node **through** it — `hadron spec` and a hand-rolled
`hadron_create_node` authenticate identically, which is precisely why the
exemption had to be a distinct server entry point and could not be a flag this
CLI sets. The Channel source of the same gate **is** a boundary (#1047, where
authorship was forgeable and not tamper-evident). Same mechanism, same error
code, materially different strengths, and nothing built here should imply the
specs corpus is as tamper-evident as a chat message.

## The one real decision: `upsert` is FALSE

#606 suggested `upsert: true` for "the re-run case". **Every call site passes
false**, and that is a decision rather than an oversight.

No authoring command upserts. **Before** this move all seven creates went
through `createNode`, which refuses a live `(memoryId, loc)`; `upsert: true`
would have quietly changed that while moving them, which is not what a routing
change is for. Two of them **promise** the refusal in user-visible text:

- `spec new` exits Conflict with `"<citation> already exists"` (`new.go:635`)
- `coding review create`'s help: *"Creating a check that already exists fails
  rather than overwriting it — edit an existing one with `hadron node update`"*

The door preserves the refusal: `authorProtectedNode` with `upsert` false
rejects a live loc with the same `NodeLocConflictError`. Passing `true` would
silently convert those documented refusals into overwrites, and would undo work this team had just finished: hadron-server #1182 and #1184 closed
exactly this hole on the generic surfaces, after a racing `createNode` was
measured silently replacing a live node **40 times out of 40**. A permanent
citation whose node a re-run can overwrite is the hazard
`findings:persona-name-allocation-two-uniques` names — *a reclaim convenience
that silently frees a permanent name*.

So the wrapper **carries** the argument (mirroring the mutation rather than
hard-coding half its contract) and every caller says false. Changing that for a
command is a behaviour change to that command, and wants its own issue and
ruling, not a default.

Verified live: re-running `coding review create` against a loc it had just
minted **refuses**, and the original node is unchanged.

## Why the command tests could not pin this on their own

The fake GraphQL server is keyed by **operation name**. Moving the call sites
made nine tests fail with *"unexpected operation AuthorProtectedNode"*, which is
a good signal — it proves the switch is observable. But the fix is to rename the
fixture keys, and **a revert plus a rename back leaves the suite green**.
Nothing in it asserts which door the authoring commands use.

So the contract is pinned directly, three ways:

1. **A structural guard** over both packages, asserting BOTH halves at every
   site: no generic `createNode` call, and every door call passing the literal
   `false`. It covers all seven uniformly *and any site added later*, which is
   the case a per-command test structurally cannot — nobody writes the test for
   the call site they forgot. That gap was real, and @copilot named it on
   review: the wire test below exercises `coding review create` alone, so
   flipping any `spec` site to `upsert: true` left it green.
   Packages are resolved by **import path**, not identifier spelling — also
   @copilot's: matching the literal `gen` would let a file importing the same
   package as `g` call `g.CreateNode` while the guard stayed green, protecting
   today's spelling rather than the rule. And it parses rather than greps, so a
   comment naming `createNode` to explain why a file avoids it does not trip it
   — the #564 lesson from the exit-code guard next door.
2. **A two-directional check on the guard itself** — it must actually find a
   `gen.CreateNode` in `internal/cmd/node`, which legitimately still uses the
   generic create. Without that, the guard's silence could mean "matcher broken"
   and read as "clean".
3. **A wire assertion** that `upsert` is sent **and** is false. Present, not
   merely absent: the mutation's default is false today, so an omitted variable
   would behave correctly while leaving the CLI's intent unstated and silently
   inheriting whatever the server later makes the default.

## Verification

Mutation-tested, each mutant confirmed to build first: reverting a call site to
the generic create, flipping one site to `upsert: true`, and dropping the
variable entirely — all three caught, each by the test that claims to cover it.

**Run live**, against the real server, in the scratch `experiments` memory
rather than any real corpus:

- `spec new --new-path zzz:010:01` exercised **all three of its create sites in
  one call** — the target, the contract mint and the ancestor scaffold — landing
  five nodes with their edges.
- `coding review create` created a check and wired its parent edge.
- Re-running it **refused** and left the original untouched.
- Every scratch node deleted afterwards; the memory is back to its 50 nodes.

## One thing found and deliberately not fixed

A duplicate-loc refusal exits **1**, not **5** (`exitcode.Conflict`).
`NodeLocConflictError` matches none of `MapError`'s conflict patterns — not
`CONFLICT`, not `DUPLICATE_*`, not `*_ALREADY_EXISTS`, not `*_TAKEN`.

Measured rather than assumed to be unrelated: `createNode` and
`authorProtectedNode` return the **identical** code for the same collision, so
this is pre-existing and unchanged by this PR. Exit codes are a documented
contract agents branch on, so moving one belongs in its own change. Filed
separately.
