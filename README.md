# hadron-cli

`hadron` is the command-line interface to the [Hadron](https://hadronmemory.com)
AI-memory platform, for humans working in a terminal and AI agents
shelling out to it.

## Status

Full v1 command surface implemented: `auth login|logout|whoami|status`,
`memory ls|get|set|rm|clone|export`, `node ls|get|add|update|rm`,
`spec ls|get|describe|register|find|new|lint|supersede|import`,
`app ls|install|uninstall|use`, `config get|set|list`, `api` (raw
GraphQL escape hatch), `replace`, `version`, `completion`, `agentic-usage`.

Specs follow a legal-code citation scheme — flat (`<module>:<feature>:<rule>`)
or product-rooted (`<product>:<module>:<feature>:<rule>`) for a multi-product
corpus — with a general-provisions contract at every tier (feature `:00`,
module `:000`, product `:gen`). See
[docs/how-to/maintain-product-specs.md](docs/how-to/maintain-product-specs.md).

## Install

### Homebrew (macOS)

```sh
brew tap hadron-memory/hadron-cli
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

For CI/scripting, mint a token with `hadron auth token create`, then set
`HADRON_TOKEN` or pipe it to `hadron auth login --with-token`. None of the
ways to authenticate require the web portal — a self-hosted `hadron-server`
is enough; see [Authentication](docs/how-to/authentication.md).

## For AI agents

Run `hadron agentic-usage` — it prints the full output contract,
stable exit codes, and recipes in one document. Every command
supports `--json` with stable field names.

### Claude Code plugin

This repo is also a Claude Code plugin marketplace. In Claude Code:

```
/plugin marketplace add hadron-memory/hadron-cli
/plugin install hadron-cli@hadron-cli
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

## Releasing

Build and test locally first, then cut the release by pushing a semver tag:

```sh
git checkout main && git pull        # release from a green main
make build && make test              # verify the build works
./bin/hadron --help                  # sanity check
git tag -a v0.3.0 -m "v0.3.0"        # bump per semver
git push origin v0.3.0
```

The tag triggers [`.github/workflows/release.yml`](.github/workflows/release.yml),
which runs [goreleaser](https://goreleaser.com) ([`.goreleaser.yaml`](.goreleaser.yaml)) to:

- cross-compile darwin/linux/windows (amd64/arm64) binaries — version-stamped
  from the tag — into archives + `checksums.txt`;
- publish a GitHub Release with those assets and an auto-generated changelog;
- push the Homebrew cask bump to
  [homebrew-hadron-cli](https://github.com/hadron-memory/homebrew-hadron-cli),
  so `brew upgrade --cask hadron` picks it up.

The cask push uses the `HOMEBREW_TAP_TOKEN` repo secret (a token with write
access to the tap). If a release fails at the cask step, that token has expired
or lost access — rotate it; nothing else needs a secret beyond the workflow's
`GITHUB_TOKEN`.

Verify from the Actions run, the new
[release](https://github.com/hadron-memory/hadron-cli/releases/latest), and the
`goreleaserbot` cask commit in the tap.

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

## Hadron slash commands (separate plugin)

The Hadron Claude Code slash commands — `/hadron:h-task`, `/hadron:h-search`,
`/hadron:h-open-node` — live in their own marketplace,
[`hadron-memory/hadron-plugins`](https://github.com/hadron-memory/hadron-plugins),
so they're available without the CLI:

```
/plugin marketplace add hadron-memory/hadron-plugins
/plugin install hadron@hadron
```
