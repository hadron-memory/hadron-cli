# hadron-cli

`hadron` is the command-line interface to the [Hadron](https://hadronmemory.com)
AI-memory platform, for humans working in a terminal and AI agents
shelling out to it.

## Status

Full v1 command surface implemented: `auth login|logout|whoami|status`,
`memory ls|get|set|rm`, `node ls|get|add|update|rm`,
`app ls|install|uninstall|use`, `config get|set|list`, `api` (raw
GraphQL escape hatch), `version`, `completion`, `agentic-usage`.

## Install

### Homebrew (macOS)

```sh
brew tap hadron-memory/tap
brew install --cask hadron
```

### Release archives (macOS, Linux, Windows)

Download the archive for your platform from the
[latest release](https://github.com/hadron-memory/hadron-cli/releases/latest),
verify against `checksums.txt`, and put `hadron` on your PATH.

### Go

```sh
go install github.com/hadron-memory/hadron-cli/cmd/hadron@latest
```

(`go install` builds without the version stamp — `hadron version` reports `dev`.)

### From source

```sh
make build        # produces bin/hadron, version-stamped
```

Requires Go (see `go.mod` for the version).

## Quick start

```sh
hadron auth login            # browser OAuth; token stored in OS keychain
hadron auth whoami
hadron memory ls --json
hadron api 'query { me { id email } }'
```

For CI/scripting, set `HADRON_TOKEN` or pipe a personal access token
to `hadron auth login --with-token`.

## For AI agents

Run `hadron agentic-usage` — it prints the full output contract,
stable exit codes, and recipes in one document. Every command
supports `--json` with stable field names.

### Claude Code plugin

This repo is also a Claude Code plugin marketplace. In Claude Code:

```
/plugin marketplace add hadron-memory/hadron-cli
/plugin install hadron-cli@hadron
```

The `hadron-cli` plugin ships a `use-hadron-cli` skill that teaches
the agent the CLI contract (auth checks, `--json`, `--yes` on
destructive commands, fully-qualified node URNs) and defers to
`hadron agentic-usage` as the runtime source of truth.

## Development

```sh
make build      # build with version stamp
make test       # go test ./...
make lint       # golangci-lint run
make generate   # regenerate genqlient code from schema/schema.graphql
make schema     # refresh schema snapshot from ../hadron-server, then generate
```

The GraphQL schema snapshot at `schema/schema.graphql` is exported
from hadron-server (`pnpm schema:export` there) and committed here;
typed operations live in `internal/api/queries/*.graphql` and are
compiled by [genqlient](https://github.com/Khan/genqlient). CI fails
if generated code drifts from the committed schema.

## Architecture notes

- **Auth (v1):** OAuth authorization-code + PKCE with a loopback
  redirect on 127.0.0.1. The server matches redirect URIs exactly, so
  each login binds the port first and registers a fresh public client
  via dynamic client registration. The resulting token is a
  long-lived `hdr_user_*` personal access token (no refresh tokens in
  v1). Stored in the OS keychain, falling back to
  `~/.config/hadron/auth.json` (0600). A device-flow strategy can
  slot in behind `internal/auth.Strategy` once the server supports
  RFC 8628.
- **Output:** commands marshal explicit DTOs, never generated
  GraphQL structs, so `--json` shapes stay stable across schema
  regenerations.
- **Exit codes** are documented contract — see
  `hadron agentic-usage`.
