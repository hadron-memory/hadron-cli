# Spec lint: exempt untouched placeholder contracts (#99 items 1 & 2)

Two ergonomics papercuts from authoring `cor:acl:070` in
`hadronmemory.com::specs`, both in the lint engine. Shipped as one PR since
both touch `lintNode`.

## Item 1 — the auto-created `:00` contract is unrequested, mandatory work

**Problem.** `spec new --new-feature` co-scaffolds the feature's `:00`
general-provisions contract (#69) so the feature's rules have an inheritance
target. But contracts are rare by convention (7 across 34 features in this
corpus), and a feature `:00` is a level-3 (rule-tier) node, so strict lint then
demands it be fully authored — a missing/placeholder abstract is an **error**.
Creating one feature forced authoring a whole contract node the author didn't
need.

**Design.** Keep #69's auto-scaffold (so children always have an inheritance
target — no orphan gap), but treat an *untouched* contract as exempt from the
rubric until the author engages it. The signal that it's untouched is the
scaffold's `TODO(abstract):` marker still sitting in the abstract. In
`lintNode`, once past the level-3 gate:

```go
if c.IsContract() && isPlaceholderAbstract(n.Abstract) {
    add("placeholder-contract", sevInfo, "untouched placeholder contract — …")
    return fs
}
```

`isPlaceholderAbstract` is distinct from `!abstractPresent`: an empty/absent
abstract is one the author *removed* (still worth nagging), whereas the marker
means the scaffold is pristine. The exemption emits a single `info` finding so
the placeholder stays visible without blocking — and because it's `info`, not
`warning`, `--strict` never promotes it to an error. Replacing the placeholder
abstract re-engages the full rubric.

Scope note: only the feature `:00` contract reaches this code — module `:000`
and product `:gen` contracts are level < 3 and already skip the rubric.

## Item 2 — lint already reports rubric gaps in one pass

The issue reported abstract and "what invalidates" surfacing one-at-a-time
across reruns. `lintNode` already appends *all* rubric findings in a single
pass (no short-circuit between checks), so this is already satisfied. Added
`TestLintNodeReportsAllRubricGapsAtOnce` to lock the behavior in; no code
change.

## Tests

`lint_test.go`: a placeholder contract yields only the `placeholder-contract`
info finding (no errors); an engaged contract (real abstract, body missing the
invalidates statement) gets the full rubric again; and a rule missing both
abstract and invalidates reports both at once.
