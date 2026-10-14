# Passkey-signed node revisions in the CLI (cli#818)

Status: as built, first slice. Epic hadron-server#1614 (S3). Server contract:
hadron-server#1864 (issuecomment-6148958707), built by #1632 (S2b-1).

## What this slice ships

The CLI covers #1864's whole client surface:

| Command | Server call |
|---|---|
| `node sign <ref> [--revision N] [--signer <user-id>]` | `requestNodeSignature` |
| `node signatures <ref> [--check]` | `Node.signatureStatus` |
| `node signing-request get <id>` | `signingRequest` |
| `node signing-request list <ref> [--state s] [--limit N]` | `Node.signingRequests` |
| `node signing-request withdraw <id>` | `withdrawSigningRequest` |

## Decisions

- **The CLI never signs.** No command takes an assertion, a challenge
  response or key material, and none prints any (Xylon's gate: "the CLI never
  takes an assertion"). A passkey is shown by its label only
  (cor:dmo:130:06). A test pins the signature's JSON keys whole, so adding a
  field is a visible contract change.
- **Separate operations file.** `internal/api/queries/signing.graphql` holds
  every operation that names a signing field. While the server's signing
  switch is off (hadron-server#1895/#1902), its schema has none of them. A
  shared read that named one would fail outright, so no existing command
  selects one.
- **A server without signing says so.** A validation refusal naming a
  signing field maps to exit 2, "this server does not offer node signing",
  instead of the generic schema-skew advice to upgrade the CLI or server.
  That advice is wrong for a current server with the switch off. The message
  states only what the client observed, so it adds no tell beyond the
  server's own refusal.
- **Optional variables are omitted, not null.** `--revision`, `--signer`,
  `--state` and `--limit` carry `omitempty`; the server reads an omitted
  `signerRef` as "any writer".
- **Loud refusals.** `--state` accepts only the six enum values (any case),
  `--limit` only 1-100, `--revision` only positive, `--signer` only non-blank.
  Each refuses with exit 2 before any call.
- **Exit codes.** NODE_NOT_FOUND, SIGNER_NOT_FOUND and
  SIGNING_REQUEST_NOT_FOUND exit 4 (the existing `_NOT_FOUND` suffix rule).
  REVISION_MISMATCH and NODE_IS_PLACEHOLDER exit 5 (existing).
  ENCRYPTED_MEMORY_NOT_APPLICABLE and SIGNING_REQUEST_ENDED exit 5, because
  the state refuses. COMMENT_NOT_SIGNABLE exits 2: name a different node.
  SIGNING_REQUEST_NOT_REQUESTER exits 8: authenticated but not permitted.
- **`signatures --check`** exits 5 after printing unless the roll-up state is
  CURRENT and `integrityProblems` is 0, mirroring `node verify`.

## Not in this slice

- **Passkey (key) list/revoke**: no server surface exists yet (S2a).
- **The signing-page link** from `node sign`: the ceremony page and its URL
  are S2b-2. Until then, the request id is the handle.
- **Signature status inside `node get`**: with the switch off, that costs
  every `node get` a refused round trip. The proposal is to add it gated on
  hadron-server#1900's on-state capability once that lands (team chat #11118).
