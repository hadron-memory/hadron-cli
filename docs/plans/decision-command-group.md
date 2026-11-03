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
- **Optional input fields are `omitempty`** (operation-level `for:` directives in
  `decision.graphql`). Amend reads an omitted field as "unchanged", and `DecisionRefInput`
  is exactly one of ref/node/spec, so a sent `null` would be a different request.
- **Except four presence-sensitive lists, which carry no `omitempty`** (Juliet's blind
  P2s on `0ee7da54`): amend's `blocks`/`followUps`/`sources`, ask's `deciders`, and
  set-deciders' `$eligibleDeciders`. For each, server `main` reads `null` as
  "unchanged/default" and `[]` as "clear" (`patch.blocks != null`,
  `eligibleDeciders == null ? prior : given`, `input.deciders ? … : defaults` in
  `resolvers.decisions.ts`). So an absent key decodes to nil and goes out as `null`, and an
  explicit `[]` goes out as `[]`. genqlient's `pointer: true` points at list elements,
  not the list, so dropping `omitempty` (explicitly `false` for the struct-reference
  lists) is the presence-preserving encoding. `amendIsEmpty` counts these by presence.
- **Amend `context` is tri-state** (Gizelle's LOCAL P2): the server reads absent as keep,
  `null` as clear and a string as set (`patch.context !== undefined`). `*string` cannot
  carry that, so `AmendDecisionInput.context` is bound to `json.RawMessage` (omitempty):
  an absent key stays nil and is omitted, and a literal `null` is kept as `null`.
- **Strict keys.** Besides `DisallowUnknownFields`, `checkKeys` walks the file against
  the target type's exact JSON names, refusing a duplicate key (encoding/json keeps the
  last) and a mis-cased one (encoding/json matches case-insensitively). The scan reads
  numbers as text (`UseNumber`) and fails closed: only a syntax error is left to the
  strict decode, because the strict decode never parses an opaque `json.RawMessage`
  value and so would not see any other scan error again (#1558).
- **`revision` in JSON** is `revSeq` renamed: it is the value `--expected-revision` takes.
- **`evidence.approval` is flattened to `approval`** in the decision DTO.
- **List flags** (`--decider`, `--eligible`, `--state`, `--urgency`) are all `StringSlice`
  (repeat or comma-separate), consistent within the group.
- **`list` is one server page**, not paged to exhaustion: the server pages
  (`DecisionPage.total`) and caps filtered candidates at 5000; the caller pages.
- **No client authorization.** The CLI refuses only usage mistakes; `purge`'s
  platform-admin rule and every other authority stay server-side (cor:api:240:01).
- **Deciders are user IDs.** Server `main` (`assertDecidersEligible`,
  `resolvers.decisions.ts`) looks each one up as a user who can write the log's Memory;
  a worker or URN is refused (`DECISION_DECIDER_NOT_ELIGIBLE`). Help says user ID; the
  value is still passed verbatim.
- **`withdraw` confirms with `cmdutil.Confirm`**, not `ConfirmDeletion`: a withdrawn
  decision stays readable, so a "Delete …? cannot be undone" prompt would misdescribe it.
  `purge` (a real hard delete) keeps `ConfirmDeletion`.
- **A blank `--input-file ""` is refused** (exit 2), not read as an absent flag.
- **No answer verb.** The server has no `answerDecision` yet (a later #1641 slice).

## Tests

`internal/cmd/decision_cmd_test.go`, fake-GraphQL: one call per verb, refs verbatim,
omitted optionals omitted, `--authorizes=false` sent, strict input decoding, destructive
confirmation before the call, `[]`/`null` JSON shapes, refusals mapped with no follow-up.
17 guard mutants killed (incl. blank `--input-file`, withdraw prompt wording; (confirmations, strict decode, trailing data, empty amend, by-code
selector, false filter, list scope, blank reason, required file, omitempty tags, nil
results, unset `--eligible`)).
