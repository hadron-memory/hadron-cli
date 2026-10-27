# `hadron team session shed` (hadron-cli#952), as built

Design came from the issue's attach (Patrick) and the CHECKED comment (Xanthos, four builder conditions). This records what shipped.

## Contract
- Reads (each independent, a failure prints `unavailable: <reason>` for that field only): `TeamChatReadHead`, `TeamDefaultChannel` + `CheckChatCursor`, `TeamWorkItems`, `GetWorkerContinuity`; `GetWorker` supplies the worker memory the node is written to (a failure there stops the shed before any write).
- Node: `sheds:<YYYY-MM-DD-HHMM>` or, with `--auto`, `sheds:auto-<YYYYMMDD-HHMMSS>` (UTC), type `info`, headings Identity / Watermark / Open work / Briefing pointers / Recovery prompt (+ opt-in Transcript tail, last whole lines, 8 KiB max, not scrubbed).
- Step order: binding + server check, reads, render, cooldown, `CreateNode`, read-back by id (loc and content must match), cooldown stamp, `CreateResumeClaim` LAST. A failure before the claim exits 0 with no claim; a failed claim leaves the verified node (`claim: failed`).
- Cooldown: `<git-dir>/hadron-team-shed.json` `{sessionId,lastShedAt,node,claim}`, written after the verified node write. `--auto` inside `--cooldown` (10m) is `skipped`; with `claim: failed` it retries only the claim, with `claim: written` it does nothing. Manual ignores it.
- Exit codes: exit 0 on every failure after the flags parse. `DisableFlagParsing` plus a hand-run `ParseFlags` lets `--auto` swallow flag errors (cobra rejects them before `RunE` otherwise); manual mode keeps exit 2 for usage errors. An empty `--transcript` / negative `--cooldown` is refused in manual mode before any call and degrades under `--auto`.
- `--print-prompt`: no node, claim or stamp; `--offline` also makes no network call. The prompt is generated from `shedRecoverySteps`; `TestSessionShedPromptCitesOnlyRegisteredCommands` pins each cited command and flag against the cobra tree.
- No claim value is printed anywhere (the receipt carries only an expiry, which is also not printed).

## Out of scope (follow-ups)
Hook wiring, transcript snapshot, supervise check and kill stay host-side. The hadron-docs CLI-reference entry and updating the shed task node to call the command are separate.
