# `hadron org github` — link, unlink, set-app (design as built, cli#2512)

Gate: Xabat's CHECKED on cli#2512 (comment 7001045747), T2 Standard + §10, with
Vera's spec FIT (AT0.8390.1, comment 7000849200). Server surface: all three
mutations were already on the committed schema snapshot (`linkGithubApp`,
`unlinkGithubApp`, `updateOrgGithubApp`, each `Organization!`); no server
change, no snapshot refresh needed. Specs cited: `cor:dmo:040:02` (the org
holds source-control installation credentials), `cor:int:010:01` (per-org,
encrypted, short-lived-token sync authorization).

## Surface

| Command | Operation |
|---|---|
| `org github link <org> --installation-id <n>` | `linkGithubApp(orgId, installationId)` |
| `org github unlink <org> [--yes]` | `unlinkGithubApp(orgId)` |
| `org github set-app <org> --app-id <id> [--private-key-file <path\|->]` | `updateOrgGithubApp(orgId, appId, privateKey)` |

DTO (all three, text and `--json`): `{id, urn, githubAppId, githubInstallationId,
hasGithubAppPrivateKey}` — nullable fields null when unset, never key material.

## Decisions made at build time

- **The org ref is passed as typed, never looked up.** The server resolves an id
  or URN itself (per each mutation's SDL description); a client-side lookup
  would add a request the acceptance forbids and make an unknown org
  distinguishable from a non-admin before the server's own answer. Same
  posture as `org signing` (signing.go).
- **The private key is write-only end to end** (`cmdutil.ReadSecretInput`):
  file, stdin or masked prompt; no inline flag exists. The resend-sender
  pattern is reused for the failure path (`githubAppWriteError`): with a key in
  play the raw error chain is never retained, and a server code that reflects
  the key drops the code rather than echo it. Reflected metadata is scrubbed
  (`redactGithubKey`) — the DTO's string fields replace the key with
  `[REDACTED]` if a server ever hands it back.
- **One mutation per verb, never retried.** A destructive unlink or a
  credential-bearing set-app must not be re-sent on a lost answer; a transport
  failure surfaces as the mapped error and the operator re-runs by hand.
- **`installationId` is `Int!`, validated locally** (`>= 1`, exit 2, no
  request); non-integers are refused by flag parsing before RunE.
- **`set-app` always sends both `appId` and `privateKey`** — the SDL takes them
  as a required pair, so there is no omit-vs-clear question and no `omitempty`
  annotations on the operation (unlike `updateOrganization`).
- **Unlink confirms** (`cmdutil.ConfirmDeletion`): TTY prompt,
  non-interactive requires `--yes` (exit 2, no request). It removes ALL GitHub
  App config — the help says exactly that, and deliberately says nothing about
  effects on already-synced memories (the corpus does not rule that; Vera's
  condition, and cor:int:010:01 only rules the *sync refusal* side).
- **Access is the server's fact.** Help quotes "org ADMIN or platform ADMIN"
  as the server's check on every call, not as a CLI or spec rule (FIT
  condition 4).

## Test pins

`internal/cmd/org_github_2512_test.go`, written red-first (all fail on main:
the commands are absent — gap #60):

- one matching mutation per verb, org ref as typed (id and URN case), no
  retry on a truncated-body transport failure;
- refusals before any request: unlink without `--yes` non-interactively,
  empty org/app-id, non-positive or non-integer installation id, explicit
  empty `--private-key-file=`, missing key on empty stdin;
- the TTY prompt can decline unlink (nothing sent) or consent (one mutation);
- FORBIDDEN → 8, NOT_FOUND → 4, UNAUTHENTICATED → 3, one call, no follow-up;
- sentinel key on stdin: exactly one occurrence in the one request payload,
  none in stdout/stderr/`--json`/error chain/envelope, `[REDACTED]` for
  reflected metadata (id, urn, githubAppId);
- no `--private-key` inline flag exists;
- `--json` DTO is exactly the five keys above.

`make coverage-matrix-check` passes with the three `gap #60` lines flipped to
`command org github link|unlink|set-app`; help inventory carries the four new
pages as `unread` (they enter the #2565 audit lane like every new page);
agentic-usage's org line and a detail line document the contract.
