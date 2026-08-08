# Implementation Plan: `hadron spec citations` — verify that citations in SOURCE still resolve

> **Status: implemented.** Design-as-built for
> [#349](https://github.com/hadron-memory/hadron-cli/issues/349). Decisions are
> recorded with the live evidence that drove them; where building it changed the
> design, [Deviations](#deviations-as-built) says so.

## Context

The add-spec workflow tells authors to point at a spec from the code it governs
(`tasks:mint-spec`, step 3 of *Wire it in*):

> **Pointer from code.** Near the load-bearing constant/query/handler:
> `// Spec: <citation>` (e.g. `// Spec: msg:010:02:03`).

So the CLI actively creates a second population of citations — in source, not in
the graph — and nothing verifies them. `spec lint` checks the corpus; the
references pointing *into* the corpus are unchecked.

That matters most for the one operation designed to invalidate a number.
`spec supersede` retires a citation (tags it `superseded`, wires `superseded-by`)
and every `// Spec: <old>` in source now documents a contract that was
deliberately replaced. The rename-safety the scheme is proud of — never
renumber, supersede instead — holds inside the graph and quietly does not hold
outside it.

`spec check-tools` is the prior art and the mirror image: it scans **specs** for
`hadron_*` tool references that aren't real tools. This scans **source** for
spec references that aren't live specs.

## What the live population looks like

Measured across the sibling checkouts before designing, since the shape of the
real data decides the matcher:

| Repo | `Spec:` pointers |
|---|---|
| hadron-server | 18 |
| hadron-portal | 2 |
| hadron-cli | 1 (a doc example — see Decision 4) |
| hadron-docs | 0 |

Three facts from reading them, each of which changed a decision:

1. **A pointer routinely carries more than one citation.**

   ```
   // Spec: cor:api:080:01 (collide vs relocate), cor:api:080:02 (source
   * Spec: cor:api:050 (feature) · cor:api:050:01 (field-sniff) · cor:api:050:02 (scope/access) · …
   ```

   A matcher that captures one citation per `Spec:` anchor would silently check
   the first and ignore the rest — the same class of silent under-coverage this
   command exists to remove.

2. **The comment marker varies** — `//`, ` * ` inside a block comment, and inside
   a Go raw-string help text. Anchoring on `Spec:` rather than on a comment
   syntax keeps the matcher language-agnostic, which is the point of putting it
   in the CLI rather than in each repo.

3. **Citations appear at every level** — `cor:api:130` (feature),
   `cor:api:080:02` (rule), `cor:acl:070:01`. The matcher must not assume a
   fully-qualified rule.

## Decisions

### 1. Anchor on `Spec:`, then take every citation on the line

Default matching is anchored on the prescribed `Spec:` prefix — the issue's own
"safe default" against citation-shaped tokens in prose, fixtures and URLs. But
per fact 1 above, the anchor selects the **line**, and every citation-shaped
token on the rest of that line is collected, not just the first.

`--loose` drops the anchor and scans every line for citation-shaped tokens. It
requires at least a feature segment (`cor:api:050`, three digits), because a bare
three-letter token would otherwise match ordinary prose.

Every candidate is then validated with `ParseCitation` — the package's own
grammar — so the flat-vs-product-rooted distinction is decided by the same code
that authors specs. That is the argument for this living in the CLI at all: every
consumer repo would otherwise reimplement the regex and get that distinction
subtly wrong.

### 2. Resolve by batch-reading the cited locs, not by scanning the corpus

The obvious implementation lists the whole corpus and matches locs. That reads
hundreds of nodes to check a handful of citations, and CLAUDE.md's
whole-corpus-read rule (#23) means it must also be paginated to exhaustion.

Instead the distinct citations are composed into node refs and read with
`api.CollectNodeBatch` — the same bulk read `check-tools` uses, bounded by the
number of **distinct citations in source** (18 in the largest repo measured), not
by corpus size. Occurrences are then fanned back out from the resolved citation,
so a citation appearing 20 times costs one read.

### 3. Three rules, matching the issue

| Rule | Severity | Catches |
|---|---|---|
| `unresolved` | error | typo, or a rule deleted rather than superseded |
| `superseded` | error | code documents a contract deliberately replaced — the message names the replacement |
| `stale-abstract` | warning, opt-in | the cited spec's body is not the version its abstract was written against |

Errors exit **5** (`exitcode.Silent(exitcode.Conflict)`), as `spec lint` and
`check-tools` do; `--strict` promotes warnings. The issue says "non-zero"; the
established convention is specifically 5.

`superseded` reads the retirement markers `spec supersede` writes — the
`superseded` tag and the `superseded-by` edge — so the finding can name the
replacement citation rather than just condemning the old one.

`stale-abstract` is computed client-side. The schema documents
`abstractOriginHash` as "SHA-256 of plaintext content, truncated to 8 hex chars",
compared at read time against the current content — so the CLI can compute the
same value from the `content` it already batch-read, with no extra query and no
new server surface. It is a warning because it does not make the citation wrong:
see [Deviation 2](#2-stale-abstract-is-opt-in---stale-abstracts-not-default-on)
for what it actually measures, which is less than its name suggests.

**`unresolved` deliberately does not distinguish "does not exist" from "exists
but is not readable by this principal."** `nodeBatch` reports both as
`unavailable`, and the CLI must not turn that into a claim it can't support: the
message names both possibilities. Reporting it is non-negotiable either way —
a citation the check could not verify must never pass as verified.

### 4. False positives are handled by scope, not by cleverness

The one pointer in this repo is a **doc example** in `spec import code`'s help
text (`e.g. // Spec: msg:010:02`), and `msg:010:02` does not exist in this repo's
spec memory. It is a true match of the prescribed form and a false report of a
problem.

Rejected: sniffing for "e.g.", ignoring help strings, an ignore-list file
(check-tools has one, but its manifest is machine-generated and its exceptions
are few and stable — a citation ignore-list would be per-repo state this command
has no home for). Kept: `--src` scopes what is scanned and `--exclude <glob>`
prunes paths, which is what a CI invocation wants anyway.

### 5. Memory comes from the existing resolution, not a new per-repo config file

The issue suggests `.hadron/config.json` as the home for the per-repo memory
constant. Not built: the CLI's config is a single global TOML
(`~/.config/hadron/config.toml`), and `spec` commands already resolve their
memory as **`-m` → `hadron spec use` → the active memory**, with
`HADRON_SPEC_MEMORY` overriding — which covers the CI case (set the env var in
the workflow) without inventing a second config format and a second resolution
order for one command.

A per-repo config file is a real feature with real value, but it belongs to the
whole `spec` group at once, not to whichever command needs it first.

## Command surface

```
hadron spec citations [--src <path>]... [-m <memory>] [--loose] [--exclude <glob>]...
                      [--strict] [--json]
```

Aliased `check-citations`, next to `check-tools`, since that is what it is.
`--src` defaults to `.` and repeats; a root may be a file or a directory.

## Scanning rules

- Directories skipped by default: `.git`, `node_modules`, `vendor`, `dist`,
  `build`, `target`, `.venv`, `venv`, `__pycache__`, `.next`, `.svelte-kit`,
  `coverage`, `bin`. `--exclude` adds globs (matched against the base name and
  the path relative to the scan root), and prunes directories.
- Files over 2 MiB and files containing a NUL byte in their first 8 KiB are
  skipped as binary/generated. Symlinks are not followed.
- **No `.gitignore` parsing.** It is a whole matcher of its own, and the default
  skip-list plus `--exclude` covers the same ground for this purpose. Recorded
  here so the omission is deliberate rather than forgotten.

## Output and exit contract

`--json` is an array of findings, one **per occurrence** (not per citation), so a
CI annotation can point at the line:

```json
[{"file":"src/lib/webFetch.ts","line":4,"citation":"cor:api:130:02",
  "rule":"superseded","severity":"error",
  "message":"…","replacement":"cor:api:130:03"}]
```

Human output is an `output.NewTable` of LOCATION / CITATION / SEVERITY / RULE /
MESSAGE. A clean run prints the counts it actually checked —
`✓ N citation(s) in M file(s) resolve` — and a run that matched nothing says so
explicitly rather than printing a checkmark, since "scanned the wrong path"
otherwise looks exactly like "everything is fine".

## Tests

- **Pure scanner** (`citationscan_test.go`) over a temp tree: the anchored form
  in `//`, `*` and raw-string comments; **multiple citations on one line** (fact
  1 — the case a single-capture matcher fails); a citation-shaped token in prose
  ignored without `--loose` and found with it; the loose matcher not firing on a
  bare three-letter word; binary, oversized, excluded and skip-listed files;
  `--src` naming a single file.
- **Pure rule engine** (`citations_test.go`): superseded (with and without a
  `superseded-by` edge to name), unresolved, stale-abstract (hash match vs
  mismatch, and no content ⇒ no finding), clean.
- **Command level** (`internal/cmd/spec_commands_test.go` style, fake GraphQL):
  `--json` shape, exit 5 on an error, exit 0 with warnings, `--strict`
  promotion, and that the resolve step batch-reads once for a citation repeated
  across files.

## Deviations (as built)

### 1. A citation must carry a feature segment — even after the anchor

Decision 1 allowed a module-level pointer (`// Spec: cor:api`) in anchored mode,
on the reasoning that the `Spec:` prefix is evidence enough. The first test run
killed it: **`ParseCitation` accepts a lone 3-letter code as a flat module
citation**, so with the feature optional every three-letter word on the line
parsed as a citation —

```
// Spec: cor:api:130 (surface contract).  →  cor:api:130, "sur", "con"
// Spec: see the design doc              →  "see", "the", "des", "doc"
```

**As built: a feature segment is required in both modes.** The cost is that a
module- or product-level pointer goes unchecked, which is the right trade: no
live pointer is written that way, and `spec supersede` only retires a numbered
rule or flow, so the retirement class cannot apply to a module root anyway.

### 2. `stale-abstract` is opt-in (`--stale-abstracts`), not default-on

The issue rates it "softer", and Decision 3 shipped it as a default-on warning.
The first live run made that untenable: **all 26 citations in the sibling repos
reported it** — the rule was the entire report.

The computation is not wrong. hadron-server's own `computeContentHash` is exactly
SHA-256-truncated-to-8, the CLI's value matches it, and the server independently
reports `abstract-stale` for the same nodes. Measured across the whole corpus:

```
specs=271  fresh=96  stale=174  no hash/content=1
```

**As built: off by default, `--stale-abstracts` turns it on.** Two thirds of the
corpus trips it, so default-on it buried `unresolved` and `superseded`, the two
rules that name an actually-broken pointer — the coding group's Decision 4
again: a check that fires on everything trains people to ignore the whole
report.

**Later correction — what the flag measures.** The wording above ("64% of the
corpus is stale") and the rule's original message both overstated it, and #352
measured the semantic question directly. `abstractOriginHash` fires whenever the
current body is not the version the abstract was written against — whether or
not the difference touches what the abstract says, and never on an edit that was
reverted byte-for-byte. Against embedding similarity the flagged cohort separates from
the clean one at **d = 0.11 overall and d = 0.01 at the rule tier**, while the
same metric detects a genuinely mismatched abstract at **d = 3.29**. Real drift
in that corpus is ~7 nodes, three of them hash-clean.

So the honest reading is "the body moved under this abstract", not "this
abstract is wrong", and every string describing the rule now says that — matching
`hadron memory validate`, which shipped with the corrected wording
([#355](https://github.com/hadron-memory/hadron-cli/pull/355)).

**Not delegated to `validateMemory`,** though #352 asked for the dedup once a
server-backed path existed. That audit caps its findings (default 200, max
1000), so on a memory with more findings the stale set returns silently
incomplete and a cited spec reads as fresh. The client-side hash is exact and
rides a batch read this command already performs; deduplicating onto a capped
source trades a correct answer for a tidier one.

### 3. A match must be the WHOLE token, which the regex cannot say

The token pattern enforced a *leading* boundary only, so it matched a valid
**prefix** of a malformed pointer and dropped the rest — and since the prefix
usually resolves, the typo was reported as a healthy citation:

```
// Spec: msg:0102            → msg:010            ✗ "resolved"
// Spec: cor:api:130:02:031  → cor:api:130:02:03  ✗
// Spec: cor:api:130.02      → cor:api:130        ✗ (the dot-for-colon typo
                                                     the authoring guide warns about)
```

Silently passing a broken pointer is the exact failure mode this command exists
to remove, so the boundary is now checked in Go (`wholeToken`) over the match
indices: a trailing letter, digit, underscore or colon rejects the match, and a
dot only when a digit follows it, so an ordinary sentence-ending
`// Spec: cor:api:130.` still matches. A rejected token is not reported as a
finding — it is not a citation, and manufacturing one for every citation-shaped
typo in a comment is the false-positive class `--loose` already risks — but it
can no longer pass as valid. (Codex review on #351.)

### 4. Zero citations short-circuits before the memory is resolved

The memory was resolved before the findings loop, so a repo with **no** pointers
failed with a usage error about `-m` instead of answering "nothing found".
Nothing to resolve means nothing to resolve it against: the scan now returns
early, with no GraphQL client and no memory lookup. (Copilot review on #351.)

### 5. Over-long lines are skipped

Re-running against hadron-docs after the short-circuit landed surfaced a third
noise source: MkDocs' `site/search/search_index.json` is the **entire docs
corpus on one line**, prose citations included, and produced dozens of
`unresolved` findings for text that is not a code pointer at all.

A line longer than 4 KB is therefore skipped. Line length is a property of the
artifact; a `site/` skip-list entry would have been a guess about somebody's
directory layout, and a real `// Spec:` comment is never 4 KB wide. hadron-docs
now reports `no spec citations found in 236 file(s)`.

### 6. `generated/` joins the skip-list

The first live run drowned in `hadron-server/src/generated/prisma/*`, where the
Prisma client embeds the schema's comments verbatim: 12 of 26 findings, every one
a duplicate of a real pointer elsewhere in the tree. Generated output is a copy
of a citation, not a pointer anyone maintains.

## Verification (as built)

`go test ./...` and `make lint` (0 issues) green. Live, read-only:

```
$ hadron spec citations -m hadronmemory.com::specs --src ../hadron-server/src
  ✓ 23 citation(s) in 13 file(s) resolve            → exit 0
$ hadron spec citations -m hadronmemory.com::specs --src ../hadron-portal/src
  ✓ 3 citation(s) in 3 file(s) resolve              → exit 0
$ hadron spec citations … --src <probe with a bogus citation>
  cor:api:999:01  error  unresolved                 → exit 5
```

23 citations from 18 `Spec:` lines is Decision 1 working: five of them would have
gone unchecked by a one-citation-per-anchor matcher.

**No live instance of the `superseded` rule exists** — `hadronmemory.com::specs`
currently has zero specs tagged `superseded`, so the corpus has not yet
exercised the failure the issue describes. The rule is covered by unit and
command tests (including the missing-`superseded-by`-edge case, the partial
write `spec supersede` warns about) rather than by live data. That the headline
failure has not happened *yet* is the argument for having the check before it
does.

## Out of scope (follow-ups)

- **The inverse report** (specs with no citing code). Weak by construction —
  plenty of rules are implemented in another repo the scanner can't see — and the
  issue itself rates it "at most an opt-in, and not the headline".
- **`.hadron/config.json`** (Decision 5) — a `spec`-group-wide feature.
- **`.gitignore` awareness.**
- **Claims that were never grounded.** The failure that prompted the issue is
  only half mechanical: two Academy comments asserted another component's
  behaviour, both wrong, neither carrying a citation. No linter catches those;
  that half is a review-time rule (*treat any claim about another component as a
  citation that must resolve*) and belongs in a review node, which
  `coding review create` now serves. This command checks citations that **went**
  stale, not claims that were **never** grounded — the docs say so in as many
  words, because the second population is the more common and the more
  expensive.
- **Auto-fix** (rewriting `<old>` to its `superseded-by` target). Tempting and
  mechanical, but a superseding rule is a *different rule* — whether the code
  still implements it is exactly the judgement the finding is asking for.
