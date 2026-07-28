# How to maintain product specs

`hadron spec` runs a Hadron memory like a legal code: a spec's `loc` **is** its
citation, each colon level is a real parent/child node, numbers are never
renumbered (to replace a spec you `supersede` it), and a fixed rubric (abstract
+ a "what invalidates this spec" statement) is enforced by `lint`.

Every subcommand takes `-m/--memory <org::memory>`.

## Two citation schemes

A memory is either **flat** or **product-rooted** — pick one per memory and
don't mix them (`lint` warns if you do).

```
flat:      <module>:<feature>:<rule>[:<flow>]            msg:010:02:03
product:  <product>:<module>:<feature>:<rule>[:<flow>]   cli:cha:010:01:02
```

- **product** — a shippable artifact (`cli`, `srv`, `por`). 3 lowercase letters.
- **module** — its top-level internal division (a command group, a backend
  service). 3 lowercase letters.
- **feature** — 3 digits, numbered in tens (`010`, `020`, …).
- **rule** — 2 digits, `+1`.
- **flow** — 2 digits, `+1` (pull-on-demand sub-parts of a rule).

A citation is self-describing: if the second segment is letters it's
product-rooted (`cli:cha:…`); if it's digits it's flat (`msg:010:…`). Product
and module codes are **frozen** once created — you never renumber or rename
them.

Use a flat memory for a single product (e.g. one team's `platform-specs`); use
a product-rooted memory when one corpus spans several products (e.g. Hadron's
own `cli` / `srv` / `por`).

### See (or declare) what scheme a memory uses

```sh
hadron spec describe -m hadronmemory.com::platform-specs
```

```
Spec scheme — hadronmemory.com::platform-specs
  scheme:    product  (declared)
  products:  cli, srv
  modules:   cli:cha, srv:gql
  counts:    2 products, 2 modules, 12 features, 40 rules, 8 flows, 5 contracts
  contracts: product <p>:gen · module <m>:000 · feature <m>:<f>:00
```

The scheme is **derived** from the live nodes, and can also be **declared** in
the memory's data (`{"spec":{"scheme":"product"}}`) so an empty memory can
announce its arity before it has any specs. A declaration is authoritative;
`describe` flags any drift from what the nodes actually look like. Declare it
once, up front:

```sh
hadron spec describe -m hadronmemory.com::platform-specs --declare product
```

## General-provisions contracts

Provisions shared across siblings live in a reserved **contract** node that the
siblings inherit from — one per tier:

| shared across… | contract loc | created with the root… | …or retrofitted with |
|---|---|---|---|
| all rules of a feature | `msg:010:00` | `spec new --module msg --new-feature` | `spec new --module msg --feature 010 --contract` |
| all features of a module | `msg:000` | `spec new --module msg --new-module` | `spec new --module msg --contract` |
| all modules of a product | `cli:gen` | `spec new --product cli --new-product` | `spec new --product cli --contract` |

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
M=hadronmemory.com::platform-specs

hadron spec new -m $M cli:cha:010:01 --new-path --title "backpressure"
```

From an empty corpus that one call mints `cli`, `cli:gen`, `cli:cha`,
`cli:cha:000`, `cli:cha:010`, `cli:cha:010:00` and the rule itself, wired
top-down; ancestors that already exist are left alone. `--new-path` takes the
citation *positionally* and refuses to be combined with the tier-selecting flags
(`--product` / `--module` / `--feature` / `--rule` / `--flow` / `--inherit` /
`--new-*` / `--contract`).

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
module codes are frozen — re-minting one exits 5 — and a missing parent tier is
rejected up front (exit 4, nothing written). An edge that can't be wired is the
one failure that half-writes: the node exists, `spec new` exits 1 rather than
report success, and you fix the target and re-run or wire it with `hadron edge
add`.

A flat corpus is identical without the `--product` flag (and `--new-module`
creates a top-level module):

```sh
hadron spec new -m micromentor.org::platform-specs --module msg --feature 010 --title "W4 — 7d check-in"
```

## The rule rubric

`spec new` scaffolds a rule with the sections below. Only the abstract and the
**What invalidates this spec** statement are `lint`-enforced; the rest are
conventions you fill in (and the two *optional* sections you delete when they add
nothing).

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
5. **What invalidates this spec** *(mandatory)* — the changes that repeal or
   supersede it.
6. **Acceptance criteria** *(optional)* — concrete, checkable statements
   engineering or QA can verify, for specs whose behavior must be testable.

Flows (`:NN:NN`) inherit their rule's scenarios and stay terse — they scaffold
only the mandatory rubric.

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

`lint` enforces the rubric (abstract + "what invalidates"), the citation shape,
parent existence, inheritance edges to the tier contract, and the
**one-arity-per-memory** rule.

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
  and `find` add a `MEMORY` column naming each hit's `<org>::<memory>`; scoped
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

## Replacing a spec

Numbers are never reused. To change a binding rule, mint a replacement and
retire the old one (it keeps its number, gains a `superseded` tag and a
`superseded-by` edge):

```sh
hadron spec supersede cli:cha:010:01 -m $M --title "backpressure v2" --yes
```

## Notes

- A memory's declared scheme lives in its `data` bag under `spec.scheme`
  (`hadron spec describe --declare …` writes it; `describe` reads it). It is
  optional — `describe` derives the scheme from the live nodes for any
  non-empty memory — but declaring it up front lets an empty memory state its
  intended arity and lets `describe` flag drift. See
  [docs/plans/spec-product-level.md](../plans/spec-product-level.md).
- `hadron spec import spec-kit|code` is reserved for future import workflows and
  currently exits with a not-implemented usage error; new, edit, extract,
  link, and supersede are the supported write paths today.
