# A context-cap reset names no task (hadron-cli#2117)

Companion to hadron-server#4258 (S4b, closes #4225). The server now issues a `worker.reset-context` for an Agent's context cap with **no `taskId`** (args `workerId`, `sessionId`, `resetId`); `ContextReset.taskId` is nullable and `ContextReset.cause` is `task-end` or `context-cap`.

## What changed
- **Handler** (`reset_handler.go`): `resetId` and `sessionId` stay required; `taskId` is optional (absent, null and "" all read as none). A cap reset runs the same claim, checkpoint, stop and relaunch path as a task-end reset. The reset driver stays off until C3's proof (#2088); this only stops a declared host from failing every cap reset with `HOST_UNSUPPORTED`.
- **Recovery** (`host_reset.go`): with no task, `registerContextResetRecovery` is sent **without** `taskId` (the server refuses a named task with `RECOVERY_REF_INVALID`). The generated input omits it (`omitempty`), because an omitted field is "none" and an explicit null is a different statement (the wire gotcha in CLAUDE.md).
- **Checkpoint** (`reset_forward.go`): a reset with a task prints `- finished task: <id>` exactly as before. With none it prints `- reset because: <cause> (this reset names no task)`, the cause read from the claim's `ContextReset.cause` (the server's own row, no extra call), else from the operation's args (hadron-server#4258 puts `cause` there too, a closed set: `context-cap` or `task-end`; Wolfgang #108845), else it says the server did not say why. If the two ever differ, the claim's wins. A task-end reset's args are unchanged and its checkpoint prints its task line exactly as before. It never prints an empty task line.
- **Schema**: re-pinned. `taskId` maps to "" when null. `cause` is in the `ContextResetFields` fragment.

## Schema pin
Pinned against hadron-server#4258's re-frozen head `91333e99e` (server main merged in) as a **draft pin** (the re-freeze, which put `cause` in the op args, changes no GraphQL type: the snapshot is identical to the pin at `cc136d0a`); re-pin at #4258's merge commit before merging this. The refresh also brought the already-merged #4247 `WorkerTerminalReportInput.repairReason`; the health-report mutation now omits it like `repair` (otherwise the exact-wire golden of #1181 sends `"repairReason": null`).

## Not here
Measuring the context (C8, merged), removing `worker-auto-shed`, and activating the reset driver (C3).
