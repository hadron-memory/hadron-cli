# S11 client wake and ticker refusals

Implements hadron-cli#2107 against hadron-server#4254's contract. The server
decides who wakes and whether a ticker subscription is allowed. The client
renders that answer without adding lifecycle checks or a wake selection.

- `CHILD_CANCELLED` appears beside `CHILD_FINISHED` in the existing
  `TASK <handle> <id> <event>` due-list format (Wolfgang, chat #108560).
  Delivery says `[hadron task] child cancelled: <title> (<handle>, <id>)`,
  using the existing handle/short-ID formatting. JSON keeps the raw event.
- Known events get their established human wording. Unknown events remain
  verbatim rather than being lowercased or interpreted. Terminal text keeps
  the existing control-character protection; JSON is unchanged.
- `TICKER_ON_SHORT_LIVED` maps centrally to state-conflict exit 5 with
  “a short-lived worker is woken by its tasks, not by tickers.” The original
  error chain retains the server code, also exposed as `serverCode` in the
  JSON error envelope. No retry or partial success output.
  `ticker unsubscribe` keeps its existing behavior.
- No GraphQL selection changes. The health-sweep input annotation omits
  the newly exported optional `repairReason` when unset, preserving its
  existing wire format. `DueWakes` and `TeamDueWakes` already
  select the event; a new enum member requires no server-new selection.
  Old-server responses and future enum strings use the same decoders.

## Provider pin

The schema and generated client are exported from the detached S11 merge
`2c5a65f8b3f4a44e2b640a66ca48426b958db134` of hadron-server#4254. This is the
merged provider contract required by #2107. The S11 delta adds
`CHILD_CANCELLED` and documents both child-end events. `WAITING_ON_CHILDREN`
was already absent from the CLI's base snapshot: this PR does not remove it,
and no current schema or client reference remains.

The full export also carries earlier merged provider changes: server#4247's
terminal-repair outcomes/reasons and input/output fields, and server#4248's
host-desired-state documentation. No new selection or command DTO is added.
The CLI does not yet send `repairReason`; that capability is tracked by #2060
and recorded in the coverage matrix. Its omission annotation preserves the
existing health report wire format. No command switches on or validates the
new repair enum values.

## Verification and scope

Red test commit `1c357833` fails on main for unknown-event rewriting and the
unmapped lifecycle refusal. Its original colon-label expectation was replaced
by the coordinator's established-format ruling (#108560); that label is now a
compatibility control. Fake-server tests cover cancellation, finished children,
unknown enums, ticker refusal in text/JSON modes, no retry/partial output,
allowed unsubscribe, and typed refusal mapping in HTTP 200/500 envelopes.

No new product rule or spec citation: these are client renderings/mappings of
S11's existing server contract. No activation or live worker/ticker mutation.
