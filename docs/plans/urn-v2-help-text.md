# Advertise grammar v2 everywhere (#372)

Design-as-built for hadron-cli#372.

## The rule

**Emit v2, accept everything.** The CLI has accepted every URN spelling since
#239 and always will. What it *advertises* — flag descriptions, error hints,
examples, `Long` prose, the embedded agent contract, shipped docs — must be
grammar v2 only:

| kind | advertised form |
|---|---|
| memory | `hrn:mem:<root>:<slug>` |
| node | `hrn:node:<root>:<slug>:<loc>` |
| agent / app / org | `hrn:agent:…` / `hrn:app:…` / `hrn:org:…` |

Nothing about parsing changed. The acceptance matrix is re-verified against a
live server below.

## Why it is not cosmetic

`--help` is documentation of record for both audiences this CLI serves, and the
second one — agents — copies it verbatim. hadron-portal made fully-qualified
`hrn:` refs mandatory (hadron-portal#728); its first CLI examples came out v1
**because that is what `--help` advertised**, costing two review-bot catches and
a follow-up commit. Every repo that adopts the rule hits the same trap while our
own strings point the other way.

The reported symptom was one error message:

```
$ hadron coding review list
hadron: -m/--memory is required (org::memory)
```

## What "everywhere" turned out to mean

The issue listed three instances. The sweep found four distinct categories, and
the last two were only visible once a guard test existed:

1. **Flag descriptions** — `memory to lint (org::memory)` and 27 siblings.
2. **Error hints** — including the `node get` message the issue quotes, plus
   the `-m` hints in `noderef.go`, `headless.go`, `chat`, and `spec`.
3. **Concrete examples** — ~190 of them (`-m acme.com::kb`,
   `hadronmemory.com::dev::start-here`, `--agent acme.com::support-bot`). This
   is the largest category and the most consequential, because an example is
   what people paste.
4. **Factually wrong strings** that only the v2 sweep surfaced:
   - `agentic-usage.md` documented the edge URN as
     `hrn:edge:<org>::<memory>::<loc>`. The server composes
     `hrn:edge:<root>:<memory>:<loc>` (schema.graphql:1505) — the doc was
     mixing a v2 scheme with a v1 separator, a form that exists nowhere.
   - Same mixed-grammar shape in two examples: `hrn:app:acme.com::support`,
     `hrn:app:acme.com::inbox-bot`.

## The one behaviour change, and why it belongs here

`memory clone --target-urn` and `memory extract <targetUrn>` gated on

```go
if !strings.Contains(targetURN, "::") { … }   // "must be a fully-qualified \"org::slug\" memory URN"
```

That is a **v1-only input gate**: it rejects `hrn:mem:<root>:<slug>` — the exact
form the server documents for `targetUrn` on both `cloneMemory` and
`extractParentNodeToMemory` ("a fully-qualified `<root>:<slug>` URN"). So a
caller held to fully-qualified refs could not use these two commands at all.
That is very likely the concrete sense in which #372 "blocks the portal", and it
is not something a help-text edit could fix — updating the docs without the gate
would have shipped a documented example that fails.

Both now gate on `cmdutil.MemoryParts`, which accepts every spelling and still
rejects a genuinely relative value (the only thing the gate was ever for).

**Deliberately not changed: the wire value.** Both still send `<root>::<slug>`,
the shape the server has always been handed here. Widening the *input* is what
unblocks the caller; flipping what goes over the wire is a separate step that
wants live verification of `cloneMemory`/`extractParentNodeToMemory` against a
v2 `targetUrn`, and creating memories on a live server was out of scope for a
help-text change. Tracked as a follow-up.

## The guard

A one-time sweep of 190 strings decays immediately. `internal/cmd/urn_grammar_help_test.go`
holds three tests, modelled on the `list_naming_test.go` pair that keeps `ls`
from creeping back:

- `TestHelpTextUsesV2UrnGrammar` — walks the whole cobra tree: `Short`, `Long`,
  `Example`, and every flag's usage string.
- `TestAgenticUsageUsesV2UrnGrammar` — the embedded agent contract.
- `TestShippedDocsUseV2UrnGrammar` — README, `docs/how-to/`, `plugins/`.

It matches placeholder forms (`<org>::<memory>`, `org::memory`), concrete legacy
examples (`acme.com::kb`), and the mixed-grammar shape (`hrn:mem:…::…`) that is
simply invalid.

**The exemption is the interesting part.** Documenting a legacy form *as legacy*
is correct and must stay — the acceptance promise is real and a reader with old
muscle memory needs it. So a line is exempt when it says so in words ("legacy",
"also accepted", "still accepted", "accepted forever"). That keeps the two
surfaces that legitimately enumerate spellings — the URN-grammar reference in
`agentic-usage.md`, and `access check`'s `Long` — intact and honest, while
banning v1 anywhere it would read as the expected spelling.

`docs/plans/` is excluded: those are design-as-built records of what was true
when written, not instructions to follow.

The guard paid for itself immediately — categories 3 and 4 above were found by
it, not by the grep that started the work. Review then found the gap it still
had: it caught v1 `::` but not the scheme-less single-colon **node** form,
which is not legacy but *invalid* (a loc carries its own colons, so
`<org>:<memory>:<loc>` is ambiguous and refused with exit 2). The shipped
`use-hadron-cli` skill advertised exactly that as THE node reference, so every
node example an agent copied from it failed. The detector now covers it, and
the skill is corrected — along with an `edge add --label` example there and in
`agentic-usage.md`, for a flag that does not exist (`edge add` takes `--name`).

The exemption widened with it: a line may also show a non-canonical form when
it is *warning* about one ("ambiguous", "rejected", "invalid", "exit 2"). Both
exemption classes teach the reader something true; neither presents v1 as the
spelling to use.

One transcript is deliberately left in v1. `docs/how-to/maintain-product-specs.md`
shows `spec describe` printing `hadronmemory.com::platform-specs`, which is what
it really prints — `canonicalMemoryURN` → `memoryRefV1`, the documented
exception in CLAUDE.md (a flat v2 node URN cannot round-trip a COMPOUND app-mem
memory). Rewriting the transcript would have made the doc lie. That the `spec`
group still *emits* v1 to the user is a separate question, noted for follow-up.

## Acceptance, re-verified live

| ref | result |
|---|---|
| `hrn:node:hadronmemory.com:hadron-cli:preflight` | exit 0 |
| `hadronmemory.com::hadron-cli::preflight` | exit 0 |
| `urn:node:hadronmemory.com::hadron-cli::preflight` | exit 0 |
| `-m hrn:mem:hadronmemory.com:hadron-cli` | exit 0 |
| `-m hadronmemory.com:hadron-cli` | exit 0 |
| `-m hadronmemory.com::hadron-cli` | exit 0 |
| `hadronmemory.com:hadron-cli:preflight` (ambiguous) | exit 2, unchanged |

The last row matters: a scheme-less single-colon node ref stays rejected. There
is no scheme-less v2 node form — a loc contains single colons, so
`org:memory:loc` is ambiguous by construction. That is precisely why the new
advice leads with the **prefixed** `hrn:node:…`, which is the only unambiguous
spelling, and it is what the issue's own suggestion asked for.
