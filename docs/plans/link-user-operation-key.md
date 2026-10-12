# link-user key supply through the operation header (#846)

Design-as-built for the first client slice of hadron-server#1796 — moving
`memory link-user`'s data key out of the GraphQL `dataKey` argument and into
the per-request `x-hadron-operation-key` header that hadron-server#1817
(transitional, on server main as `1cedfa2e`) introduced.

## The law and the deadline

[cor:sec:040:04](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:sec:040:04)
— "a key is never a field on a query or a mutation." A key in GraphQL
variables travels in the request body, where body logging, APM capture or a
proxy log holds key material. #1817 deprecated the argument while keeping it
accepted; the later **removal slice** deletes it, and that breaks **every**
document still naming `dataKey` — even to omit it, even for a plaintext
promotion, because the argument appears in the document text regardless of
the value. The removal slice is gated on this CLI change being released (and
on server#1814's caller inventory), so this migration must land AND ship
against the transitional server first.

## What changed

- **`internal/api/queries/memories.graphql`** — `LinkMemoryToUser` no longer
  declares or passes `$dataKey`. `encryptMemory` still does, deliberately:
  its replacement is the later #1259-dependent slice (server#1796 scope).
- **`internal/api/operationkey.go`** (new) — `OperationKeyHeader`
  (`X-Hadron-Operation-Key`), `WithOperationKey` (per-call context value,
  the `WithSession` pattern), and `ValidateOperationKey`. Validation mirrors
  the server's `requestOperationKey.ts` parser exactly: 43 base64 chars plus
  one `=`, decoding to exactly 32 bytes, re-encoding to the same string (the
  round-trip rejects non-canonical spellings whose final character's low
  padding bits a lenient decoder silently drops). Refusals are `Usage` exit
  2, before any request, and never echo the value.
- **`internal/api/client.go`** — `bearerDoer` sets the header only when the
  call's context carries a key; `checkInitialSessionRequest` and
  `checkSessionRedirect` treat a key-bearing request like a session-bearing
  one (no cleartext initial URL, no cross-host or insecure redirect hop);
  `classifyGatewayResponse` redacts the key from a gateway error's bounded
  body snippet.
- **`internal/cmd/memory/linkuser.go`** — validates the key after
  `readDataKey` (stdin `-` and explicit-empty refusal unchanged) and before
  the `GetMemory` pre-read, then wraps only the promotion call's context in
  `WithOperationKey`.
- **`internal/api/errors.go`** — `BAD_UNLOCK_INPUT` and `WRONG_KEY` map to
  `Usage` (2): the caller fixes the value, the same shape as
  `BAD_USER_INPUT`; neither server message ever echoes the key.

## Why the redirect work is load-bearing (§10 pre-read, chat #9428)

Go's `net/http` strips `Authorization` and cookies on a cross-host redirect
but **forwards custom headers**. Without the `checkSessionRedirect`
extension, a 307/308 from a misconfigured or hostile endpoint would hand the
key to whatever host it names — on both of `NewClient`'s policies (the
token-bearing one allows cross-host hops precisely because it trusts Go to
drop `Authorization`, which does not apply here). Key-bearing requests now
refuse cross-host and insecure hops, on the initial URL too, including the
no-token client path. The cleartext refusal shares the token rules'
carve-outs (`schemeIsSecure`): https, an http **loopback** host, or
`HADRON_ALLOW_HTTP=1` for a trusted self-hosted server.

## Cycle 1 (review findings on the frozen head)

Cycle 0 (#9452/#9457/#9458/#9466) found two defects and doc gaps, all fixed
in one head:

- **The silent downgrade** (Xavier P1 = Ralion's §10 P2): a pre-#1817 server
  or a header-stripping proxy ignores the header, the document legitimately
  omits `dataKey`, and the irreversible promotion completed UNENCRYPTED with
  exit 0. `linkuser.go` now fails loudly when a key was supplied and the
  response reports `isEncrypted: false` — a guard that cannot false-alarm on
  a #1817+ server, where a key-supplied promotion either lands encrypted or
  the request itself errors. Ralion's recommended preflight introspection of
  `dataKey`'s deprecation was deliberately NOT taken: the response guard
  covers stripped headers, old servers and future channel defects alike,
  while a preflight costs a round trip and reads a schema detail the guard
  does not depend on.
- **Redaction at every status** (Diego P2 = Ralion's amended P2 = Xavier
  P2-1): genqlient embeds the whole raw body of a non-200 non-envelope
  response in its printed error, and a 200 errors envelope renders through
  MapError's message join — so the 5xx-only gateway snippet scrub was not
  enough. The doer now reads and redacts every response to a key-bearing
  request (status, envelope and codes preserved), including URL-encoded
  spellings of the key in both hex cases.
- Doc accuracy (Xavier P2-2): the operation comment no longer claims
  `WithOperationKey` validates — `ValidateOperationKey` is the refusing
  function, called at the command layer before any request.

## Contract notes

- The key shape is stricter than the legacy argument's: the server's header
  parser demands the canonical spelling, and the legacy `parseDataKey`
  already required exactly 32 bytes, so client-side validation breaks no
  invocation the real server ever accepted (the fake-server fixtures that
  used arbitrary strings were never observable against production).
- Dual-channel agreement (Eden #9314) cannot arise from this client: the
  document no longer names the argument, so only the header can carry a key.
- The header is single-consumption server-side; the CLI sends exactly one
  key-consuming field per request, so the spent-header refusal is a
  server-side invariant, not a client state.
- Release sequencing: this must be **released** (tag-driven, goreleaser)
  against the transitional server before the #1796 removal slice merges;
  the removal is additionally gated on server#1814's inventory.

## Tests

Command-level (fake server, ordered capture of headers + raw bodies):
key only in the promotion's header and nowhere in any body or document;
plaintext promotion names no key anywhere; malformed keys (charset, length,
padding, non-canonical, url-safe) refused locally before any request without
echoing the value; a cross-host 307 is refused before the second host sees
header or body key material. API-level: per-call-only header, redirect
policy table under both client policies (header- and context-carried), the
cleartext initial-URL refusal, gateway-snippet redaction, `ValidateOperationKey`
table, and the two new exit-code mappings.

Red-first: the wire-contract tests landed in commit `f49ecd9` against
unmodified main and failed exactly as the issue predicts (the document named
`dataKey`, the key rode the variables, malformed keys were accepted and sent,
the cross-host redirect replayed them).
