# `hadron auth passkey list` (cli#878 part 1): design as built

**Scope:** list only. This is the CHECKED cut of hadron-cli#878 part 1 (gate [6831823066](https://github.com/hadron-memory/hadron-cli/issues/878#issuecomment-6831823066), author contract [6831767572](https://github.com/hadron-memory/hadron-cli/issues/878#issuecomment-6831767572), scoped FIT [6831698519](https://github.com/hadron-memory/hadron-cli/issues/878#issuecomment-6831698519)). Provider: hadron-server `Query.myPasskeys` (#1631 PR3), read at server `main` 2755d34ad. Specs: cor:aut:040:05 (the passkey record, and who sees it) and cor:aut:040:08 (only the owner revokes, interactively).

## Not in this cut
- **Revoke.** The server's revoke page takes no target. The CLI builds no URL, takes no assertion and has no destructive revoke. Its help says that removal happens on the server's interactive page, that the CLI cannot do it, and (since the revoke-page cut below) that `hadron auth passkey revoke-page` prints the page's address.
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
  - the discovery-failure refusal as one line keeping its tail, for a synthetic multi-line cause (CRLF and a lone CR), an over-long cause, and a connection-refused `*url.Error` behind a `--server` URL long enough to push the cause past the 1024-rune bound. The cut keeps the END of the cause, where the reason is (cli#1950);
  - a query-time 500 with an HTML body giving exit 7 with neither discovery sentence and no HTML (cli#1950);
  - both refusals, with their exit and `serverCode`;
  - ON with no credential giving exit 3 with zero POSTs;
  - the help naming no URL or portal page, and no `revoke` verb.

Seventeen mutants (gate dropped, discovery error let through, plain `MapError`, a broad skew match, a `credentialId` field, each state guard, a nil slice, nil transports, `serverCode` dropped, auth before the gate, re-ordering, a help URL, transports in the table, a `credentialId` selection) are each killed by a named test.

## Revoke-page handoff: `hadron auth passkey revoke-page [--open] [--json]` (design as built)

**Scope:** the revoke-page handoff only, `Refs #878` (the umbrella stays open). Gate [6835278968](https://github.com/hadron-memory/hadron-cli/issues/878#issuecomment-6835278968) (CHECKED, T2 + §10 + UX + compiled-CLI ON/OFF QA), author contract [6835152900](https://github.com/hadron-memory/hadron-cli/issues/878#issuecomment-6835152900), scoped FIT [6835218645](https://github.com/hadron-memory/hadron-cli/issues/878#issuecomment-6835218645) (COMPLIANT). Provider: `ServerInfo.passkeyRevokeUrl` (hadron-server#4063, merged `739ecd7d6`; the schema snapshot is refreshed from that SHA). Specs: cor:aut:040:08 (only the owner revokes, interactively) and cor:api:020:01 (the field is removed while registration is off, never null). Spec reach: none (client presentation of a ruled contract).

The command prints the address of the server's revoke page, or, on an explicit `--open` in a terminal, also hands it to the platform launcher. It never revokes and never builds the URL.

### Flow
1. **Flag checks, zero network.** `--open` with `--json`, and `--open` when stdout is not a terminal, exit 2 before any request (no `/capabilities` read either). The flag is never ignored silently.
2. **Discovery**, the same `PasskeyCapabilities` seam and sentences as `list`: no `passkey-registration` token, or a failed discovery (its cause cut by `oneLineTail`), exits 2 with zero GraphQL requests.
3. **`f.PublicGraphQLClient()`.** No credential is required: the server answers the field anonymously and the URL grants nothing. A signed-in user's credential still rides under the existing transport rules, unchanged (gate: preserve that helper's behaviour).
4. **One `PasskeyRevokeUrl` query**, selecting only `serverInfo { passkeyRevokeUrl }`. No mutation exists in the path.
5. **Errors.** An envelope whose every error is a validation error naming `passkeyRevokeUrl` exits 2 with "this server does not advertise its passkey revoke page (an older server, or passkeys were switched off since discovery); no URL was constructed": the two causes look the same, so the sentence names both. A mixed envelope, or a validation error on any other name, keeps `api.MapErrorKeepingCode` (skew advice, exit 7 for transport).
6. **Validation, construct nothing** (`validRevokeURL`). The value is used byte-for-byte if it is an absolute http(s) URL with a host, no userinfo and no opaque part (node open's rule), does not start with `-`, carries no control or format character (it is printed verbatim; `url.Parse` refuses ASCII controls only before the `#`, so an ESC in the fragment, a C1 control or a bidi override would otherwise pass), and passes the CLI's own transport rule (`api.URLIsSecure`, exporting `schemeIsSecure`: https, http to a loopback host, or `HADRON_ALLOW_HTTP=1`). Anything else exits 1, "the server returned an invalid passkey revoke URL", and nothing is opened. **No same-origin check against `--server`**: the value is the deployment's canonical `baseUrl`, which may differ from the host the CLI dials (§10 judges this boundary).
7. **Output.** Text: the URL alone on one line. `--json`: `{"passkeyRevokeUrl": "<url>"}` (an explicit DTO). With `--open`: one launcher call (`auth.OpenInBrowser`: argv, no shell) with exactly that URL, then the URL is printed; a launcher failure exits 1 with the URL in the message. A launcher that starts is not proof that the page loaded. **No auto-open**: unlike `node open`, a terminal without `--open` only prints.

Test seams: `auth.PasskeyOpenBrowser` (the launcher) and `auth.PasskeyStdoutIsTerminal` (the test streams can fake a terminal on input, not on output).

### Tests and mutants
`internal/cmd/auth_passkey_revoke_page_878_test.go`, against a fake server that counts capability reads and POSTs and records each operation, query and `Authorization` header, and a fake opener that records calls. Every server in the file fails the test if any operation other than `PasskeyRevokeUrl` is sent (contract pin 10).
- OFF shapes (switch off, a 404, an unparsable body, off with no credential), text and `--json`, on a terminal: exit 2, 0 POSTs, 0 opener calls.
- Signed out: exit 0, the fixture URL (host `accounts.hadron.example`, not `--server`) printed byte-identical, one POST whose whitespace-stripped query is exactly `query PasskeyRevokeUrl{serverInfo{passkeyRevokeUrl}}`, no `Authorization`. Signed in: the bearer still rides.
- `--json` exactly one key; no auto-open on a terminal; `--open` one call with the exact URL; launcher failure exit 1 with the URL.
- `--open --json` and `--open` off a terminal: exit 2 with 0 capability reads, 0 POSTs, 0 opener calls.
- The missing field: exit 2 with its sentence and no skew advice, even with `--open`; another name keeps the skew advice; a mixed envelope is not read as the missing field. A 500: exit 7, no HTML, nothing opened.
- Invalid values (`""`, `javascript:`, a relative path, userinfo, `-h`, insecure http, an opaque form, an empty host, `ftp://localhost`, a trailing U+0085, a U+202E, an ESC hyperlink sequence in the fragment, an empty hostname `https://:443/…`) under `--open`, `--json` and text: exit 1, nothing opened, the value not echoed. Loopback http and `HADRON_ALLOW_HTTP=1` pass.
- The production terminal seam asks stdout, not stdin (`TestTTY` streams are an input terminal only).
- The help's sentences, and no URL in it. The `list` help pin now also requires its revoke paragraph to name `revoke-page`.

Twenty mutants, one guard each. Every mutant's search text was asserted to match exactly once before it was applied, and the file was restored afterwards. `TestPasskey*` was run against each mutant.

| Mutant | Killed by |
|---|---|
| capability gate dropped | `RevokePageOffMakesNoGraphQLRequest` |
| discovery error let through | `RevokePageOffMakesNoGraphQLRequest` |
| auto-open on a terminal | `RevokePageNoAutoOpen`, `RevokePageJSON` |
| terminal check dropped | `RevokePageOpenMisuse` |
| `--json` check dropped | `RevokePageOpenMisuse` |
| terminal seam asks stdin (`IsInputTerminal`) | `StdoutIsTerminalReadsStdout` |
| URL built from `--server` + `/passkeys/revoke` | `RevokePageJSON`, `RevokePageInvalidURL`, `RevokePageLauncherFailure` |
| userinfo check dropped | `RevokePageInvalidURL` |
| host and hostname checks dropped | `RevokePageInvalidURL` |
| hostname check alone dropped (`https://:443/…`) | `RevokePageInvalidURL` |
| transport rule (`URLIsSecure`) dropped | `RevokePageInvalidURL` |
| control/format-character check dropped (U+0085, U+202E, ESC in the fragment) | `RevokePageInvalidURL` |
| validation mapping dropped (generic skew) | `RevokePageFieldMissing` |
| match broadened to `IsGraphQLValidationFor` | `RevokePageFieldMissing` (mixed envelope) |
| `GraphQLClient` instead of the public client | `RevokePageSignedOut` |
| launcher error swallowed | `RevokePageLauncherFailure` |
| opened before validation | `RevokePageInvalidURL`, `RevokePageOpen` |
| explicit http(s) allow-list dropped | survives, equivalent today (see below) |
| opaque check dropped | survives, equivalent (see below) |
| leading-`-` refusal dropped | survives, equivalent (see below) |

Three mutants survive because each guard is equivalent to another check, and each guard is kept on purpose:

- **The http(s) allow-list.** `URLIsSecure` allows only http and https today. That rule decides where a credential may go and could gain schemes later; the allow-list keeps this page a web page if it does.
- **The opaque check.** An opaque URL has no host, so `u.Host == ""` already refuses it. The check stays to keep node open's rule word for word.
- **The leading-`-` refusal.** No absolute http(s) URL can start with `-`. The refusal backs up `OpenInBrowser`'s own.

A fourth guard, `!u.IsAbs()`, was equivalent to the scheme allow-list and was removed.
