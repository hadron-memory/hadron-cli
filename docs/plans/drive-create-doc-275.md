# Drive document creation (cli#275)

The existing `drive list`, `get` and `export` commands read a connected Drive.
`drive create-doc <nodeRef> --connection <id>` reaches the separate,
owner-only `exportNodeToGoogleDoc` mutation. It always creates a new Google Doc;
it does not edit or replace an existing one.

The cleared remote-create cut is T2 with LOCAL, BLIND, security and fake-HTTP
QA seats (issue gate 7048732342). Refs #275; the change manager owns issue
completion after merge. This command does not change Drive read behavior.

## Request and consent

The CLI forwards an ID or qualified node reference directly, without resolving
it or fetching its content. The server reads the node, resolves includes with
the caller's access, authorizes the connection and passes the content to the
Drive tool. No permission preflight, discovery or alternate export path runs.

The confirmation names the node and connection and states that this copies
content to an external account, where Hadron cannot recall it. Given folder
and title values also appear in the question. Noninteractive calls need
`--yes`; declining sends nothing. The generated mutation is invoked once.

Unset `--folder` and `--title` use nil pointers with genqlient omitempty,
preserving server defaults (Drive root and node name). Explicit empty or
whitespace-only values refuse locally; other values are forwarded unchanged.

## Output and errors

Success reuses `drive get`'s explicit file DTO and text metadata renderer.
The `size` pointer preserves null; no node content or provider credential is
read or printed by the CLI. A missing/incomplete result cannot establish which
Doc was created and reports unknown outcome, without producing a success DTO.

Server refusals use the shared error mapper and retain the server code.
Transport/gateway failures and undecodable responses receive fixed exit-7
guidance: the outcome is unknown; check the drive before retrying. The message
does not claim a failed creation or encourage an automatic retry. There is no
retry or fallback mutation. A structured unknown server refusal keeps the
existing unknown-code fallback.

## Existing cross-repository rules

Both FITs found this compliant without a new spec. cor:dmo:080:04 makes an
operation with no grant mapping owner-only; App/headless, impersonated and
non-owner callers are refused by the server. cor:dmo:150:03 governs resolved
includes under the exporter's access. cor:api:240:01/:02 and cor:int:030:01
keep authorization, provider dispatch and node content resolution server-side.
The global `--unlock` supplies invocation-scoped keys for encrypted nodes;
`SESSION_EXPIRED` remains a server refusal. Consent, formatting and ambiguous
outcome guidance are CLI choices. No new corpus citation is proposed.

The existing schema snapshot already declares all inputs and the DriveFile
result, so no cross-repo schema refresh or server change is needed.

## Evidence and limits

The first commit contains command-level fake-HTTP pins that fail on main
because `create-doc` and its connection flag are absent. Tests count every
HTTP request, inspect the GraphQL operation and exact variables, cover both
consent branches and omitted inputs, preserve refusal mappings/null size, and
verify one attempt for gateway, dropped, malformed and null responses. Existing
Drive read/export pins remain in the focused suite.

No real Google account, Drive provider, production server or external write is
used during author tests. Independent named reviewers and QA own their seats.
