# Implementation Plan: App-scoped memory creation and attachment

Issue: hadron-memory/hadron-cli#233

## Goal

Expose the server's explicit App-memory mutations through the existing
`memory` command group:

- `memory set --app <ref> --agent <ref> --class <class> --name <name>` creates
  a new App-scoped `app`, `personal`, or `private` memory.
- `memory attach <memory-ref> --app <ref> --agent <ref>` attaches an existing
  free-standing personal/private memory to an App.

The server remains authoritative for reference resolution, authorization,
installed-Agent checks, and typed domain errors.

## Contract decisions

- `--app` and `--agent` are an all-or-nothing pair on `memory set`.
- App-scoped creation requires `--name` and an explicit App-associable
  `--class`; it does not accept the free-standing-only `--org` or
  `--visibility` flags.
- App-scoped creation rejects `--slug`: `createMemoryInApp` intentionally has
  no slug input. App-class URNs are name-derived by the server, while
  personal/private URNs use an opaque per-owner identity.
- Update mode rejects `--app` and `--agent`; attaching is a separate command so
  a script cannot accidentally change scope while editing ordinary fields.
- `memory attach` normalizes the advertised short memory-URN forms, passes all
  refs to the typed mutation, and renders the existing stable `memoryDTO` in
  JSON mode.
- GraphQL/domain failures continue through `api.MapError`, preserving the CLI's
  documented exit-code behavior and the server's useful typed messages.

## Files and verification

- Add schema snapshot entries and genqlient operations in
  `internal/api/queries/memories.graphql`; regenerate `internal/api/gen`.
- Extend `internal/cmd/memory/set.go`, add `attach.go`, and register it in the
  memory command root.
- Cover operation routing, wire variables, JSON output, and invalid flag
  combinations with fake-GraphQL command tests.
- Update `agentic-usage` so agents can discover both surfaces and their
  ref/slug semantics.
- Run focused command tests, `make generate`, `make test`, `make build`, lint,
  and the Hadron CLI review checklist.

## Server dependency

The server operations landed in hadron-server#655. The committed SDL snapshot
was refreshed from the merged server `origin/main` and the typed client was
regenerated from that authoritative export.
