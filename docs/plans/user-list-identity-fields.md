# `hadron user list` — full user listing + identity fields for merge triage

Design-as-built for the two gaps that surfaced while consolidating duplicate
production accounts with `user merge`.

## Why

Duplicate accounts are created by ordinary behaviour: signing in once with
GitHub and once with Google/Apple/email mints two Users. Consolidating them
needs two things the CLI could not do.

**1. See the whole population.** The server's `users` query already lets a
platform `ADMIN`/`OWNER` omit `filter.query` for the full list
(`cor:acl:070:02`), but `SearchUsers` declared `$query: String!` and the command
rejected an empty one — so the only way to find duplicates was to guess
substrings and union the results. That finds the accounts you thought to search
for, which is the wrong set: an account with an auto-generated hex handle and no
GitHub username (exactly what an email signup produces) is invisible unless you
already know its address.

**2. See which credential each account authenticates with.** `UserFields`
selected `email` and `githubUsername` only. That is not enough to predict what a
merge does to a login, and `mergeUsers` is irreversible with no dry-run.

## Surface

```
hadron user list [query] [--limit N] [--offset N] [--json]   # aliases: search, ls
```

- Omitting the query requests the unfiltered listing. A caller without platform
  authority gets an **empty list, not an error** — the server will not confirm
  that accounts it won't show you exist, and the CLI must not turn that into a
  distinguishable signal. The human branch prints a hint so an empty table isn't
  read as "no users".
- Without an explicit `--limit`/`--offset` the listing pages to exhaustion via
  `api.CollectAll`, per the whole-corpus rule in CLAUDE.md. Either flag selects a
  single explicit page.
- `UserFields` gained `identityProvider`, `githubId`, `externalId`,
  `externalAppId`, `linkedAt`; `PROVIDER` joins the table.

## Decisions

**Nullable `$query` + omitempty, not an empty string.** A blank query and an
omitted one are different requests: blank is a filter that matches nothing,
omitted is "no filter". `# @genqlient(omitempty: true)` on a `String` drops the
variable, and GraphQL input-object coercion renders the absent variable as
`filter: {}` — the admin full-list path. Sending `""` would have quietly
returned an empty page and looked like a permissions problem. The command
still rejects an explicitly empty argument as usage, since `user list ""` is a
typo, not a request to enumerate the platform.

**The identity fields go on the shared `UserFields` fragment**, but that alone
surfaces nothing: every command marshals its own DTO so `--json` stays stable
across regenerations, so the fragment only makes the data *available*. `org
member list` has its own `userDTO` in `internal/cmd/org/common.go` and was
extended to match — a roster of one org's members is where two rows for the same
human are most visible, and that judgement is made wherever users are listed,
not only in `user list`. `access check` also selects `UserFields`, but only to
resolve an id; it emits no user object, so there is nothing to surface there.

**`identityProvider` is displayed but is not the answer.** It is tempting to
read it as the login method; it is not. Auth resolves a user by *provider id*
first (`googleId` / `githubId` / `appleId`), then by verified email —
`identityProvider` only records which provider created the row, and the linking
branch explicitly keeps the existing value. `mergeUsers` mirrors that: it adopts
each provider id when the target's is null (`target.googleId ?? source.googleId`),
so a merge usually *preserves* both sign-in paths. The field that actually gets
destroyed is `email`, which is target-wins and nulled on the source tombstone.
The column is worth showing because it explains an account's origin, but the
triage question is "is the target's corresponding id column empty?".

**No admin handle rename, so the target choice is final.** `profile set` is
self-only and `mergeUsers` keeps `target.handle ?? source.handle` — with handle
NOT NULL the target's always wins. Whichever account you merge *into* keeps its
handle forever, which is why the auto-generated hex handles have to be sources.
`agentic-usage.md` says this outright next to `user merge`; it is the kind of
thing you only discover after the irreversible step.

## Tests

`internal/cmd/user_cmd_test.go` — the query variable is omitted (not empty) for
the full list; identity fields round-trip through `--json`; the unbounded
listing drains multiple pages and stops at `total`; an explicit `--limit` makes
exactly one request; an empty full list explains itself.

`list_naming_test.go` gains the `user ls`/`user list` alias pair.

## Not done

`googleId` and `appleId` are columns on the server's User but are not exposed on
the GraphQL type, so a Google- or Apple-origin account's provider id is still
invisible to the CLI — it has to be inferred from `identityProvider`. Adding
them is a hadron-server change; worth doing before the next consolidation.
