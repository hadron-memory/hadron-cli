# Removing the fixed spec hierarchy (#708, #709)

> **Status: slices A and C built (cli#710); B and D planned.** Written 2026-09-24 by Jonas
> (cli-engineer) on Ada's dispatch (team chat #1473), under Holger's
> authorization of the same day. The authorization covers removing the legacy
> spec-corpus hierarchy checks and the flat/product concept **before** a
> replacement design exists. It does NOT cover implementing the stale
> September 23 types/pen/attestation design. This plan is removal only: no new
> taxonomy, no new numbering policy, no new schema fields.

## 1. What "the hierarchy" is, and where it lives

A spec's loc is its citation. Until now the CLI also required that citation to
fit one grammar:
- flat: `<module>[:<feature>[:<rule>[:<flow>]]]`;
- product-rooted: `<product>:<module>…`.

Modules are 3 lowercase letters, features 3 digits, rules and flows 2 digits,
with at most five segments. Depth carried meaning. Level 1 was a module, 2 a
feature, 3 a rule and 4 a flow, and each tier had a reserved
"general-provisions" contract (feature `:00`, module `:000`, product `:gen`)
that its siblings were expected to inherit.

**The server enforces none of this.** Dara's server#1312 audit (team chat
#1483) found the only server loc rule is the generic `validateLoc`, and spec
governance keys on `role = 'spec'`. So the hierarchy is a CLI construct, and
removing it is a CLI change.

**All of it hangs off one parser**, `ParseCitation` in
`internal/cmd/spec/spec.go`, plus the `Citation` methods `Level`, `Parent`,
`IsContract`, `InheritedContractLoc` and `ChildContract`. There are about 90
call sites, all inside `internal/cmd/spec`; nothing outside the package imports
them. They fall into six groups:

| Group | Where | What it did | Slice |
|---|---|---|---|
| Address grammar | `edit`, `link`, `get`, `find` | Refused any address that did not fit the grammar | **A** (`extract`'s source is legacy allocation: **C**) |
| Silent scan filters | `list`, `get --prefix`, `grep`, `replace`, `check-tools` (plus scans inside `new`, `extract`, `supersede`, `lint`) | Dropped a tagged spec whose loc did not fit, with no count and no note | **A** (read commands), **C** (authoring scans), **B** (lint) |
| Tier-derived lint | `lint.go`, `lintindex.go` | Parent must exist, TOC edge to parent, inheritance edge to the tier contract, index lists its children, a rubric only at rule/flow depth, `loc-shape` | **B** |
| Authoring adapters | `new`, `allocate`, `extract --to-feature`, `supersede`, `register` | Allocate the next legacy number, co-scaffold contracts, require tier parents, supersede only rules/flows | **C** |
| Scheme | `describe`, `lint`'s `mixed-arity` | Infer or declare flat/product, and warn on mixing | **D (#709)** |
| Prose citations | `citations` (`citationscan.go`) | Recognize citation-shaped tokens in text | **C** (reported seam) |

## 2. What a spec is now: measured, not assumed

Without the shape filter, something else has to say which nodes are specs.
**The rule is: the `spec` tag, or the governed `role: spec`.**

It was measured read-only on 2026-09-24 against both production spec corpora:

| | hadronmemory.com:specs | micromentor.org:specs |
|---|---|---|
| nodes | 369 | 229 |
| citation-shaped | 365 | 219 |
| … of which tagged `spec` | 365 | 219 |
| … of which `role: spec` | 347 | 1 |
| not citation-shaped | 4 | 10 |
| … of which tagged `spec` or `role: spec` | 0 | 0 |

**The tag marks exactly the nodes the shape filter kept**, so switching the
predicate drops nothing and admits nothing on today's data. The role alone
would miss 218 of 219 Micromentor specs, so it can't be the only marker. It is
accepted as well because it's what the server governs by.

**Known limit:**
- `NodeFilter` has no role facet, so the corpus **scans** still select by the
  `spec` tag server-side.
- A spec carrying the role but no tag is found by address (`get`, `edit`,
  `link`) but not by a scan.
- No such node exists in either corpus, and every CLI spec write sets the tag
  (`specTags`).
- Dropping the server-side tag filter to close this isn't an option: `spec
  list` without `-m` scans every readable memory.

## 3. The validity floor

Every spec address now passes exactly one shape check: `validateSpecLoc`, which
is `cmdutil.ValidateURNPath`. That's the generic node-loc rule of
colon-separated slug atoms, the same rule every node in every memory obeys and
the one the server enforces. What's still refused:
- an empty segment (`msg::010`);
- whitespace inside a loc. Surrounding whitespace is trimmed first, as
  `ParseCitation` always did (@copilot on #710; kept on purpose, see §6);
- a leading or trailing colon;
- anything else that isn't a valid atom;
- linking a spec to itself;
- linking or editing a node that isn't a spec.

## 4. Slices

### A. Addressing: built (cli#710)

- `validateSpecLoc` replaces `ParseCitation` on every address a user types:
  - `spec get <citation>`;
  - `spec edit <citation>`;
  - `spec link <from> <to>`;
  - and, through `fetchSpecTaggedNode`, every command that reads one spec by
    address.
- `isSpec` (tag or role) replaces "tagged or citation-shaped" in `spec find`,
  and "tagged" in the address and link guards.
- The redundant shape filter is removed from `spec list`, `spec get --prefix`,
  `spec grep`, `spec replace` and `spec check-tools`. Each already selected the
  corpus by tag server-side, so the filter could only ever **silently drop** a
  tagged spec for its shape.
- `supersede` keeps its legacy behaviour until C. It parses the old grammar
  itself, because successor allocation is legacy numbering.
- Help (`get`, `list`) and `agentic-usage.md` now describe the legacy
  numbering as a convention that `spec new` produces, not a rule other
  commands enforce.

**Tests:**
- `spec_hierarchy_neutral_test.go` runs the real `get`, `list` and `link`
  commands over five shapes: a legacy flat citation, a legacy product-rooted
  flow at the old maximum depth, one segment, words with no tier shape, and a
  path deeper than the old ceiling.
- The controls refuse generically invalid locs before any request, on `get`,
  `edit` and `link`, and refuse a self-link.
- `TestIsSpecIgnoresLocShape` pins the predicate.
- Three existing tests pinned the old rule and were rewritten to the new one:
  - `find` counting an untagged citation as a spec;
  - the null-tags render reached through that path;
  - `get register` expecting a usage error.
- **Mutation-checked**, each mutation compiled and each run uncached:
  - restoring the grammar on addresses: red;
  - removing the generic check: red;
  - restoring the `list` shape filter: red;
  - dropping the role from `isSpec`: red.

### B. Lint: planned

Remove the rules whose only basis is the tier grammar:

| Rule | Severity | Basis |
|---|---|---|
| `loc-shape` | error | the grammar itself |
| `parent-exists` | error | a mandatory tier parent |
| `toc-edge` | warning | a mandatory edge to the tier parent |
| `inheritance-edge` | warning | the tier contract |
| `placeholder-contract` | info | the tier contract |
| `index-incomplete` | (lintindex) | a header tier must list its children |

**Kept:**
- `duplicate-loc`;
- the URN-example check (#527);
- the name prefix;
- the abstract fingerprint and length checks;
- `unavailable`;
- `data-version` and the scaffold-body guard, if the rubric stays (below).

Explicit edges keep their dangling-target checks.

**Open, and not decided here: the rubric.** `abstract` and `invalidates` as
errors, plus `data-version` and `scaffold-body`, run only at rule/flow
**depth**, which is a depth-derived obligation. There are three choices, put
to Vera and Ada in team chat #1484:
1. Drop the rubric from lint until per-type rules exist (#684).
2. Apply it to every spec. That's stricter than today, so it's new policy.
3. Keep it only for legacy rule/flow-shaped locs, as an adapter: no change for
   today's corpora, and no obligation for other shapes.

### C. Authoring adapters: built (cli#710)

- **`spec new <loc> --title <title>`** creates exactly that spec, at any valid
  loc, through the spec door:
  - nothing is derived from the loc's shape, so there's no parent, no contract,
    no index obligation and no allocation;
  - the only edge is an explicit `--inherit <loc>`, written inline, and it must
    resolve first;
  - a loc that already holds a node is refused (pre-checked, and the server
    doors are create-only);
  - the body is the rubric scaffold, including the optional sections, and the
    abstract is the placeholder;
  - a numeric last segment sets `seq`;
  - combining a positional loc with the tier flags is refused, since those
    select legacy numbering.
- **The legacy flags stay as optional adapters:**
  `--product/--module/--feature/--rule/--flow`, `--new-*`, `--contract` and
  `--new-path`. `--new-path` on a non-legacy loc now says to drop `--new-path`
  instead of printing the grammar error.
- **`spec supersede` works on any spec.**
  - `--to <loc>` names the replacement: any valid, free loc, never the old one,
    with no derived edges. It can't be combined with `--feature/--rule-after`.
  - Without `--to`, a legacy rule/flow citation gets an allocated number
    exactly as before. Anything else is refused with a pointer to `--to`, since
    no numbering policy is invented.
  - The resume/finish, re-read-before-retire and concurrent-successor guards
    are untouched. Their messages now name the node's own loc rather than a
    re-formatted citation.
- **`spec register`** stays the legacy-numbering ledger. Specs at any other loc
  are **named** in a new `outsideNumbering` field (`[]` when there are none)
  and in the human output, never dropped.
- **Reported seams, not generalized:**
  - `spec extract` allocates under the source's legacy module, so a source
    outside the numbering is refused with a pointer to `spec new <loc>` +
    `spec edit`.
  - `spec citations` recognizes only numbered legacy citations in source text
    (a feature segment is required). Recognizing an arbitrary loc in prose
    would need a new pointer policy, so its help now says a pointer to any
    other loc is not checked.

**Tests** (`spec_hierarchy_neutral_test.go`):
- `new <loc>` at four shapes, checking loc, name, tag, role, no derived edges,
  `seq` and the scaffold, and that no scan happens;
- an explicit `--inherit` written inline by resolved id;
- `--dry-run`;
- an occupied loc refused;
- the refusals: invalid loc, tier flags, `--no-contract`, self-inherit, and
  `--new-path` on a non-legacy loc;
- `supersede --to` from a non-legacy spec, a legacy module header (once
  refused) and a legacy rule, with no scan, no derived edges, the old loc kept
  and retired;
- without `--to`, non-legacy specs are refused with nothing written;
- `--to` refused when occupied, self, invalid, or combined with `--feature`;
- `register` naming specs outside the numbering, and `[]` when there are none.

**Mutation-checked**, each mutation compiled and each run uncached. Each of
these turns a test red:
- no existence check;
- `seq` never set;
- the supersede depth gate restored;
- no `--to` occupancy check;
- `register` dropping specs outside the numbering;
- `new <loc>` deriving a parent edge.

### D. Scheme (#709): planned

- `describe --declare flat|product` is retired. It refuses with an explanation
  and writes nothing.
- A stored declaration is left in place and no longer read as policy.
- Scheme inference and `lint`'s `mixed-arity` warning are removed.
- `describe` remains a neutral read-only inventory, with no tier classification
  by depth.

## 5. Safety boundary: unchanged by every slice

These are all preserved:
- **Stored data:** nodes, locs and citations, IDs, content, edges and revision
  history. There is no flattening, renumbering, deletion or migration.
- **Guards:** memory authorization, the governed spec-write doors, generic
  loc/URN validation, uniqueness, explicit edge-target checks, atomic writes,
  and truthful failure/retry.
- **What "flat" means here:** the spec-corpus scheme, never the flat-v2 URN
  grammar.

## 6. Review notes

- **Surrounding whitespace is trimmed, not refused** (@copilot on #710).
  `ParseCitation` trimmed it before this change, so refusing it would be a new
  failure for any script passing a quoted, padded argument. The generic rule
  still refuses whitespace inside a loc. Trimming changes nothing about which
  node is addressed: no stored loc has surrounding whitespace, because the
  server's `validateLoc` refuses it.
- **A and C ship together** in cli#710. Copilot's review of A alone found that
  `extract` and `supersede` still behaved legacy-only in between. Those are
  exactly slice C's changes, so C was folded in instead of leaving an
  intermediate state (and a stacked PR) behind.
