# `hadron automation` — owner approval of schedules and webhooks (cli#1071, S2)

Design-as-built for slice S2 of the automation-adoption plan (approved r4:
`hrn:node:hadronmemory.com:hadron-dev-team-shared:plans:automation-adoption-approval`).
Server contract: hadron-server#2441 (S1, merged `aef8e83b`) and #2434 (`36d2c9b9`).
The snapshot was refreshed from `36d2c9b98` (== origin/main). Every sentence, state and hash is the server's;
the CLI binds `automationApprovals`, `automationApproval`, `approveAutomation`
and `declineAutomation` and derives nothing.

## Surface

| Command | Does |
|---|---|
| `automation list` (alias `ls`) | follows `nextCursor` to the end; rows UNADOPTED/ADOPTED, printed "not approved"/"approved"; the MANUAL `omitted` entry prints "Manual runs are approved from the run that needs them" |
| `automation approval <name> [--depth]` | the plain summary; `--depth` adds graph and hash (`AutomationApprovalDepth`, a separate operation so the graph is only fetched when asked) |
| `automation approve <name> [--yes]` | fetch one projection, show it on stderr, confirm, send THAT hash; never retry |
| `automation decline <name> [--yes]` | kind and ref only; no projection read |

`<name>` is `schedule/<name>`, `webhook/<name>` or a bare name one trigger has;
`--run <run-id>` names a MANUAL origin (exclusive with a name; an explicit blank
`--run` refuses). `--app` is resolved by `cmdutil.TargetApp` (moved out of
`app member`, same rule: explicit blank refuses, ambient App is named in the prompt).

## Decisions

- **Resolution** matches the whole list (all pages). Zero matches exit 4 with one
  line; several exit 2 listing only the matches by kind and headline. The wire has
  no creation time, so the issue's "creation time" is not shown. Two triggers
  sharing kind and name cannot be told apart by a name: the refusal says to rename
  one. No row id or ref is ever typed or printed for selection.
- **Decline needs no projection** (an unreadable automation must stay declinable).
  Its wording comes from the list state: ADOPTED (which may in fact be CHANGED) is
  "switches it off ... including a newer approval", UNADOPTED "only records".
  A MANUAL decline has no list row, so it uses a generic MANUAL sentence and first
  runs `requireSupport` (one list page, only schema skew acted on) so an old server
  is refused before the prompt.
- **Exit codes**: FORBIDDEN 8 (the #2434 read refusals ride it), NOT_FOUND 4,
  `AUTOMATION_CHANGED` / `AUTOMATION_UNADOPTED` 5 (new `codeForExtension` rows),
  selection 2, cancelled 6, 7 for malformed or unconfirmed. Schema skew exits 7
  with its own sentence (the generic `MapError` skew path is exit 2 and is not
  changed).
- **Malformed is unavailable (7)**: a null required list, empty hash, unknown
  state, a `CHANGED` or MANUAL list row, a null page, a repeating cursor. A `true`
  mutation result beside an error is outcome unknown (7), never retried.
- **Terminal safety**: text mode strips escapes and control characters and bounds
  every server string and list; `--json` is unchanged.
- **Legacy**: `previewAutomation` and `reAdoptAutomation` have no call site and
  stay in `unbound-ops.txt`. The schema refresh also lists the unrelated
  operations the snapshot was behind on.
