# `hadron auth passkey list` (cli#878 part 1): design as built

**Scope:** list only. This is the CHECKED cut of hadron-cli#878 part 1 (gate [6831823066](https://github.com/hadron-memory/hadron-cli/issues/878#issuecomment-6831823066), author contract [6831767572](https://github.com/hadron-memory/hadron-cli/issues/878#issuecomment-6831767572), scoped FIT [6831698519](https://github.com/hadron-memory/hadron-cli/issues/878#issuecomment-6831698519)). Provider: hadron-server `Query.myPasskeys` (#1631 PR3), read at server `main` 2755d34ad. Specs: cor:aut:040:05 (the passkey record, and who sees it) and cor:aut:040:08 (only the owner revokes, interactively).

## Not in this cut
- **Revoke.** The server's revoke page (`GET /passkeys/revoke`) takes no target, and no surface advertises its URL. The CLI builds no URL, takes no assertion and has no destructive revoke. Its help says only that removal happens on the server's interactive page and that the CLI cannot do it. The URL-advertisement companion is hadron-server#4051. A revoke-link command is a later slice with its own gate.
- **Part 2** (the `node sign` ceremony link) is still held on S2b-2.
- **Part 3** shipped in cli#891.

## Flow
1. **`GET /capabilities`** (`api.Capabilities`: unauthenticated, cached, every failure means "none"). Without `passkey-registration`, the command exits 2 with "this server does not offer passkeys". If discovery itself fails (unreachable, a non-200 response such as an older server's 404, or a bad body), it exits 2 with "could not confirm that this server offers passkeys (<why>); no query was sent". That is unknown, not no: same exit and the same zero queries, as the gate ruled, but the wording does not claim the feature is absent. **Zero GraphQL requests** are made, and this happens before the credential is looked at. The switch hides `myPasskeys` and its types from the schema, so asking would only fail validation.
2. **The credential check**, via `f.GraphQLClient()`. With no credential, it exits 3 and sends nothing.
3. **One `MyPasskeys` query.** It takes no arguments and does no paging. It selects every `PasskeyCredential` field the SDL has, which includes no key material.
4. **Errors:**
   - An envelope whose only errors are validation errors on `myPasskeys` means the switch went off after discovery. It exits 2 with the same sentence.
   - Everything else goes through `api.MapErrorKeepingCode`: an App key gets `UNAUTHENTICATED` (exit 3), and impersonation gets `FORBIDDEN` (exit 8). Both rows already existed.
   - The type names are deliberately not in that match. A validation error on one of their *fields* is real schema skew, and its message names the type (a mutant on this was killed).

## Output
- **`--json`:** a top-level array, `[]` when there are none, in the server's order (newest first). Each element has:
  - `id`: the record id, not the credential id;
  - `label` and the derived `state`;
  - `registeredAt`, `lastUsedAt` and `revokedAt`;
  - `window {endsAt, endedAt, outcome, registrationMethod}` or `null`;
  - `cancel {at}` or `null`;
  - `transports` (always an array), `backupEligible` and `backedUp`.
- **Table:** `LABEL STATE REGISTERED LAST USED WINDOW ENDS ID`. Transports and the backup flags are in `--json` only.

## State, a client-clock presentation
There is no platform spec for this derivation. It is a client-clock display, not authority, and it grants nothing. Its open-window rule is the server's own test (`lib/signing/state.ts:45`: `windowOpen = windowEndedAt === null && now < windowEndsAt`, at 2755d34ad).

`revoked` if `revokedAt` is set. That wins over any window, a cancel included. Otherwise `cooling-off` if the window has not ended (`endedAt` null) and `endsAt` is after now. An unparsable `endsAt` also reads `cooling-off`, because claiming the window is over would be the stronger, unproved statement. Otherwise `active`, which includes a window that ran out with `outcome` still null. No window length is assumed, because it is a server setting. `WINDOW ENDS` is printed so the reader can check the state against the server's clock. The state grants nothing.

## Tests and mutants
- **`internal/cmd/auth/passkey_test.go`:**
  - the state table at an injected clock;
  - the `transports` nil guard. This is DTO robustness only: the SDL is non-null, so no fixture sends null;
  - the compiled operation, parsed with gqlparser, selects none of `credentialId`, `publicKey`, `ip`, `ipAddress` or `userAgent`.
- **`internal/cmd/auth_passkey_878_test.go`**, all with legal SDL fixtures:
  - five OFF shapes, each giving exit 2 with zero POSTs (switch off, all off, a 404, an unparsable body, off with no credential);
  - the schema dropping the field after discovery;
  - a validation error on another name keeping the skew mapping;
  - the JSON keys, order and state, and a walk of every `--json` key against `auth.PasskeyKeyMaterialNames`, the one list the operation-selection unit test also reads (cli#1950);
  - the table, the windowless WINDOW ENDS cell asserted exactly `-`, and empty output;
  - a label carrying ESC and LF rendered as one safe row, with `--json` keeping the original (cli#1950);
  - the discovery-failure refusal as one line keeping its tail, for a synthetic multi-line cause, an over-long cause and a real connection-refused error behind a long `--server` URL (cli#1950);
  - a query-time 500 with an HTML body giving exit 7 with neither discovery sentence and no HTML (cli#1950);
  - both refusals, with their exit and `serverCode`;
  - ON with no credential giving exit 3 with zero POSTs;
  - the help naming no URL or portal page, and no `revoke` verb.

Seventeen mutants (gate dropped, discovery error let through, plain `MapError`, a broad skew match, a `credentialId` field, each state guard, a nil slice, nil transports, `serverCode` dropped, auth before the gate, re-ordering, a help URL, transports in the table, a `credentialId` selection) are each killed by a named test.
