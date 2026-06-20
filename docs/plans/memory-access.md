# Implementation Plan: `hadron memory member` / `memory share` — access control

> **Status: implemented and verified** on branch `feat/memory-access` (not yet
> merged); this reflects the design as built. GH issue
> [#57](https://github.com/hadron-memory/hadron-cli/issues/57), Tier 1 of the
> CLI⟷portal parity epic [#67](https://github.com/hadron-memory/hadron-cli/issues/67).

## Context

Memory access control was portal-only — no way to grant another user access from
the CLI. This adds **members** (team rows on group-class memories) and **shares**
(per-user grants) as subcommands of the existing `memory` group, reusing its
`resolveMemoryID` (so every command takes a memory id *or* URN).

## Command surface (as built)

```
hadron memory member ls <memory>
hadron memory member add <memory>      --user <user-id> --role <owner|writer|reader>
hadron memory member set-role <memory> --user <user-id> --role <owner|writer|reader>
hadron memory member rm <memory>       --user <user-id> --yes

hadron memory share  ls <memory>
hadron memory share  create <memory>   --grantee <user-id> --role <writer|reader>
hadron memory share  set-role <memory> --grantee <user-id> --role <writer|reader>
hadron memory share  revoke <memory>   --grantee <user-id> --yes
```

- **Two role enums, lower-case.** `MemoryMemberRole` = `owner|writer|reader`;
  `MemoryShareRole` = `writer|reader` (no owner). Parsed case-insensitively;
  `parseShareRole` rejects `owner` — caught by a test. Bad value → usage error
  before any network call.
- **add / create are upserts** (the server's `addMemoryMember` /
  `createMemoryShare` semantics), so they double as "change role".
- **Listing** goes through `memory(id) { members | shares }` — members are
  non-empty only for group-class memories (per spec 023).
- `member rm` / `share revoke` are gated by `cmdutil.ConfirmDeletion`.

## Codec / API

`internal/api/queries/memory_access.graphql` over a `MemUserFields` fragment
(named to avoid colliding with #56's `UserFields` when both land). Operations:
MemoryMembers/MemoryShares; Add/Update/RemoveMemoryMember;
Create/Update/RevokeMemoryShare.

## Out of scope (follow-ups for #57)

- **Subscriptions** — `createMemorySubscription` / `delete` / `update` (org-level
  subscription to a memory).
- **`addToMyMemories`**, **`linkMemoryToUser`** (external-user linking).
- **`encryptMemory`** — memory encryption (needs a data-key flow).

## Tests / verification

`internal/cmd/memory_access_cmd_test.go` (fake GraphQL): member add (role
normalized to lower-case + forwarded), bad-role rejected, ls, set-role, rm
(`--yes` gating); share create, owner-role-rejected, ls, revoke. `make build` /
`go test ./...` / `golangci-lint run` / `make generate` (no drift) all green.
