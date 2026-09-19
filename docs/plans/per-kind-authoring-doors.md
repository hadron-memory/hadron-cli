# Design as built: the per-kind authoring doors (#606, completed)

> **Status: implemented and verified.** Closes the whole of
> [#606](https://github.com/hadron-memory/hadron-cli/issues/606) — the create
> half shipped in [#607](https://github.com/hadron-memory/hadron-cli/pull/607)
> against the old door and is re-routed here; the update half was blocked on
> hadron-server#1192 and is unblocked by the same change that broke the creates.
>
> Supersedes
> [`authoring-commands-through-the-protected-door.md`](authoring-commands-through-the-protected-door.md).

## Why this was urgent rather than scheduled

hadron-server [#1203](https://github.com/hadron-memory/hadron-server/pull/1203)
shipped #1201: it replaced the single `authorProtectedNode` with one door per
kind **and deleted the generic one**. Five CLI commands called the deleted
field, so `main` broke against the live server the moment it merged:

```
$ hadron api 'mutation { authorProtectedNode(...) { id } }'
HTTP 400: Cannot query field "authorProtectedNode" on type "Mutation"
```

`spec new`, `spec supersede`, `spec extract`, `coding preflight add` and
`coding review add` were all dead. `make schema-check` — the drift detector
built for exactly this class after #168 — flagged it precisely.

**No installed CLI was affected.** `766e0c5` (the door, 2026-09-17) is not an
ancestor of `v0.13.0` (2026-09-11), checked with `git merge-base --is-ancestor`
rather than recalled. It hit anyone running `./bin/hadron` from a checkout,
which is most of this team. It is also the concrete argument against the release
that was nearly cut on the 17th: v0.14 would have broken `hadron spec new` on
every upgraded machine.

## What actually changed, and why the new shape is better

**Protection moved from the node's ADDRESS to its KIND.**

`Memory.protectedLocs` was a per-memory list of loc patterns, and one generic
door was exempt from **every** entry in **every** memory — so holding it was a
skeleton key. A kind-specific door is exempt from its own kind only, and remains
fully subject to the others and to Channel address protection, so it cannot
forge a chat message. That containment is the whole gain.

The gate reads the **resulting state** of the write, not the request:

| the node carries | door |
| --- | --- |
| `role: "spec"` | `createSpecNode` / `updateSpecNode` |
| `role: "review"` | `createReviewNode` / `updateReviewNode` |
| `isRunnable: true` | `createTaskNode` / `updateTaskNode` |

### The three are not equally strong, and the code says so

Stated at the wrapper rather than left for a reader to assume, because the
entire #1201 thread was people — on both repos — assuming a mechanism was
stronger than it is:

- **Spec — DILIGENCE.** Attests the spec tool authored the node, not that nobody
  else could. The label is free to omit and a citation resolves without it. The
  old "guardrail, not a security boundary" note was about this and still holds.
- **Review — SECURITY.** Any work can be reviewed and a review may be a *safety
  check*, so an attacker able to modify an assignment must not also be able to
  remove the check meant to catch it.
- **Task — CAPABILITY.** The only gate that cannot be dodged by omission: it
  keys on `isRunnable` rather than on a label, and dropping the capability means
  the node does not run.

## The routing decision, per call site

Eleven sites. Two of them are not a rename.

| site | kind | door |
| --- | --- | --- |
| `spec new` ×3 (spec, co-scaffolded contract, ancestor scaffold) | spec | `createSpecNode` |
| `spec supersede` (mint) | spec | `createSpecNode` |
| `spec extract` (mint) | spec | `createSpecNode` |
| `coding review create` | review | `createReviewNode` |
| **`coding preflight create`** | **none** | **generic `createNode`** |
| `spec edit` | spec | `updateSpecNode` |
| `spec supersede` (retire) | spec | `updateSpecNode` |
| `spec extract` (source rewrite) | spec | `updateSpecNode` |
| **`coding preflight create`** (router body) | **none** | **generic `updateNode`** |

### Why `coding preflight create` goes BACK to the generic surface

The dispatch said *"`coding preflight add` → the task door"*. That was written
while the design was kind-by-name; by the time it settled, the task gate keys on
`isRunnable`.

**A route target is not runnable.** The command cannot make one — there is no
flag — and the file's own constant says why: *"Route targets are orientation,
not gates — a runnable node is a task, which the router links to rather than
owns."* Measured on the live corpus before deciding: every node under
`preflight` is `isRunnable: false`, `role: null`.

It used the old door because `preflight` was a protected **address**. That
column is gone, so it needs no door. Routing it through `createTaskNode` would
claim a kind it does not have and would start failing the day a door asserts
one.

### The role is not set by the wrapper

`CreateSpecNode` does **not** stamp `role` for the caller. The gate reads the
resulting state, so a node's kind is a property of the node being written, not
of the function called to write it. Stamping it in the wrapper would let a call
site that never thought about the kind still mint a governed node — the wrong
direction for a signal whose whole job is to be explicit.

Both halves are needed and neither implies the other: **the door is how the
write gets through; the role is what the gate reads.** A check written through
the review door without `role: "review"` is ungoverned, and the generic
`updateNode` will rewrite it afterwards.

## Verified against the live server, not the resolver

A scratch node in `hadron-cli`'s own memory, created and deleted:

| step | result |
| --- | --- |
| `createNode` with `role: "spec"` | **refused** — *"only createSpecNode may create. The generic node surface does not reach it."* |
| `createSpecNode` with `role: "spec"` | created, `role: "spec"` on the response |
| `updateNode` on that node (content only) | **refused** — *"This write edits a node with role \"spec\", which only updateSpecNode may update."* |
| `updateSpecNode` (content only) | succeeded, `name` preserved |

The third row is the one worth keeping: it is the proof that the update doors
are load-bearing rather than symmetrical tidiness. An edit touching neither
signal is still refused, because the gate reads what the node **will be**. It is
also why `upsert: true` was never a workaround for the missing update door —
`CreateNodeInput` has no omit-to-preserve, and the fourth row shows
`updateSpecNode` keeping it.

## What the guard now asserts

`internal/cmd/authoring_door_test.go` is rewritten, and the change is in its
shape, not its strictness.

The old guard asked *"is this package privileged?"* — right when the rule was
about addresses. The new one asks *"what will this node be?"*, which means the
`coding` package is **mixed**: `review_add.go` is governed, `preflight_add.go`
is not. So it keys on the FILE, and the classification is an **allow-list of the
ungoverned** rather than a list of the governed — a new file is governed by
default, so forgetting to classify it fails the guard instead of silently
exempting it (`review:an-exemption-you-cannot-enumerate-is-a-hiding-place`).

It also now:

- matches `UpdateNode`, which it deliberately did not while the door was
  create-only;
- **names the deleted doors explicitly**, so calling `authorProtectedNode` fails
  a test rather than a live request. That is the #1203 lesson turned into a
  check: the field's removal was invisible to every test in this repo, because
  the fake GraphQL server is keyed by operation name and answers whatever it is
  asked.

## One defect found by an existing guard

`TestUpdateNodeInputFromMapsAllFields` failed the moment `Role` appeared on the
inputs: `node import`'s `updateNodeInputFrom` dropped it. Left unmapped,
**re-importing a spec or review node would silently strip the role that makes it
governed**, leaving it rewritable through the generic surface. Mapping it does
not let `node import` mint a governed node — the server refuses that on the
generic surface — it keeps the import honest about what the file says, and makes
the refusal visible instead of silently obeyed.

## Deliberately not done

- **No backfill.** Existing spec and review nodes carry `role: null`, so they
  stay ungoverned until something rewrites them. Backfilling is a write over
  nodes in memories this repo does not own; it is the coordinator's to scope.
- **No `createTaskNode` / `updateReviewNode` / `updateTaskNode` operations.** No
  call site needs them, and an unused operation is generated code that has to be
  maintained. They exist server-side when a caller appears.
- **`--upsert` is not reintroduced.** The doors do not offer it, so "every call
  site passes false" stopped being a contract this repo has to keep. The refusal
  it protected survives as `NodeLocConflictError` → exit 5 (#610).
