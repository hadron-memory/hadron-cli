# Implementation Plan: `hadron org` — organizations & membership

> **Status: implemented and verified** on branch `feat/org-commands` (not yet
> merged); this reflects the design as built. GH issue
> [#56](https://github.com/hadron-memory/hadron-cli/issues/56), Tier 1 of the
> CLI⟷portal parity epic [#67](https://github.com/hadron-memory/hadron-cli/issues/67).

## Context

Org/tenant setup and team membership were portal-only. This adds the `org`
group over the server's org mutations + the `organization(id)` query (the only
path to an org's members — there is no plural list query).

## Command surface (as built)

```
hadron org create --name <name> --urn <urn>
hadron org get <id>
hadron org update <id> [--name] [--urn] [--visible]
hadron org rm <id> --yes
hadron org member ls <org-id>
hadron org member add <org-id> --user <user-id> --role <role>
hadron org member set-role <org-id> --user <user-id> --role <role>
hadron org member rm <org-id> --user <user-id> --yes
```

- **Roles** — the shared `Role` enum: `OWNER | ADMIN | CONTRIBUTOR | READER`,
  parsed case-insensitively by `parseRole` (bad value → usage error before any
  network call).
- **update** is `Changed`-gated (only the fields you pass change); `--visible`
  toggles `isVisible` (`--visible=false` to hide).
- **member ls** reads `organization(id) { members { role, user{…} } }` — there's
  no `orgMembers` query, so listing goes through the org resolver.
- `org rm` / `member rm` are gated by `cmdutil.ConfirmDeletion` (prompt on a
  TTY, `--yes` non-interactive).

## Codec / API

`internal/api/queries/org.graphql` over shared `OrgFields` + `UserFields`
fragments. Operations: Get/Create/Update/DeleteOrganization, OrgMembers,
Add/Update/RemoveOrgMember.

## Out of scope (follow-ups for #56)

Deliberately deferred to keep this PR focused on the org core:

- **`user search`** (`searchUsers`) — to find the user IDs `member add` needs.
  For now they come from `org member ls` or `auth whoami`. *(Highest-value
  follow-up — pairs with member add.)*
- **`profile set`** (`updateMyProfile`).
- **Invitations** — `inviteUser` / `createUserInvitation` / `acceptInvitation` /
  `invitation` (the invite flow for users not yet in the system).
- **`updateUserRoles`** — platform-admin only; reach it via `hadron api`.

## Tests / verification

`internal/cmd/org_cmd_test.go` (fake GraphQL): org create/get(not-found)/update
(preserve-unset)/rm(`--yes`); member ls, add (role normalized + forwarded), bad
role rejected, set-role, rm(`--yes`). `make build` / `go test ./...` /
`golangci-lint run` / `make generate` (no drift) all green.
