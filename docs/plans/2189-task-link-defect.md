# `team task link-defect` (#2189, S12b)

**As built.** A thin client over `linkDefectToPullRequest` (hadron-server#4280, PR #4284, merge `696ed50c`; Proposed cor:agt:130:05), per Hugo's restatement and Xanthos's CHECKED gate (6951297903).

## Schema

The committed snapshot is already at hadron-server `33e89dcc` (#2269), which descends from `696ed50c`, and carries the mutation and `DefectLink`. No refresh in this PR.

## Behaviour

- `team task link-defect <pr-build-task> --defect <issue URL> [--introduced-at-head <sha>]` sends `TaskLinkDefect` once.
  - It sends `prTaskRef` (the task ref, qualified by `--app` like every task verb), `defectRef` as typed and `introducedAtHead` only when the flag is given (omitempty).
  - It never sends `instruction` (that argument stays `gap #1617` in the coverage matrix, like the other task writes).
- **Attribution:** `linkDefectToPullRequest` joins `authoredWriteField`, so the bound session rides `X-Hadron-Session` and a bound worker is the actor. The server admits a `qa-engineer` or `coordinator` worker, or a person who manages the App.
- **Output:** text prints "linked" or "already linked (nothing written)", then the task, defect, introduced head ("none" when null), event and linked-at. `--json` is an explicit six-field DTO mirroring `DefectLink`. An empty response is an error that tells the caller to read the events before retrying, never a fabricated receipt.
- **Exit codes** (`internal/api/errors.go`):

  | Code | Exit |
  |---|---|
  | `DEFECT_LINK_FORBIDDEN` | 8 |
  | `PR_REFERENCE_MISSING` | 5 |
  | `DEFECT_HEAD_NOT_FROZEN` | 5 |
  | `TASK_NOT_FOUND` | 4 (the existing `_NOT_FOUND` rule) |
  | `BAD_USER_INPUT` | 2 (existing) |

  The three new codes join `taskErrorCodes`, so they are prefixed in the message and kept as `serverCode` (via `mapFindingError`). No retry on any path.
- **Local refusal:** an empty or whitespace-only `--defect` exits 2 before any request. Nothing else is validated client-side: the URL form and the head's shape and frozen state are the server's.

## Tests

`internal/cmd/team_task_link_defect_2189_test.go`, red first (7448cc39):
- the exact variables, the omitted head, no instruction and the session header;
- linked and already linked, in text and as exact JSON;
- the five refusals: exit code, code in the message, one call;
- a TASK_NOT_FOUND answer identical across three different refs;
- an empty, whitespace or missing `--defect` refused with zero calls.

`TestTaskWritesAuthored1585` gains `TaskLinkDefect`.
