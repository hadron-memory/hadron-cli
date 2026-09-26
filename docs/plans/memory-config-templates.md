# Design as built: `memory config template` — owned, reusable rulebooks (#716 slice 2)

> **Status: built in a draft PR (Jane), stacked on cli#733 (slice 1), against
> hadron-server#1369 (#1325 part c) at `15914879`**, itself stacked on #1360
> `9241c55`, the head cli#733 is pinned to. Neither server PR is merged; the
> snapshot is a candidate and is re-exported from the actual merge before this
> lands. Slice 1's design: [`memory-config.md`](memory-config.md).

## 1. Scope

In: `memory config template list | get | create | update | rm`.

Out, and not mocked: **applying** a template and the locked-rule refusal (#1334),
and the visibility warnings themselves (#1327; the `warnings[]` envelope is
rendered, and is empty today).

**A deviation from the design handoff.** The handoff on #716 proposed a
top-level `hadron config-template` group. It is built under `memory config
template` instead:
- every MemoryConfig verb then sits under one group;
- the template commands share slice 1's rule rendering, reference validation
  and enum parsing without an exported cross-package seam;
- #1334's apply has an obvious home beside it (`memory config apply`).

## 2. What the server provides (part c)

- A template has **exactly one owner**: `HADRON_SERVER` (platform admins),
  `ORGANIZATION` (org ADMIN/OWNER), `USER` (yourself only), or `APP` (the App's
  owner or an org ADMIN/OWNER).
  - `ownerRef` is omitted for the server and for yourself.
  - For anyone who doesn't manage the owner, the template does not exist:
    `memoryConfigTemplate` is null, and a write is `MEMORY_CONFIG_TEMPLATE_NOT_FOUND`.
- **Rules reuse `CreateNodeRoleRuleInput`**, the same input `rule add` sends, and
  are validated as a whole. `updateMemoryConfigTemplate` replaces them wholesale
  under the template's `revision` (`CONFLICT {currentRevision}`).
- **Update semantics:** omitted means unchanged, and `description: null` clears.
  `rules: null` is read like omitted (`input.rules != null`), and `[]` replaces
  the rules with none.
- **Errors:** `MEMORY_CONFIG_TEMPLATE_EXISTS` (the name is taken under the
  owner), `MEMORY_CONFIG_TEMPLATE_NOT_FOUND`, `HADRON_SERVER_NOT_CONFIGURED`,
  `NODE_ROLE_RULE_EXISTS` (a role listed twice), `BAD_USER_INPUT` (name
  grammar, owner arity), plus part (b)'s rule codes.

## 3. What the CLI does

### 3.1 The file is `get --json`

`create` and `update` read `--file` (JSON; `-` reads stdin through
`cmdutil.ReadDocumentStdin`, which refuses a terminal). Its shape is **`template
get --json`'s own**, so a template round-trips: read it, edit it, send it back,
or feed it to `create` to copy it.

- The server-owned keys it carries (`id`, `ownerType`, `ownerId`, `created*`,
  `updated*`, and `revision` on create) are accepted and **never sent**.
- Every other unknown key is **refused** (exit 2). A typo (`"requird"`) would
  otherwise do nothing, silently.
- A rule reference is taken from its URN, else from its id (an OK reference
  whose legacy memory URN cannot address it). It is validated with slice 1's
  `canonicalRuleRef`, one validator for flags and files.
- **A reference the file only knows as `BROKEN` or `UNREADABLE` is refused.** The
  server withheld which node it is, so sending nothing would **drop** it from
  the template: the stale-snapshot overwrite #716 forbids. The message names
  both remedies: set the reference, or remove its state key to drop it on
  purpose.

**Review round 1 (#735) tightened three things, each from Copilot and Codex:**
- **Reference states are exact:** `NONE | OK | BROKEN | UNREADABLE` or absent.
  An `OK` with neither URN nor id is refused, and so is any other spelling
  (`ok`, `UNREADBLE`). The state is never sent, so a state with nothing behind
  it would drop the reference, and `update` replaces every rule. A NEW
  reference beside a stale state is still accepted: that is the documented
  remedy.
- **Exactly one JSON object:** `{"name":"a"}{"requird":true}` used to apply the
  first object. `cmdutil.HasTrailingJSON` (exported from the `--where` parser,
  so there's one copy) now refuses it.
- **An empty `--owner-app`** is refused like `--owner-org`. `CanonicalAppRef`
  reads `""` as "no App", which here would have sent `APP` with an empty ref.

### 3.2 `update` is guarded by the FILE's revision

The guard is the revision the **file** was derived from: its `"revision"` key,
or `--expected-revision`. It is **never a fresh read**. A fresh read would
certify a stale file as current, which is the one overwrite the guard exists to
refuse.

Refused before any request (exit 2):
- no revision at all;
- a flag that disagrees with the file;
- a file whose `id` names another template (applying template A's file to B);
- a file that changes nothing.

A key's presence is what the file says: an omitted key is unchanged,
`"description": null` clears (via a literal-null operation, one update under one
revision, as in slice 1 §3.3), and `"rules"` replaces all rules.

### 3.3 `"rules": []` must reach the wire

`omitempty` drops an **empty** slice as well as a nil one, so with it,
`"rules": []` ("remove every rule") was silently dropped and the rules kept.
The server reads null like omitted, and this input exists on no server lacking
the field. So `UpdateMemoryConfigTemplateInput.rules`, and the clearing
operation's `$rules`, carry **no** `omitempty`: nil is sent as `null`
(unchanged), and `[]` is sent as `[]`.

That took an **explicit** `omitempty: false`. `use_struct_references`
(genqlient.yaml) makes genqlient add `omitempty` to a struct-typed list by
default, so a directive simply left out is not enough.
`TestMemoryConfigInputsOmitEveryUnsetField` carries this field in an
**enumerated** exemption (`sendsNullOnPurpose`) with its reason, and asserts it
has *no* `omitempty`.

### 3.4 A shared input must carry its directives everywhere

`CreateNodeRoleRuleInput` is now sent by four operations. With its seven
`omitempty` directives on `CreateNodeRoleRule` only, genqlient **flipped the
tags between runs** (measured: red in 4 of 10 regenerations). In a flipped
run, `rule add` would have sent `null` for every unset reference. Every
operation sending the input now repeats the seven directives verbatim, and the
tags are stable over 8 regenerations.

The reflection guard now covers the five memory-config inputs. Note what it
does and doesn't do: it checks the **committed** generated code, which is what
ships, so it is not a determinism test. A partial directive set passes on the
runs that happen to keep the tag.

### 3.5 Owners, listing, deletion

- **Owner flags:** `--owner-server | --owner-org <ref> | --owner-me | --owner-app
  <ref>`. `create` needs exactly one, `list` at most one; both are refused
  before any request. An **empty** `--owner-org` (an unset shell variable) is
  refused, not read as "no owner". `--owner-app` goes through
  `cmdutil.CanonicalAppRef`; an org ref is resolved by the server, as `scope`
  does.
- **`list` pages to exhaustion** (cap 200) and stops at `total` **or** on an
  empty page, so a shrinking set ends the loop instead of spinning it.
- **`rm`** reads the template (revision, name), confirms, and deletes under that
  revision. It uses `cmdutil.Confirm`, not `ConfirmDeletion`: the delete is soft
  (configs keep their copies), so "cannot be undone" would overstate it, but
  no command restores a template either. The prompt says both
  (review:confirm-prompt-tells-the-truth).

### 3.6 Exit codes

- **One new row:** `MEMORY_CONFIG_TEMPLATE_EXISTS` → 5.
- `MEMORY_CONFIG_TEMPLATE_NOT_FOUND` → 4 via the `_NOT_FOUND` suffix, pinned in
  the table.
- `HADRON_SERVER_NOT_CONFIGURED` is deliberately **left on 1**, pinned in the
  table. It means the deployment has no server row, which is an operator's fix,
  not the caller's.
- Naming an owner you don't manage is refused by the server with the untyped
  `Forbidden` (exit 1, the server-wide pattern noted in slice 1 §5).

## 4. Tests

`internal/cmd/memory_config_template_cmd_test.go`:

- **`list`:** 201 templates over two pages, with the second page's offset
  asserted; an empty page ends the loop; `items: []` on the raw output; the owner
  filter is sent as given and omitted when absent; two owners or an empty
  `--owner-org` are refused with no request.
- **`get`:** a template you don't manage is exit 4.
- **`create`:**
  - owners: each of the four types, with no `ownerRef` for server or self and
    the App ref canonicalized. No owner or two owners are refused with no request.
  - round trip: `get --json` output fed back sends the name and description
    verbatim and no server-owned key; a URN, or else an id, for references; a
    NONE reference omitted.
  - nine malformed files are refused before any request, including a typo'd
    key, a withheld reference and a bare loc.
  - a taken name is exit 5.
- **`update`:**
  - the file's revision is sent, and the template is **not** re-read.
  - five local refusals; a stale file is exit 5.
  - `"rules": []` reaches the wire as `[]`, and an absent `rules` as `null`.
  - the description clear is one update carrying the other keys.
- **`rm`:** without `--yes`, no delete is sent; with it, it deletes under the
  revision read.

**Mutation-checked:** 22 compiling mutants, all red (5 added in round 1: an OK reference dropped, any state accepted, trailing content accepted, an empty `--owner-app` sent, and the remedy refused). They cover:
- listing: only the first page read, no empty-page stop;
- the revision guard: a fresh read of the revision, the flag winning over the
  file, a file of another template accepted;
- file handling: a withheld reference silently dropped, `rules: []` becoming
  nil, unknown keys allowed, the name altered, the id fallback ignored;
- the description clear sent as an ordinary update;
- `--owner-me` sending a ref;
- no confirmation;
- a dropped exit row.

Two first-round survivors were informative. The empty-rules fallback existed
twice, each copy covering the other, so the redundant one was deleted and the
remaining one is killed. The copied name was not asserted; it now is.

## 5. Before merge

1. #1369 merged (after #1360 and slice 1, cli#733).
2. `make schema` from the merge commit, `make generate`, a diff check, and 6+
   regenerations to confirm the tags are stable.
3. The full suite.
4. Read-only against a server running the merge: `template list`, and `get` on
   a template you manage and on one id you don't (exit 4).
5. Mark ready and re-request reviews on the final head.
