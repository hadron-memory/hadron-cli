# `hadron decision` — decision logs and decisions (design as built, cli#1335)

Gate: Xabat's CHECKED on hadron-cli#1335 (comment 6540704302), T1. Server surface:
hadron-server #1641 (`resolvers.decisions.ts`), all ten operations on main.

## Surface

| Command | Operation |
|---|---|
| `decision log create <memory> --loc --decider… [--eligible…] [--name] [--prefix]` | `createDecisionLog` |
| `decision log get <log>` | `decisionLog` |
| `decision log set-deciders <log> --decider… [--eligible…] --expected-revision --reason` | `setDecisionLogDeciders` |
| `decision ask <log> --input-file [--title --context --tier --urgency]` | `askDecision` |
| `decision get <decision>` / `get <code> --log <log>` | `decision` |
| `decision list (--log \| --awaiting-me) [filters] [--limit --offset]` (alias `ls`) | `decisions` |
| `decision amend <decision> --expected-revision --reason [--input-file] [field flags]` | `amendDecision` |
| `decision withdraw <decision> --expected-revision --reason [--yes]` | `withdrawDecision` |
| `decision verify <decision>` | `verifyDecision` |
| `decision purge <decision> --reason [--yes]` | `purgeUnansweredDecision` |

## Decisions made at build time (within the gate's "flag shapes may refine")

- **Nested inputs come from `--input-file`.** `AskDecisionInput`/`AmendDecisionInput` carry
  parts, options, recommendations and refs; flags for those would be a second, lossy
  grammar. The file is decoded strictly into the generated input struct (unknown keys
  refused, exit 2, before any call); the four scalar fields also have flags that override
  the file. Decoding into the generated struct is also what lets the coverage matrix see
  every input field as reachable (`scripts/unboundops` counts a `Decode(&input)`).
- **Every optional input field is `omitempty`** (operation-level `for:` directives in
  `decision.graphql`). Amend reads an omitted field as "unchanged", and `DecisionRefInput`
  is exactly one of ref/node/spec, so a sent `null` would be a different request.
  Consequence (named limit): an empty list in the file is the same as omitting it, and an
  explicit `null` cannot be sent.
- **`revision` in JSON** is `revSeq` renamed: it is the value `--expected-revision` takes.
- **`evidence.approval` is flattened to `approval`** in the decision DTO.
- **List flags** (`--decider`, `--eligible`, `--state`, `--urgency`) are all `StringSlice`
  (repeat or comma-separate), consistent within the group.
- **`list` is one server page**, not paged to exhaustion: the server pages
  (`DecisionPage.total`) and caps filtered candidates at 5000; the caller pages.
- **No client authorization.** The CLI refuses only usage mistakes; `purge`'s
  platform-admin rule and every other authority stay server-side (cor:api:240:01).
- **No answer verb.** The server has no `answerDecision` yet (a later #1641 slice).

## Tests

`internal/cmd/decision_cmd_test.go`, fake-GraphQL: one call per verb, refs verbatim,
omitted optionals omitted, `--authorizes=false` sent, strict input decoding, destructive
confirmation before the call, `[]`/`null` JSON shapes, refusals mapped with no follow-up.
15 guard mutants killed (confirmations, strict decode, trailing data, empty amend, by-code
selector, false filter, list scope, blank reason, required file, omitempty tags, nil
results, unset `--eligible`).
