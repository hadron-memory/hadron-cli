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

**Exact-match resolution.** `ResolveUser` falls back to a sole *substring* hit,
which is a convenience for additive commands (`memory share --grantee alic`
finding alice). For a destructive global write it is a hazard: a typo would
silently retarget another account, and `--yes` skips the prompt that would have
caught it. `cmdutil.ResolveUserExactly` wraps `ResolveUser` and refuses a
partial match, naming what it matched so the operator can retype. The
pass-through of an unmatched bare token as a literal id is preserved.

**Confirm against the resolved account.** The prompt names the resolved user
(`usr1 (@alice, alice@acme.com)`), never the typed ref — showing the input back
would confirm nothing about who is actually being modified.

**`changed` is a set comparison, and nullable.** Role order carries no meaning
server-side, so `[ADMIN, CONTRIBUTOR]` and `[CONTRIBUTOR, ADMIN]` compare equal.
When the prior roles could not be read, `changed` is `null` rather than `false`:
false would assert nothing changed when the truth is that it can't be known.
The prompt and human output say "unknown" for the same reason — rendering
unknown prior roles as "none" would tell an administrator the account is
unprivileged when it may hold ADMIN or OWNER.

**The lockout warning tracks the RESULT, not the removal.** Only a platform
admin can run this command, and the server's `requireRole` compares the caller's
*highest* role against ADMIN on `ROLE_ORDER` (READER 0 < CONTRIBUTOR 1 <
ADMIN 2 < OWNER 3) — so OWNER qualifies too. The warning fires only when the
resulting set grants neither: replacing `[OWNER, ADMIN]` with `[ADMIN]` removes
a role but is still undoable by that user, so it stays quiet.

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
`roles` are always arrays, never null. `changed` is `null` when the prior roles
could not be read; otherwise it is a set-membership comparison. Exit codes: 2 for an unknown/empty role,
a missing `--role`, or a missing `--yes` non-interactively; the server's
`Forbidden` propagates through `api.MapError` for a non-admin caller.
