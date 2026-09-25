# How to maintain product specs

`hadron spec` runs a Hadron memory like a legal code: a spec's `loc` **is** its
citation, numbers are never renumbered (to replace a spec you `supersede` it),
and `lint` checks a spec's structure (not its content sections, which depend on
its type). A loc implies no parent: a spec's edges are the ones it was given.

Every subcommand takes `-m/--memory hrn:mem:<root>:<slug>`.

> **The fixed hierarchy is removed (#708/#709).** A spec is any node
> tagged `spec` (or carrying the governed spec role), and **any valid node loc
> is a spec address**, at any depth and in any shape. `get`, `list`, `edit`,
> `link`, `find`, `grep`, `replace` and `check-tools` no longer check the
> numbering below, and no longer drop a spec from a listing for its shape. The
> schemes below describe the **legacy numbering**, which `spec new`'s
> allocation and contract flags still produce. To create a spec anywhere
> else, use `spec new <loc> --title <title>`. To replace one, use
> `spec supersede <old> --to <loc>`. The flat/product scheme is retired
> (#709), and `lint` no longer checks the tiers (parent-exists, toc-edge,
> inheritance-edge, index-incomplete, mixed-arity). The old content rubric is
> removed too (#708): no missing abstract, "what invalidates", `data.version`,
> scaffold-body or placeholder-contract finding at any loc.
> See [docs/plans/spec-hierarchy-removal.md](../plans/spec-hierarchy-removal.md).

## The legacy numbering

Any valid loc is a spec (#708). Some corpora number their specs in a legacy
scheme, and `spec new`'s allocation and contract flags still produce it:

```
flat:      <module>:<feature>:<rule>[:<flow>]            msg:010:02:03
product:  <product>:<module>:<feature>:<rule>[:<flow>]   cli:cha:010:01:02
```

- **product**: a shippable artifact (`cli`, `srv`, `por`), 3 lowercase letters.
- **module**: its top-level internal division, 3 lowercase letters.
- **feature**: 3 digits, numbered in tens (`010`, `020`, …).
- **rule**: 2 digits, `+1`.
- **flow**: 2 digits, `+1`.

This is a convention, not a rule: nothing refuses or flags a spec at any other
loc, and a memory no longer has a "scheme". It may mix both forms, or use
neither (#709). Codes and numbers are still never renumbered; to replace a spec
you `supersede` it.

### See what a memory holds

```sh
hadron spec describe -m hrn:mem:hadronmemory.com:specs
```

`describe` is a neutral inventory. It reports:
- how many specs the memory holds;
- their root segments;
- the deepest loc;
- how many specs are in the legacy numbering, and how many are outside it.

It classifies nothing. `--declare` is retired: it is refused and writes
nothing. A scheme a memory's data still carries from it is shown as retired
and ignored.

## General-provisions contracts

Provisions shared across siblings live in a reserved **contract** node that the
siblings inherit from — one per tier:

| shared across… | contract loc | created with the root… | …or retrofitted with |
|---|---|---|---|
| all rules of a feature | `<m>:<f>:00` | `spec new --module msg --new-feature` | `spec new --module msg --feature 010 --contract` |
| all features of a module | `<m>:000` | `spec new --module msg --new-module` | `spec new --module msg --contract` |
| all modules of a product | `<p>:gen` | `spec new --product cli --new-product` | `spec new --product cli --contract` |

(Abbreviated: every one of those also needs `--title`, plus `-m` unless a default
memory is set. `--new-feature` allocates the *next free* feature, so its contract
lands under whatever number that turns out to be — not necessarily `010`.)

The contract spelling follows each tier's alphabet — the numeric tiers use their
"zero" (`00`, `000`), and the alpha module tier uses the reserved code `gen`.
A new sibling automatically gets an inheritance edge to the contract when it
exists.

**You rarely create one by hand.** Creating a root — `--new-product`,
`--new-module`, `--new-feature`, or any root minted by `--new-path` — *also*
scaffolds that tier's contract, titled `<title> general provisions` and wired
back to the root, so the root's children have an inheritance target from the
start. Pass `--no-contract` to suppress it; the root is still created, and still
inherits whatever contract already exists above it.

`--contract` is therefore for retrofitting a tier that predates that behaviour
or was created with `--no-contract`. It always scaffolds the contract at the
**deepest tier you name**, so you don't have to remember which spelling applies.

## Scaffolding a product corpus

`spec new <citation> --new-path` creates the citation you name **plus every
missing ancestor** in one call — each with its tier template and, for the roots,
its general-provisions contract. Use `--dry-run` to preview any step without
writing.

```sh
M=hrn:mem:hadronmemory.com:platform-specs

hadron spec new -m $M cli:cha:010:01 --new-path --title "backpressure"
```

From an empty corpus that one call mints `cli`, `cli:gen`, `cli:cha`,
`cli:cha:000`, `cli:cha:010`, `cli:cha:010:00` and the rule itself, wired
top-down; ancestors that already exist are left alone. `--new-path` takes the
citation *positionally* and refuses to be combined with the tier-selecting flags
(`--product` / `--module` / `--feature` / `--rule` / `--rule-after` / `--flow` /
`--inherit` / `--new-*` / `--contract`).

**Rename the ancestors afterward.** `--title` lands on the citation you named;
each ancestor is titled from its own citation segment — `cli:cha` is titled
`cha`, `cli:cha:010` is titled `010`. That lints clean (the name leads with the
citation), so nothing will remind you:

```sh
hadron node update cli:cha -m $M --name "cli:cha — chat command group"
```

Building tier by tier is still there for when you want to title and populate
each level as you go. Each level must exist before its children:

```sh
hadron spec new -m $M --new-product --product cli --title "Hadron CLI"
hadron spec new -m $M --product cli --new-module --module cha --title "chat command group"
hadron spec new -m $M --product cli --module cha --new-feature --title "streaming"
hadron spec new -m $M --product cli --module cha --feature 010 --title "backpressure"
```

Each of the first three calls also scaffolds its tier's contract (`cli:gen`,
`cli:cha:000`, `cli:cha:010:00`) unless you pass `--no-contract`. Product and
module codes are frozen: re-minting one exits 5.

**Each node is written together with its edges.** Unless you pass
`--no-edges` (which deliberately creates the node without them), a spec node
and its table-of-contents / inheritance edges are one server write, so a node
never lands without them. A missing parent tier, or an edge target that doesn't
resolve, is rejected up front (exit 4, nothing written).

**A failed `spec new` is still not always a clean slate**, because a command
that creates *several* nodes creates each in its own write: a root whose
co-created contract then failed, or a `--new-path` chain that stopped part-way.
The error names what was already created, and each of those is complete, edges
included. Don't reflexively re-run: the citation exists now, so `--new-path` and
the `--new-*` roots exit 5 on conflict, while an allocating call
(`--new-feature`, or `--feature` without `--rule`) quietly mints a *second*
number instead of repairing the first. Inspect what actually landed, then create
only what is missing, e.g. a contract the root is still waiting for:

```sh
hadron spec list -m $M --prefix cli:cha            # what actually exists
hadron spec new -m $M --product cli --module cha --contract --title "chat command group"
```

A flat corpus is identical without the `--product` flag (and `--new-module`
creates a top-level module):

```sh
hadron spec new -m hrn:mem:micromentor.org:platform-specs --module msg --feature 010 --title "W4 — 7d check-in"
```

## The legacy rule scaffold

`spec new` scaffolds a legacy-numbered rule with the sections below. **`lint`
enforces none of them** (#708): the sections a spec needs depend on its type,
so no one rubric fits every spec. Use the sections your spec type calls for
(`specs:tasks:validate-spec` checks them); this scaffold is a starting point,
and the two *optional* sections are deleted when they add nothing.

1. **Definition** — one line: what this spec governs.
2. **Scenarios / user stories** *(optional)* — 3–7 short scenarios that explain
   who needs the rule and why, framing intent before the precise contract. Prefer
   `As a <actor>, I want <capability>, so that <outcome>.`; for lower-level,
   multi-actor, or failure/recovery behavior, plain `Scenarios:` bullets read
   better. Add them where they clarify intent (APIs, auth, permissions,
   workflows, multi-actor flows); skip them on a self-evident schema rule. Don't
   pad to fill the template.
3. **Rule & examples** — the rule precisely, with concrete examples and edge cases.
4. **Durable vs tunable** — which parts are load-bearing, which are dials.
5. **What invalidates this spec** — the changes that repeal or supersede it.
6. **Acceptance criteria** *(optional)* — concrete, checkable statements
   engineering or QA can verify, for specs whose behavior must be testable.

Flows (`:NN:NN`) inherit their rule's scenarios and stay terse — their scaffold
is shorter.

## The index convention (legacy module and feature nodes)

In the legacy numbering, a module or feature node is not a rule but an **index
of its children**, and its two fields divide that work between them. This is a
convention, not a lint obligation: `index-incomplete` is removed (#708), so
nothing checks the body index, and a spec at any loc owes none. Only the
abstract's cap still applies, through `abstract-length`.

| field | job |
| --- | --- |
| **abstract** | ROUTE by *describing* subjects — one clause per child, naming what that child is. An index that RESTATES its children instead of routing to them runs into `abstract-length` |
| **body** | INDEX by *citing* children — one entry per child, carrying its loc |

Keep citations out of the abstract. It is the embedded retrieval surface, a loc
string means nothing to an embedding, and the characters it spends count against
the 2000-char cap. Cite in the body.

Three spellings of a body entry are in live use:

```markdown
- [`cor:acl:010`](hrn:node:hadronmemory.com:specs:cor:acl:010) — full citation
- **[010 Memory access](hrn:node:hadronmemory.com:specs:cor:acl:010)** — full citation in the link target
- **`:01` Who may impersonate** — colon-leaf, once the node's own citation sets the prefix
```

Record a superseded child **struck** rather than dropping it, so the withdrawal
stays visible:

```markdown
- ~~[`cor:agt:020:06`](hrn:node:hadronmemory.com:specs:cor:agt:020:06)~~ — **superseded** (rescinded 2026-08-14, no successor)
```

## Navigating and validating

```sh
hadron spec use $M                                # save the default spec memory in your user config
hadron spec list   -m $M --prefix cli            # one product (or cli:cha for one module)
hadron spec list   -m $M --prefix cli:cha:010    # one feature and its rules/flows
hadron spec get  cli:cha:010:01 -m $M          # one spec + lint summary
hadron spec find "backpressure" -m $M          # semantic search, filtered to specs
hadron spec grep h-read-node -m $M             # body+abstract search, citation:line: text (exhaustive)
hadron spec lint --product cli -m $M           # lint one product
hadron spec lint --all -m $M --strict          # lint the whole corpus, warnings = errors
hadron spec register -m $M                      # derived number ledger (next-free at each tier)
```

`find` ranks by relevance over name/loc/description/tags; `grep` is the
exhaustive, line-oriented complement that reads every spec's **body and
abstract** (one bulk fetch, not a per-spec loop) and prints every occurrence as
`citation:line: text` — literal by default, `--regex`/`-i`, `--field
content|abstract`, `--prefix` to scope.

`lint` checks each spec's name, node type and `spec` tag, duplicate locs,
serialization leaks, and each abstract's length and freshness; every finding
names its rule. It is structural: since #708 it checks no content sections at
any loc (no missing abstract, "what invalidates", `data.version`,
scaffold-body or placeholder-contract finding). A spec at any loc owes no parent, contract or index, and a
memory may mix loc shapes: the legacy tier checks (parent-exists, toc-edge,
inheritance-edge, index-incomplete) and the one-arity rule (`mixed-arity`) are
removed.

Use `hadron spec use $M` when you are repeatedly maintaining the same corpus.
It writes `spec_memory` to your user config (for example,
`~/.config/hadron/config.toml`), so it applies across checkouts; pass `-m` when
one repository or one call should target a different corpus.

### Working across several spec corpora

Specs in different memories share citations and names — `msg:010:02` exists in
as many corpora as have a messaging module — so an *unscoped* `list`/`find`
returns rows that look identical. Two things keep that straight:

- **Scope the session.** `export HADRON_SPEC_MEMORY=$M` scopes every `hadron
  spec` call in that shell (and any agent it launches). Prefer it over `hadron
  spec use` when you work on more than one corpus: `use` writes the
  machine-global user config, so two concurrent sessions would fight over it.
  Whenever a default answers, the command notes which one on stderr.
- **Read the MEMORY column.** When results *do* span several memories, `list`
  and `find` add a `MEMORY` column naming each hit's memory; scoped
  output stays narrow. In `--json`, every row carries `memoryId` (the PK) and
  `memoryUrn` (the readable form) regardless of scope.

## Corpus-wide find/replace

To rename a token across the whole corpus (e.g. stale tool shorthand), use
`spec replace` — word-boundary-aware by default, so it rewrites whole tokens only:

```sh
# Preview: which specs, how many matches (nothing written)
hadron spec replace h-read-node hadron_get_node -m $M --dry-run

# Apply across one module (whole-token only), then it re-lints the changed specs
hadron spec replace h-read-node hadron_get_node -m $M --prefix cor:api --yes

# Regex with a backreference; --word-boundary=false for a raw substring replace
hadron spec replace 'h-chat-(\w+)' 'hadron_chatbot_$1' -m $M --regex --yes
```

It rewrites **body + abstract** by default (`--field` narrows), is gated like
other bulk writes (prompt / `--yes`, `--max-specs N` to cap blast radius), saves
every change to version history, and re-lints the rewritten specs so a body edit
that leaves an abstract stale is surfaced immediately.

## Editing and splitting specs

**A body-only edit arms `abstract-stale`.** The abstract was fingerprinted
against the old content, so every later read flags it as a possibly-outdated
preview — and because `edit` preserves an unchanged field by omitting it,
re-running with the same abstract writes nothing and cannot settle the marker.
`--abstract-still-accurate` is the way out: it asserts you re-read the abstract
and it still describes the spec, and re-sends it unchanged so the server
re-fingerprints it against the new body.

```sh
# a body edit where the abstract survives it
hadron spec edit cor:agt:020 -m $M --content-file body.md --abstract-still-accurate

# settle a marker an earlier edit left behind (nothing else changes)
hadron spec edit cor:agt:020 -m $M --abstract-still-accurate
```

It is an assertion, not a formality — the marker is a prompt to check, and the
one use it must not be put to is re-affirming an abstract you have not re-read.
It is refused alongside `--abstract`/`--abstract-file`; on a spec with no
abstract at all (re-sending an empty value would clear the field rather than
re-affirm it); and on a legacy abstract past the 2000-char cap, where
re-affirming turns a preservable value into a replacement the server rejects —
shorten it with `--abstract-file`, which re-fingerprints in the same write.

Use `edit` for ordinary body or abstract changes that do not change the durable
meaning of the citation:

```sh
hadron spec edit cli:cha:010:01 -m $M
hadron spec edit cli:cha:010:01 -m $M --content-file /tmp/rule.md --abstract-file /tmp/abstract.md --dry-run
```

The interactive form opens both abstract and body in `$EDITOR`; non-interactive
flags update only the fields you provide and preserve the rest.

Use `extract` when part of a fat rule deserves its own citation. Pipe or pass
the moved chunk as the new body; `--strip-source` trims it from the old body only
when it matches verbatim.

```sh
hadron spec extract cli:cha:010:01 -m $M \
  --to-feature 010 --title "backpressure timeout" \
  --content-file /tmp/extracted.md --strip-source --dry-run
```

After an extract, refresh both abstracts so semantic search can retrieve the
right node.

## Cross-referencing specs

Use `link` for same-corpus spec-to-spec references. The command validates both
endpoints are spec nodes and creates the edge from the more specific citation to
the more general one; omit `--label` to let the CLI synthesize the conventional
field-to-entity wording.

```sh
hadron spec link cli:cha:010:04 cli:cha:010:01 -m $M --dry-run
hadron spec link cli:cha:010:04 cli:cha:010:01 -m $M --label "documents retry timing"
```

## Checking a URN you wrote into a spec

A worked example is the one part of a spec a reader *executes*, so when the
prose and the example disagree, **the example wins in practice and the prose
rots unread**. That is not hypothetical: `cor:urn:010:04` stated an App-scoped
memory shape one way and its own example decomposed another way, forty lines
apart, and nothing caught it — nobody diffs a spec against itself.

**Decomposition happens BEFORE lookup, so a failing read names the memory it
tried.** That makes a not-found error a free parser oracle: no fixture, no
library, no checkout, and it works against any deployment.

```
hadron_get_node hrn:node:acme.com::mmdata::services::query   # legacy :: chain, accepted forever
  Memory not found: "acme.com:mmdata:services".

hadron_get_node hrn:node:acme.com:mmdata:services:query      # the v2 form we emit
  Memory not found: "acme.com:mmdata".
```

The quoted name in each answer **is the boundary** — everything the platform
read as the memory, so whatever remains is the loc. Reproduce it verbatim,
trailing period included; that is the message as the server emits it.

Both spellings are legal and they address **different memories** — the v1 `::`
chain uses the *last-segment* reading (the memory is everything but the final
segment), while flat v2 is fixed-arity (the first two atoms). So collapsing the
doubled colons is not a spelling change; it moves the boundary. Run the probe
on every URN literal you put in a spec, and paste the answer beside the prose
before deciding which of the two is wrong.

Three traps, all of them load-bearing:

- **The probe needs the MCP surface, not the CLI.** `hadron node get` renders
  its own not-found message, and GraphQL `resolveUrn` / `node(ref:)` return a
  bare `null` — neither reports the memory that was attempted. Only
  `hadron_get_node` surfaces it (hadron-server `src/mcp/server.ts`, which
  splits and then names what it looked up).
- **Do not substitute a local library for the probe.** `urn-lib-go`'s
  `SplitNodeUrn` applies the flat-v2 rule to `::` chains too, so it disagrees
  with the platform from the fourth segment on — including on `cor:urn:010:04`'s
  own example. A decomposition you did not get from the platform is a guess that
  looks like a measurement. See
  `findings:splitnodeurn-disagrees-with-the-server-on-deep-chains`.
- **A bare citation is decomposable-looking.** `cor:urn:010:04` splits happily
  into memory `cor:urn` + loc `010:04` with no error. When you are checking by
  eye or by script, key on URN *shape* — a scheme prefix, or a `::` chain —
  never on "it parsed".

## Replacing a spec

Numbers are never reused. To change a binding rule, mint a replacement and
retire the old one (it keeps its number, gains a `superseded` tag and a
`superseded-by` edge):

```sh
hadron spec supersede cli:cha:010:01 -m $M --title "backpressure v2" --yes
```

The replacement is created together with its table-of-contents and inheritance
edges. The `superseded-by` edge leaves the *old* spec, so it is a second write.
If that write errors, supersede re-reads the old spec first, because a lost
response looks like a refusal. If the edge shows up, it finishes the
retirement. If it doesn't, that still isn't proof: a read can lag. So the error
says to check with `hadron spec get <old> -m $M`. If the edge is still missing
after a minute, run the
`hadron spec link <old> <new> -m $M --label superseded-by` it names.
Once `spec get` shows the edge, rerunning the same `spec supersede` finishes the
retirement. Don't rerun it *before*: with no edge to find, it mints a second
replacement.

## Citations in source, and keeping them honest

The authoring workflow tells you to point at a spec from the code it governs —
`// Spec: <citation>` near the load-bearing constant, query or handler. That
creates a **second population of citations, outside the graph**, and
`spec lint` cannot see it. Supersede a rule and every one of those pointers now
documents a contract that was deliberately replaced.

```sh
hadron spec citations -m $M --src src/
```

```
LOCATION                CITATION        SEVERITY  RULE         MESSAGE
src/lib/webFetch.ts:4   cor:api:130:02  error     superseded   cites a superseded spec — replaced by cor:api:130:03; …
src/lib/probe.ts:1      cor:api:999:01  error     unresolved   does not resolve in … — a typo, a spec deleted rather …
```

- Matching is anchored on the prescribed `Spec:` prefix, and takes **every**
  citation on that line — real pointers routinely list several
  (`// Spec: cor:api:080:01 (collide vs relocate), cor:api:080:02 (…)`).
  `--loose` drops the anchor and scans every line for citation-shaped tokens,
  which finds pointers written some other way at the cost of prose false
  positives.
- Errors exit **5**, like `spec lint`, so this gates CI. `--src` repeats and
  accepts a file or a directory; `--exclude <glob>` prunes paths (a doc that
  *shows* the pointer form is a true match and a false alarm).
- `--stale-abstracts` adds a warning when a cited spec's **body differs from the
  version its abstract was written against**. Read it narrowly — it is a hash
  comparison, so it fires on a difference that changed nothing the abstract says,
  and not at all on a body edited and then restored byte-for-byte. It is *not* evidence the abstract is wrong:
  measured against embedding similarity it separates the stale cohort from the
  clean one at Cohen's d = 0.01 at the rule tier, while the same metric detects a
  genuinely mismatched abstract at d = 3.29
  ([#352](https://github.com/hadron-memory/hadron-cli/issues/352)). Off by
  default, since two thirds of a live corpus trips it and it is a property of the
  **spec** rather than of the pointer. For the corpus-wide view use
  `hadron memory validate <memory> --check stale-abstract`.

### Two populations — the second is the expensive one

This check catches citations that **went** stale. It cannot catch a claim that
was **never** grounded, and those are more common and cost more. Two real
examples from one client/server pair:

- A comment stated a training's status was "deliberately not bypassable". The
  ratified rule says withdrawal must never strand a mid-run learner. It survived
  a merged PR and was then quoted **in review** to defend the bug it described.
- A comment justified latest-attempt routing as "matching the server, which
  grades the most recent attempt". The server grades the **best** attempt —
  behaviour right, stated mechanism wrong, sitting exactly on a deliberate
  asymmetry.

Neither carried a citation, so no linter would have caught either. That half is
a review-time rule — *treat any claim about another component as a citation that
must resolve* — and belongs in a review checklist (`hadron coding review create`),
not in a scanner.

## Notes

- A memory's data may still carry `spec.scheme` from the retired
  `describe --declare`. It is left in place (no sweep), shown by `describe` as
  retired, and read by nothing (#709).
- `hadron spec import spec-kit|code` is reserved for future import workflows and
  currently exits with a not-implemented usage error; new, edit, extract,
  link, and supersede are the supported write paths today.
