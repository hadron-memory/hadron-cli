# App grant, identity and latest-run surfaces (#1338)

## Scope and authority

Six existing GraphQL operations become curated CLI commands. No schema, server,
MCP, portal, authorization rule or runtime execution behavior changes. Refs #1338;
the issue remains open for the coordinator's evidence close. The schedule is
T2 with independent LOCAL, context-free BLIND, CLI QA and security at every head
(Xabat's CHECKED comment 6540741209).

The committed schema already contains all six operations. Server authority was
checked at origin/main 0a6d80f5ac2bf22b01ec336e58e3fdce2744ee0a:

- `resolvers.appWriteGrant.ts`: person acting as themselves, managing the App;
  grant creation also requires target write access. Personal/private targets
  require their owner; shared targets belong to the App's org/team. Encrypted
  and system-class memories are barred. Prefix validation and live-grant reuse
  are server decisions. Revocation retains the row. Listings conceal another
  owner's personal/private grants and may return null memory metadata.
- `resolvers.mutation.asset.ts` and `setAgentAssetUploadEnabled.ts`: readable
  Agent, then org admin; user-owned Agents unsupported. The target is the
  Agent's own backing App, found by its URN and org, not any installation.
  `asset/gate.ts` identifies the tool as `asset:upload`. This source answers the
  gate's missing-SDL-description question; no guessed authorization is added.
- `myApp` identifies the authenticated App, not ambient context. A null App is
  valid. `latestAppRun` returns a concrete latest/previous run, not a synthetic ID.

Existing specs cor:agt:010:01 and cor:api:240:01 provide the userless-grant and
server-owned-rule context. This change implements existing rules and mints none.

## Commands and wire behavior

| Command | Operation | Result |
| --- | --- | --- |
| app memory-grant add APP --memory REF --prefix LOC | setAppMemoryWriteGrant | grant metadata |
| app memory-grant list APP [--include-revoked] | appMemoryWriteGrants | metadata array |
| app memory-grant rm ID | revokeAppMemoryWriteGrant | retained revoked metadata |
| run latest APP [--previous] | latestAppRun | existing full run-detail DTO |
| app whoami | myApp | `{app: metadata|null}` |
| app asset-upload enable/disable AGENT | setAgentAssetUploadEnabled | `{appId,appUrn,agentRef,enabled}` |

App/memory references use existing canonicalizers. Agent references go to the
server without a lookup. Every write confirms its explicit target and requires
`--yes` without a terminal. Reads and writes make one request; no command retry
or fallback changes scope. A lost mutation response is an unknown outcome;
help directs grant callers to list before retrying.

Optional booleans distinguish omission from explicit false through pointer
variables with genqlient omitempty. `--register-node` is omitted unless supplied;
it is a citation, never fetched. Empty required references/prefix and explicitly
empty register citations fail before any request. Disable sends enabled:false.
No session ID or ambient --app chooses the target of these commands.

## Output and failures

Explicit command DTOs isolate JSON from generated types. A grant has id, appId,
memoryUrn, prefix, registerNodeUrn, grantedBy, createdAt, revokedAt and revokedBy;
nullable values remain null. Lists preserve server order and produce [] when
empty. Text uses SafeCell for server values, ? for a concealed/null memory and - for
other nullable metadata. Local confirmation targets are quoted without truncation. No credential or node content
is selected. App identity selects only id, urn, name, appType and description.

Null/malformed grant mutation/list responses fail rather than invent success or
empty state. Revocation needs a returned revokedAt; asset changes need a returned
App identity and an allowlist matching the requested state. A null latest run
exits 4 without claiming that no hidden run exists. Null myApp is successful.

All server failures use MapErrorKeepingCode. Grant FORBIDDEN and general
AVAILABILITY_NOT_PERMITTED map to8, typed NOT_FOUND to4, BAD_USER_INPUT to2,
UNAUTHENTICATED to3. These are mapper controls, not a claim that every operation
emits every code. The asset-setting operation currently throws SetAssetEnabledError
but the server GraphQL formatter does not register that class: its non-admin,
unknown/unreadable Agent and missing backing-App refusals become
INTERNAL_SERVER_ERROR (exit1 with requestId). The CLI preserves the actual wire
answer; it does not infer access or existence from prose. No scoped
PERMISSION_DENIED override remains. [hadron-server#2979](https://github.com/hadron-memory/hadron-server/issues/2979)
owns registration of the class and its same-answer concealment rule; this PR
does not fix the server.
HTTP503 maps to availability7 with unknown-write-outcome guidance. No local role
checks reproduce policy.

## Verification and limits

The first test commit failed on main at runtime because the commands/operations
were absent. Wire tests check all six operations, canonical refs, optional values,
metadata/null handling and request counts. Confirmation/empty inputs make no
requests. Refusal tests preserve server codes and exits; HTTP503 cases send once.
Additional malformed-response cases prevent false success. The valid variableless
MyApp query required correcting a test helper that tried to decode empty variables;
that correction and an input-pointer compile fix are not red-first evidence.

Independent CLI QA must cover the gate's grant add/list/rm round-trip, revoked-row
visibility and availability refusals; author fake-server tests do not establish
real server access control. Security review remains required at every head.
No production grants, keys or upload settings were changed during author testing.

Every path sends zero requests before validation/confirmation or exactly one
operation afterward. Grant list is an unpaged server list of scalar metadata,
roughly 250–400 JSON bytes per typical grant (about 25–40 KB for 100 grants),
without memory/node bodies. This is a size estimate, not a measured server limit.
Identity and asset answers are a handful of scalar fields. Latest run reuses the
full run-get projection because that same detail is its output; response size
therefore depends on the existing run-detail fields. No list is fetched to resolve
one App or Agent.

Author mutation checks: removal of add confirmation, omission of explicitly false
include-revoked/previous, disabling the returned-setting consistency guard, changing
the admin-refusal mapping (historical pre-review probe, superseded by LOCAL evidence) and widening identity selection each compiled and
failed the intended new test. An equivalent string-comparison control survived.
Each edit was checked for its exact replacement before execution and restored.


LOCAL formatter finding: the initial source audit stopped at the operation's
custom error code and missed the GraphQL boundary. Real formatter execution by
Elena showed INTERNAL_SERVER_ERROR; the combined fix removes the unreachable
exit8 branch and documents that limit. Tests pin HTTP200/500 fault envelopes
through the production JSON renderer (exit1, serverCode, requestId, no retry).
Synthetic raw domain-code tests are explicitly mapper controls. The whoami help
also clarifies that user credentials return null. Independent earlier receipts
remain historical until their holders delta-review this fix head.
