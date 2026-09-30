# Design as built: `spec replace` in a draft corpus (cli#777)

> Built on `main` against server `main` `0a4a97d` (contains #1451 draft state,
> #1452 placeholders and #1459 the governed spec door, merged `754d2ed0`).
> Independent of the stacked cli#779/#780: it needs only the draft state and
> the door, both on server `main`. Law: `cor:spc:020` (Vera, minted 2026-09-29);
> the door's own citation is pending (#4421).

## What changes

`spec replace` reads the corpus state once (`SpecCorpusState`) and picks the door:

| Corpus | Door | Governed specs | Apply |
|---|---|---|---|
| DRAFT | `searchReplaceInSpecNodes` (#1459) | searched | bound to the preview's `plan` |
| minted, or a server without `corpusState` | `searchReplaceInNodes` (generic) | skipped, counted (#659) | as before |

`--json` gains `door` (`"spec"` / `"generic"`) and `skipped[]`
(`{citation,nodeId,reason}`, always `[]` on the generic door).

## Decisions

- **The apply sends the preview's plan back, with identical inputs.** The
  door fingerprints the selected rows, matches, protections and corpus state;
  any change refuses the whole apply with zero writes
  (`SEARCH_REPLACE_PLAN_STALE`, exit 5, message "NOTHING was written").
  Holger chose this strict binding on #1294 (team chat #4020).
- **The door takes the RAW pattern.** The generic door gets a `\b`-wrapped
  regex the CLI builds; the spec door has its own `wordBoundary` flag
  ("matches the CLI's default ASCII word-boundary behavior"), so the pattern
  and `--word-boundary` / `--regex` / `-i` are sent as given.
- **`--max-specs` is sent as `maxNodesChanged`** as well as checked locally, so
  the server refuses an apply over the limit atomically; its refusal
  (`SEARCH_REPLACE_MAX_NODES_CHANGED`) exits 2, like the CLI's own pre-check.
- **Exit codes.** `SEARCH_REPLACE_PLAN_STALE`, `_NOT_DRAFT`, `_MIXED_CORPUS` →
  5 (state). `_NO_SELECTION`, `_NO_FIELDS`, `_BAD_LIMIT`, `_PLAN_REQUIRED` are
  NOT mapped: the CLI never sends those inputs, so one arriving is a CLI
  defect and the generic 1 is honest.
- **An older server** has no `corpusState`: the read fails validation and the
  generic door runs, exactly as before. The test fake answers the state read
  that way by default, so every existing replace test exercises it.
- **Baseline.** `searchReplaceInSpecNodes` is wired; `reserveSpecCitation`
  (server#1452, now on `main`) is annotated: cli#779 wires it.

## Rebase note for cli#779

cli#779 defines `SpecCorpusState` in the same file with `corpusMintedAt`
(#1462, not yet on server `main`). Whichever lands second keeps one
definition — #779's, once #1462 merges.

## Evidence

- 4 command tests: the apply sends the exact plan with identical inputs and
  never the generic door; a stale plan exits 5 saying nothing was written; a
  dry run is one preview naming the skipped specs; a minted corpus keeps the
  generic door. The 9 existing replace tests pass unchanged.
