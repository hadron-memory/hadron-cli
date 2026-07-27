# `hadron user set-roles` — platform-role administration

Design-as-built for the `updateUserRoles` item descoped from #56 and tracked in
#75.

## Why

`updateUserRoles` was the last identity gap with no CLI surface — reachable only
through `hadron api`. It is the platform-role half of an admin's toolkit; the
org and memory halves (`org member set-role`, `memory member set-role`) already
shipped.

## Surface

```
hadron user set-roles <userRef> --role <role>... [--yes] [--json]
```

- `<userRef>` — id, email, bare or `@`-prefixed handle, or `hrn:user:<handle>`.
- `--role` — repeatable; `owner` | `admin` | `contributor` | `reader`,
  case-insensitive.
- Requires the platform `ADMIN` role (server-side `requireRole(ctx, 'ADMIN')`).

## Decisions

**Replace, not merge — and say so loudly.** The server assigns the list verbatim
(`db.user.update({ data: { roles } })`), so the command is a whole-set
replacement. That is the feature's one real footgun: omitting a role silently
removes it. Three things address it rather than one — the name (`set-roles`,
plural, not `add-role`), a confirmation that renders `before → after` and names
the roles being **removed**, and `previousRoles` in the `--json` output so a
scripted caller can see what it displaced without a second read.

**Gated like the other irreversible user command.** `user merge` already prompts
and requires `--yes` non-interactively; `set-roles` follows it. Dropping your own
`ADMIN` is a one-way door — only a platform admin can call this — so the prompt
says so explicitly when the removed set includes `ADMIN` or `OWNER`.

**Client-side ref resolution.** Unlike `mergeUsers`, `updateUserRoles` takes a
raw PK and does no `resolveUserRef` of its own. The CLI resolves the ref itself
so this command accepts the same forms as every other user-keyed command.

**`ResolveUser` added to cmdutil.** The first cut called `ResolveUserID` and then
re-read the user to get its current roles — but `ResolveUserID` *already* fetched
the full `UserFields` on both of its paths and discarded everything but the id.
`ResolveUser` returns what the resolution already read, so the before/after
display costs no extra round trip. `ResolveUserID` is now a thin wrapper over it,
unchanged for its existing callers.

Its `found` return distinguishes a real match from the documented pass-through
case, where an unmatched bare token is returned verbatim as a literal id. In that
case the prior roles are genuinely *unknown*, so they render as `[]` rather than
claiming the user had none — and the update still proceeds, because reading the
current roles feeds the confirmation message and is not an authorization gate.

**Client-side role validation.** A typo becomes a usage error naming the valid
set, instead of a GraphQL enum-coercion error from the wire. `--role ""` is
rejected rather than treated as "clear every role".

## Not done

`--add` / `--remove` convenience flags. They would need a read-modify-write over
a set the server replaces wholesale, which invites a lost update between the read
and the write. The explicit whole-set replacement is the honest model of the
mutation; revisit only if the server grows a delta-based surface.

## Contract

`--json` emits `{user, previousRoles, roles, changed}`. `previousRoles` and
`roles` are always arrays, never null. Exit codes: 2 for an unknown/empty role,
a missing `--role`, or a missing `--yes` non-interactively; the server's
`Forbidden` propagates through `api.MapError` for a non-admin caller.
