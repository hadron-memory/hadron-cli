# C7 inbox staging (cli#912)

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
