# Design as built: node revisions and `rev=N` skill provenance (#715)

> **Status: built in cli#724 (Jane), against hadron-server#1339 (#1323).** The
> schema snapshot must be re-exported from #1339's merge before cli#724 lands.
> Part of hadron-concept#74 (CLI item 2 + L0.1 exposure).

## 1. What the server provides (#1339)

- `Node.revision: Int!` is the node's live revision. Creation is 1, and each
  committed authoring change advances it. MCP's node read prints it as
  `Revision: N`.
- The rendered skill header gains a key:
  `<!-- hadron-skill id=… rev=N source=… hash=… -->`. `rev=` is **not** a hash
  input.
- `SkillFileFactsInput.revision: Int` carries a file's `rev=`. The server
  classifies a file `stale` when it differs from the node's live revision, even
  when the content hash agrees.
- One rule governs a `rev=` value, shared by render and parse:
  `^[1-9]\d*$` and at most 2147483647 (`Node.revSeq`'s signed 32-bit column).
  **A malformed `rev=` voids the whole header.**

## 2. What the CLI does

### 2.1 `node get` shows `revision`

- `--json`: the key is **always present**. `null` means *the server predates
  revisions* and nothing else. Human output: `revision: N`, or
  `revision: unknown (the server predates node revisions)`.
- **A separate query, `NodeLiveRevisions`.** It is not added to
  `GetNode`/`NodeBatch`. Those back about 30 commands (`spec`, `skill`,
  `coding`, `memory export` …), and a server without the field rejects the
  **whole** query that names it. A second query costs `node get` extra round
  trips. It costs every other command nothing.
- **Pairing guarantee: the revision printed is the revision of the content
  printed.** The content read is bracketed by two revision reads:
  1. the revisions of the nodes the content read will name, using the **same
     selector** (the resolved id; the canonical refs, via the shared
     `batchRefs`; or `memory` + `locPrefix`);
  2. the content read;
  3. the revisions of the ids the content read returned, in
     `api.NodeBatchCap` calls.

  The content is kept only when every node has the same revision in (1) and
  (3). A revision advances on every authoring change, so equal brackets mean
  the content **is** that revision. An `updatedAt` comparison was the first
  design and was rejected (@codex on #724): two writes can share a timestamp.
- **What counts as a change** (the whole read repeats, up to 3 attempts, then
  exit 5, "try again"):
  - a revision that differs between the brackets;
  - a node the content read returned that is absent from either bracket
    (made unavailable, or deleted and recreated, in between). Two absences
    never pair as `0 == 0`;
  - revision support appearing or vanishing between (1) and (3), in either
    direction: a server changing under the read in a rolling or mixed
    deployment. That is not "predates revisions".
- **The guarantee is about the nodes printed.** In `--prefix` mode, a node (1)
  saw that the content read no longer returns (deleted or made unreadable in
  between) is correctly absent from a read of that moment. It is not a retry
  (declined on #724).
- **Only one thing downgrades to `null`:** both (1) and (3) refused as an
  unknown field (`Cannot query field "revision"` /
  `GRAPHQL_VALIDATION_FAILED`). One probe alone cannot say it, because in a
  mixed deployment either can reach an older instance. Any other failure,
  including a `null` `nodeBatch` envelope, is the command's error.

### 2.2 `rev=` in skill files

- The Go header parser reads `rev=` with the server's exact rule. A malformed
  value voids the header in `ParseFile` and `ParseProvenance`, and the line
  stays body, as on the server. It is never downgraded to a revision-less
  header. Otherwise the CLI would submit as ours a file the server reads as
  foreign.
- `skill status`/`export` send the value as `SkillFileFactsInput.revision`,
  with **`omitempty`**. A file without `rev=` sends no key, so an older server
  (which rejects unknown input fields) still accepts the request.
- **A `rev=` file against an older server** (rollback, mixed deployment, a
  directory reused against another endpoint): the server refuses the whole plan.
  Measured on production 0.19.0: `BAD_USER_INPUT`,
  `Field "revision" is not defined by type "SkillFileFactsInput"`. On exactly
  that refusal the plan is asked once more with every revision dropped. Any
  other error is still the run's.
- **A parse-failed file sends no revision, deliberately.** The server returns
  `{parseFailure: true}` before it reads `headerRevision` (#1339 `classify.ts`,
  `classifySkill`), so a revision there changes nothing.
- **No client rendering.** `skill export` and `skill plugin` write the server's
  rendered body verbatim, so Claude, Codex and plugin files all carry `rev=`
  with no client change (#1177: rendering is server-side).

## 3. What a revision is not

A revision versions the **node**. It does not version partials or template
data the node includes, and it is not proof that the content complies with any
task (#715's acceptance). `agentic-usage.md` says so.

## 4. Test doubles

`fakeGraphQL`/`captureGraphQL`/`captureGraphQLFunc` answer an **unstubbed**
`NodeLiveRevisions` with an older server's exact refusal (`unstubbedDefault`).
So every existing `node get` test also exercises the compatibility path, and a
test that cares stubs the modern answer. No other operation becomes
stub-optional.

## 5. Before merge

1. hadron-server#1339 merged.
2. `make schema` from the merge, then `make generate`, plus a diff check.
3. Full suite.
4. A read-only `node get` and `skill status` against a server running the merge.
