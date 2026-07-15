# Implementation Plan: client-side URN-slug validation

> **Status: implemented and verified** on this branch; reflects the design as
> built. GH issue
> [#189](https://github.com/hadron-memory/hadron-cli/issues/189).

## Context

An agent created `hrn:agent:holger-pers:Flow Lab` via the CLI — a URN with a
space (and uppercase). The CLI passed the user's input straight to the server
with no format check, so malformed slugs only failed (if at all) after a
round-trip, with a server-worded error.

The server's authoritative create-time grammar is `validateAtomShape`
(hadron-server `src/lib/urn.ts`, spec 021 FR-016/017): a slug atom is **1–64
chars of `[A-Za-z0-9._-]`, starting and ending alphanumeric**. That rejects the
space in `Flow Lab` — but *allows* uppercase.

## Two classes, two fixes

1. **Explicit URN/slug inputs** (this PR): flags where the user types a slug that
   becomes a URN — validated client-side, mirroring the server grammar.
2. **Name-derived URNs** (server-side): `createAgent`/`createMemory`/`updateAgent`
   build the URN from `--name`/`--urn` verbatim (`${org.urn}:${name}`) with no
   slugify or validation — the path that actually produced the bad URN. A display
   name legitimately contains spaces/caps, so the durable fix is server-side
   (and covers portal + MCP, which bypass the CLI). Filed as **hadron-server#574**.
   The uppercase-allowed grammar is filed separately as **hadron-server#575**.

## What was built

`internal/cmdutil/urnvalidate.go`:

- `isSlugAtom(atom)` — mirrors `validateAtomShape` (1–64, charset, alnum edges),
  hand-rolled (no regexp) for an allocation-free hot path.
- `ValidateURNSlug(flag, slug)` — a single slug atom (org/app slug).
- `ValidateURNPath(flag, path)` — a `:`-delimited node loc; every atom must be a
  valid slug atom, so leading/trailing/doubled colons are rejected too. `@`
  remains illegal here because it is not a valid node-loc character.
- `ValidateAgentURNPath(flag, path)` — an agent slug path that may carry an
  author org atom or spec-047 user-author namespace (`@handle:slug`).
- `CanonicalizeURN(flag, urn)` — a scheme-prefixed URN validator/canonicalizer
  for parser-parity golden cases, including spec-047 `@handle` owner/author
  namespaces, type-marker optionality, and source/self-install collapse.

Both return an `exitcode.Usage` (exit 2) error anchored on the flag name, and run
**before** `GraphQLClient()` so a bad slug never triggers a network/auth call.

### Grammar parity, not stricter

The validator **allows uppercase**, exactly as the server does today — a value
the CLI accepts is one the server accepts (no client/server drift). When
hadron-server#575 tightens the grammar to lowercase-only, `isSlugAtom` gets the
same one-line tightening. Reserved-word rejection (FR-019) stays server-side for
the same anti-drift reason.

## Sites wired

| Command | Flag | Validator |
|---|---|---|
| `org create` | `--urn` (req) | `ValidateURNSlug` |
| `org update` | `--urn` (when changed) | `ValidateURNSlug` |
| `app install` | `--urn` (when set) | `ValidateURNSlug` |
| `node add` | `--loc` (req) | `ValidateURNPath` |
| `agent update` | `--urn` (when changed) | `ValidateAgentURNPath` |

`org create/update --urn` are already server-validated (`validateOrgSlug`); the
client check is pre-flight UX there. `agent update --urn` and `node add --loc`
are the client-side-only wins (the server's own coverage on those is what #574
addresses). The `--name`-derived creates (`agent create`, `memory set`,
`ai-config create`) are intentionally left to #574 — validating a display name
as a slug would reject legitimate names.

## Tests

`internal/cmdutil/urnvalidate_test.go` — table tests for both helpers (valid +
invalid sets, the 64/65-char boundary, uppercase allowed, spaces rejected).
Command-level wiring: `TestOrgCreateRejectsInvalidURN` and
`TestNodeAddRejectsInvalidLoc` assert the mutation is never reached when the
slug is invalid.
