# cli#2788: org subscribe/revoke and the paged Agent subscription reads

Design-as-built. Refs #1334, #890, #58, #67; leaves #2788 open (personal revoke is #2789).

## Surface (all under `hadron agent subscription`)

| command | operation | notes |
| --- | --- | --- |
| `add --org --agent` | `createAgentOrgGrant` | one write, never replayed |
| `revoke\|unsubscribe --org --agent [--yes]` | `revokeAgentOrgGrant` | confirms on a TTY, `--yes` otherwise |
| `list --agent <ref> [--limit --offset \| --all]` | `agentSubscriptionsPage` | Agent ADMIN (server's gate) |
| `list --mine [--limit --offset \| --all]` | `myAgentSubscriptionsPage` | authenticated user |
| `list --org` | `OrgAgentGrants` (unchanged) | bare array, unchanged |

Exactly one of `--org|--agent|--mine`. `--limit/--offset/--all` apply to the paged two;
`--all` conflicts with `--limit/--offset`. Usage errors send no request.

## Contracts

- **Writes.** Only a coded server refusal (`api.IsDefiniteRefusal`) says nothing changed and
  keeps its normal exit. A lost answer, `INTERNAL_SERVER_ERROR` or a null payload is exit 7
  "outcome unknown" with a check-first `subscription list --org`; nothing is retried.
- **Pages.** One page by default; the table says "NOT the whole set" and `--json`
  `{items,total,hasMore,complete}` carries `complete:false`. `--all` follows pages to
  exhaustion; an empty page that still claims `hasMore` is exit 1, not a complete list. An
  unset `--limit/--offset` is omitted (omitempty), never `null`.
- **No inference from empty.** `--mine` empty says it does not prove there are none (a run or
  impersonated caller gets an empty page); the server stays authoritative on every gate.
- **Schema snapshot.** The four operations were already in the committed snapshot, so it is
  not refreshed: a refresh at server main fails codegen on an unrelated `Question.context`
  removal (server #4426, `decision.graphql`), raised separately.

## Tests

`internal/cmd/agent_org_grants_2788_test.go` (fake transport): exact refs and one write;
`--yes` gate; unknown outcome x6 with no replay; coded refusal not unknown; selector and flag
rules with no request; page/all semantics for both reads incl. the stuck-server case; mine
caveat; `--org` shape unchanged. Mutants killed: refusal inversion, `Complete: true`, the
confirmation, the stuck-server guard.
