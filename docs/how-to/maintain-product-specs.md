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

| shared across… | contract loc | created with |
|---|---|---|
| all rules of a feature | `msg:010:00` | `spec new --feature 010 --contract` |
| all features of a module | `msg:000` | `spec new --module msg --contract` |
| all modules of a product | `cli:gen` | `spec new --product cli --contract` |

The contract spelling follows each tier's alphabet — the numeric tiers use their
"zero" (`00`, `000`), and the alpha module tier uses the reserved code `gen`.
`--contract` always scaffolds the contract at the **deepest tier you name**, so
you don't have to remember which spelling applies. A new sibling automatically
gets an inheritance edge to the contract when it exists.

## Scaffolding a product corpus

Create top-down; each level must exist before its children. Use `--dry-run` to
preview any step without writing.

```sh
M=hadronmemory.com::platform-specs

# 1. the product root
hadron spec new -m $M --new-product --product cli --title "Hadron CLI"

# 2. (optional) the product's general provisions, inherited by every module
hadron spec new -m $M --product cli --contract --title "CLI-wide provisions"

# 3. a module under the product
hadron spec new -m $M --product cli --new-module --module cha --title "chat command group"

# 4. (optional) the module's general provisions, inherited by every feature
hadron spec new -m $M --product cli --module cha --contract --title "chat provisions"

# 5. a feature, then its rules
hadron spec new -m $M --product cli --module cha --new-feature --title "streaming"
hadron spec new -m $M --product cli --module cha --feature 010 --title "backpressure"
```

A flat corpus is identical without the `--product` flag (and `--new-module`
creates a top-level module):

```sh
hadron spec new -m micromentor.org::platform-specs --module msg --feature 010 --title "W4 — 7d check-in"
```

## Navigating and validating

```sh
hadron spec ls   -m $M --prefix cli            # one product (or cli:cha for one module)
hadron spec ls   -m $M --prefix cli:cha:010    # one feature and its rules/flows
hadron spec get  cli:cha:010:01 -m $M          # one spec + lint summary
hadron spec find "backpressure" -m $M          # semantic search, filtered to specs
hadron spec lint --product cli -m $M           # lint one product
hadron spec lint --all -m $M --strict          # lint the whole corpus, warnings = errors
hadron spec register -m $M                      # derived number ledger (next-free at each tier)
```

`lint` enforces the rubric (abstract + "what invalidates"), the citation shape,
parent existence, inheritance edges to the tier contract, and the
**one-arity-per-memory** rule.

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
