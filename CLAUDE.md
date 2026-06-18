# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

`hadron` is a Go CLI (cobra) for the Hadron AI-memory platform, used both by humans and by AI agents shelling out to it. Module `github.com/hadron-memory/hadron-cli`, entrypoint `cmd/hadron/main.go`; it talks to hadron-server over GraphQL.

## Commands

```sh
make build      # version-stamped binary at bin/hadron (ldflags from `git describe`)
make test       # go test ./...
make lint       # golangci-lint run
make generate   # regenerate genqlient code from the committed schema snapshot
make schema     # re-export the schema from ../hadron-server, then generate
```

- Run one test: `go test ./internal/cmd/ -run TestSpecGet -v` (any `<pkg> -run <name>`).
- Run the dev binary: `./bin/hadron <cmd>` (e.g. `./bin/hadron memory ls --json`). `--server <url>` points at a non-default backend; auth via `hadron auth login` or the `HADRON_TOKEN` env var.

## GraphQL codegen pipeline (read before touching the API layer)

The client is fully typed via [genqlient](https://github.com/Khan/genqlient); you never hand-write request structs.

- `schema/schema.graphql` — a committed snapshot of hadron-server's SDL; the contract genqlient checks against. Refresh with `make schema` (re-exports from the sibling `../hadron-server` checkout) — needed whenever an operation references a server field not yet in the snapshot.
- `internal/api/queries/*.graphql` — the typed operations you author. Add/edit one, then `make generate`.
- `internal/api/gen/generated.go` — generated; never hand-edit. CI fails if it drifts from the committed schema.
- Generated type names are deeply nested (e.g. `gen.NodeBatchNodeBatchNodeBatchResultNodesNode`); alias them locally (`type batchNode = gen.…`) when reused.

**Wire-semantics gotcha:** the server reads an *omitted* input field as "preserve" and an explicit `null` as "clear". Optional mutation variables therefore carry `# @genqlient(omitempty: true)` so a nil pointer is omitted, not sent as `null`. Follow the omitempty annotations already in `nodes.graphql`/`memories.graphql` when adding flags, or you'll silently clear fields.

## Output / `--json` contract (load-bearing)

Every command supports `--json`, and those shapes are a stable public contract that agents depend on. Consequently:

- Commands marshal **explicit DTO structs defined in the command package** — never genqlient structs — so `--json` stays stable across regenerations. Initialize slices to `[]T{}` (not nil) so empty fields render as `[]`, not `null`.
- Render via `output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {...})`: JSON when `--json`, else the human/table branch (`output.NewTable`).
- Exit codes are a documented contract in `internal/exitcode`; route GraphQL/transport errors through `api.MapError(err)` and usage/not-found through `exitcode.Newf(exitcode.Usage, …)` — don't return raw errors. `hadron agentic-usage` (embedded `internal/cmd/agentic/agentic-usage.md`) prints the full contract; update it when adding commands.

## Command structure

- Each group is `internal/cmd/<group>/` with a `New<Group>Cmd(*cmdutil.Factory)` constructor, wired in `internal/cmd/root.go`.
- `cmdutil.Factory` is the DI seam: lazily resolves config, the token store, and the GraphQL client (`f.GraphQLClient()`), and carries the persistent `--json/--server/--app` flags plus `f.IOStreams`. Commands take the Factory; tests inject a fake one.
- Destructive commands (`memory rm`, `node rm`, `edge rm`, `app uninstall`) prompt on a TTY and require `--yes` non-interactively (`cmdutil.ConfirmDeletion`).
- Node references are always fully-qualified URNs `<org>:<memory>:<loc>` (resolved via `cmdutil.ResolveNodeURN`); bare locs are rejected. `spec` commands instead take `-m/--memory` + a bare citation (the loc *is* a legal-code citation — see `docs/how-to/maintain-product-specs.md`).

## Whole-corpus reads — paginate, don't truncate

The server caps an unbounded `nodes` query at one default page and silently drops the rest (issue #23). Any command whose contract is "the whole memory/scope" must page to exhaustion via `scanAllNodes`/`paginateNodes` (`internal/cmd/spec/spec.go`), not a single `gen.Nodes` call. To fetch many *full* nodes, use the bulk read `api.CollectNodeBatch` (wraps `nodeBatch`: 200-node / 1 MB cap, re-queues the truncated spillover) rather than N× `GetNodeById`.

**Visibility gap:** the `nodes` *listing* can return ids that `nodeBatch`/`nodeById` then refuse — a node can list but be unreadable. Client-side fan-outs must surface those (as `unavailable`), not drop them silently.

## Testing

Command-level tests live in `internal/cmd/*_test.go` against a fake GraphQL server keyed by operation name: `testFactory(t)` + `fakeGraphQL` / `captureGraphQL` (the latter records request variables for assertions). Pure logic (serializers, pagination loops, lint rules) is unit-tested in its owning package with injected functions. Prefer these over hitting a real server.

## Conventions for changes

- **Substantial features get a design-as-built plan doc in `docs/plans/`**, bundled in the PR for review (see the existing ones).
- One change per PR, squash-merged; CI (build/test/lint + `goreleaser check`) must be green.
- **Releasing is tag-driven** (push `vX.Y.Z` → goreleaser publishes archives + auto-bumps the Homebrew cask). See the README "Releasing" section.
