# `hadron memory link-user` — anonymous → registered promotion

Design-as-built for the `linkMemoryToUser` item descoped from #57 and tracked
in #75. With this, #75's remaining scope is closed.

## Why

`linkMemoryToUser` is the mutation an App calls when an anonymous end user signs
up: the session memory they have been accumulating gets attached to a real user
instead of being expired. It had no CLI surface, so operating or debugging that
flow by hand meant hand-writing GraphQL through `hadron api`.

## Surface

```
hadron memory link-user <memoryRef> --external-user <id> [--data-key -] [--yes] [--json]
```

## Decisions

**App key only, said up front.** The resolver's first line is
`if (!ctx.appId) throw new Error('Forbidden: App Key required')` — a personal
user token cannot run this at all. That is unusual for a CLI command, so the
help leads with it rather than leaving the caller to decode a `Forbidden`. The
server's message propagates verbatim, and a test pins that.

**Report the re-mint.** The promotion mints a *fresh* URN
(`mintMemoryUrnV2`, the #697 Stage 3 flat single-atom form) and the anonymous
URN stops resolving. A caller holding the old URN would otherwise be left with a
dead reference and no clue why. So the command reads the memory first and
reports `previousUrn → urn` in both output modes, saying plainly that the old
one no longer resolves.

That pre-read is **best-effort**: an App key that can run the mutation need not
satisfy `memory(ref:)`, so a miss costs `previousUrn` and nothing else. Failing
the command there would block a promotion the mutation itself would have
allowed. The human output falls back to naming the new URN and noting that any
previous one is dead.

**Pass the ref through, don't pre-resolve.** `linkMemoryToUser` resolves an id
or a URN itself (`resolveMemoryRef`), so the command canonicalizes and hands it
over — the same reasoning as `memory share rm`. Pre-resolving would impose a
second authorization surface the mutation does not require.

**`--data-key` reuses the `memory encrypt` shape.** Same flag name, same `-`
means stdin, same one-way warning, and the confirmation happens BEFORE the key
is read because `--data-key -` consumes the stdin the prompt is answered on.
`dataKey` carries `omitempty`, so omitting it sends no field rather than an
explicit null.

**Gated.** The promotion is irreversible — URN re-minted, TTL cleared, possibly
encrypted — so it prompts on a terminal and requires `--yes` non-interactively,
like the other destructive memory commands. The prompt names the encryption
consequence only when `--data-key` was passed.

## Not done

No `--dry-run`. The server offers no preview for this mutation, and a
client-side simulation would have to guess the minted URN — the one thing a
caller most needs to be accurate. The confirmation is the safety boundary.

## Contract

`--json` emits `{memory, previousUrn, encrypted}`. `previousUrn` is `""` when
the pre-read missed. Exit codes: 2 for a missing `--external-user`, an empty
ref, or a missing `--yes` non-interactively; the server's `Forbidden: App Key
required` propagates through `api.MapError`.
