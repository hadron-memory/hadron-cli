# C7 inbox (cli#912)

The opening section records the original staging boundary; production
integration and remaining acceptance are recorded below.

This branch stages the output/acknowledgment lifecycle behind `inboxTransport`.
It does not register a command, generate GraphQL operations, call MCP, or
activate the self arm. Integration waits for landed S1/R57 contracts.

`Read` and `Ack` receive the same complete App/worker/session/server identity.
The future generated adapter must use api.WithSession for both calls; no
GraphQL sessionRef argument, host-key fallback, through selector or read-mark
side effect is implied. A binding change before output refuses display; a
change after output refuses acknowledgment. Server authorization remains
mandatory even after local binding checks.

Human output preserves server text with a framing newline. JSON is a stable
page DTO with non-null arrays. Complete write and explicit flush precede ack;
write failure, cancellation, invalid recipient and rebinding cannot ack.
After output, an ack failure preserves that single output document, returns
the mapped exit status silently and emits sanitized recovery text on stderr.
Ack-only recovery prints counts without rereading or redisplaying the batch.

The input decoder accepts one object containing batchIds (1–200 nonempty
opaque proofs), bounded to 1 MiB. An unregistered constructor supplies --ack-file
path/stdin handling, terminal-stdin refusal and help; production binding and
registration remain held. No proof goes on argv,
into diagnostics or an automatically created file.

Tests use a fake typed transport and exercise output/flush failures, refusal
mapping, binding changes, foreign recipients, cancellation and recovery input.
They are not compiled-CLI/live-server/MCP QA. Those independent seats, UX,
security review, generated adapter and end-to-end contract/refusal checks
remain outstanding before readiness under Xylon's CHECKED T2+§10+UX gate
6257700396. This partial branch does not close #912.

## Integration against C1 main c418f653

Registered `team chat inbox` with a generated `SessionInbox` own-worker page
(first=200, optional --after) against the committed server-main ad7f1c2 schema.
Both read and ack use the ordinary credential plus api.WithSession for exactly
the locally bound session; no host key or fallback is involved. Explicit App
aliases resolve to the binding's canonical id; mismatched deployments or Apps
refuse. Binding identity is reread before display and ack. The server remains
the live-session/authority/refusal gate. Full acknowledgments reuse C1's
AcknowledgeDeliveryBatches operation with through omitted and require counts
that sum to the supplied proof count. C1 hostServer remains session-free.

JSON output contains the server-issued ids for caller-owned private ack-file
recovery; human callers retaining no ids may repull and redisplay before ack.
No unconditional exactly-once claim is made. MCP uses the existing server tools
hadron_team_chat_inbox and hadron_team_chat_inbox_ack as two explicit calls;
CLI-only integration does not prove live cross-transport QA. Required review
and independent CLI/MCP QA remain Ada's seats. Real-worker activation is HARD
BLOCKED by server#2121, and no release, enablement or cutover is authorized.

## Combined review fix (after all c1fc6d31 seats posted)

Known server/local reasons now use a fixed membership vocabulary and fixed
messages; unknown codes/messages never become diagnostic text. A definitive
refusal is labelled refused, with its own next step, rather than implying a
lost response. Unknown/transport ack failures alone use unconfirmed recovery:
human mode repulls with an explicit redisplay warning, JSON mode names
batches[].id -> batchIds and --ack-file -. Signed proofs stay out of human
stdout and all stderr. Empty human pages say so; a next-page hint makes no
claim the entire inbox is empty. Ack replay counts say already acknowledged.
Duplicate recovery ids refuse before a request. Binding guards have explicit
loopback tests and both removal mutants fail. The published gate's old MCP
names are corrected here to shipped hadron_team_chat_inbox and
hadron_team_chat_inbox_ack (server tickerTools.ts / Celine real-stack receipt).
This is a naming correction, not a new MCP/server operation.

Nonblocking --ack-file permission/path-detail suggestions remain follow-ups;
this command reads caller-owned input and never writes a proof journal.
Independent fix-head deltas/carries and final gate are still required.
