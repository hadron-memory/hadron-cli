BIN        := bin/hadron
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w \
  -X github.com/hadron-memory/hadron-cli/internal/build.Version=$(VERSION) \
  -X github.com/hadron-memory/hadron-cli/internal/build.Commit=$(COMMIT) \
  -X github.com/hadron-memory/hadron-cli/internal/build.Date=$(DATE)

# Sibling checkout of hadron-server, used to refresh the schema snapshot.
HADRON_SERVER_DIR ?= ../hadron-server
# Command (run inside HADRON_SERVER_DIR) that prints the server's GraphQL SDL to
# stdout. Overridable so CI can export without a full server install — the
# schema-drift workflow does a throwaway `npm install` of just tsx + graphql
# (typeDefs.ts is a self-contained SDL string, so those are the only deps).
SDL_EXPORT ?= pnpm -s tsx scripts/export-graphql-sdl.mjs

.PHONY: build test lint fmt generate schema schema-check tools-manifest tools-manifest-check clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/hadron

test:
	go test ./...

lint:
	golangci-lint run

# Apply the formatting `make lint` gates on (gofmt, per .golangci.yml).
fmt:
	golangci-lint fmt

# Regenerate genqlient code from the committed schema snapshot.
generate:
	go tool genqlient

# Refresh the schema snapshot from the hadron-server checkout, then
# regenerate. Requires hadron-server#259 (schema:export script).
schema:
	cd $(HADRON_SERVER_DIR) && $(SDL_EXPORT) > $(abspath schema/schema.graphql)
	$(MAKE) generate

# Drift detector: rebuild the SDL from the server checkout and fail if the
# generated genqlient client would change — i.e. the committed snapshot is stale
# for an operation the CLI actually uses (an appId->appRef-style server rename,
# or a shape change to a type we select). Ignores server-side changes the CLI
# doesn't touch (genqlient normalizes those away), so it's low-noise. Restores
# the working tree afterward via a temp backup, so it's safe to run on a dirty
# tree too. The schema-drift workflow runs this nightly against a fresh server
# checkout.
#
# The staleness test compares the regenerated client against the temp BACKUP,
# not against git. Comparing against HEAD would conflate "regeneration changed
# the client" (real drift) with "you have uncommitted generated code" (the
# normal state while adding an operation), so the target would cry drift at
# your own in-progress work until you committed it.
schema-check:
	@set -e; \
	bak=$$(mktemp -d); \
	cp schema/schema.graphql $$bak/schema.graphql; \
	cp internal/api/gen/generated.go $$bak/generated.go; \
	trap 'cp $$bak/schema.graphql schema/schema.graphql; cp $$bak/generated.go internal/api/gen/generated.go; rm -rf $$bak' EXIT; \
	( cd $(HADRON_SERVER_DIR) && $(SDL_EXPORT) ) > schema/schema.graphql; \
	if ! go tool genqlient; then \
	  echo "✗ schema drift: CLI operations no longer typecheck against the server SDL — run 'make schema' and reconcile."; \
	  exit 1; \
	fi; \
	if ! diff -q $$bak/generated.go internal/api/gen/generated.go >/dev/null 2>&1; then \
	  echo "✗ schema drift: the generated client is stale for an operation the CLI uses — run 'make schema' and commit."; \
	  echo "  internal/api/gen/generated.go: $$(diff $$bak/generated.go internal/api/gen/generated.go | grep -c '^<' || true) line(s) removed, $$(diff $$bak/generated.go internal/api/gen/generated.go | grep -c '^>' || true) added"; \
	  exit 1; \
	fi; \
	echo "✓ generated client in sync with $(HADRON_SERVER_DIR)"

# Refresh the registered-tool manifest (internal/cmd/spec/mcp-tools.txt) from the
# hadron-server checkout — the union of the MCP + runner tool registries that
# `hadron spec check-tools` embeds. Regenerate whenever server tools are added,
# removed, or renamed. The hand-maintained internal/cmd/spec/mcp-tools-ignore.txt
# (known non-tool hadron_* identifiers) is separate and NOT touched here.
tools-manifest:
	HADRON_SERVER_DIR=$(HADRON_SERVER_DIR) bash scripts/gen-tools-manifest.sh > internal/cmd/spec/mcp-tools.txt

# Drift detector for the tool manifest: regenerate from the server checkout and
# fail if the committed internal/cmd/spec/mcp-tools.txt is stale — the tool renamed/added
# out from under a spec that cites it (the h-* shorthand rot that motivated
# `spec check-tools`, #240). Restores the working tree afterward, so it is safe
# to run on a dirty tree too. The schema-drift workflow runs this nightly
# against a fresh server checkout.
#
# Compares against the temp BACKUP rather than git, for the same reason
# schema-check does: comparing against HEAD would report drift for an
# uncommitted manifest you had just regenerated yourself.
tools-manifest-check:
	@set -e; \
	bak=$$(mktemp -d); \
	cp internal/cmd/spec/mcp-tools.txt $$bak/mcp-tools.txt; \
	trap 'cp $$bak/mcp-tools.txt internal/cmd/spec/mcp-tools.txt; rm -rf $$bak' EXIT; \
	HADRON_SERVER_DIR=$(HADRON_SERVER_DIR) bash scripts/gen-tools-manifest.sh > internal/cmd/spec/mcp-tools.txt; \
	if ! diff -q $$bak/mcp-tools.txt internal/cmd/spec/mcp-tools.txt >/dev/null 2>&1; then \
	  echo "✗ tool-manifest drift: hadron-server's tool set changed — run 'make tools-manifest' and commit."; \
	  diff -u $$bak/mcp-tools.txt internal/cmd/spec/mcp-tools.txt || true; \
	  exit 1; \
	fi; \
	echo "✓ tool manifest in sync with $(HADRON_SERVER_DIR)"

# Report GraphQL operations hadron-server exposes that no CLI command wraps
# (#397). The committed baseline turns that inventory into a DIFF: a schema
# refresh that adds an unbound operation shows up as an added line in review.
#
# Via a TEMP file, not a redirect onto the baseline: the generator harvests the
# existing `# reason` annotations out of that same file to carry them across a
# regen, and `> internal/api/unbound-ops.txt` truncates it before the program
# ever runs — so a plain redirect silently dropped every reason it was supposed
# to preserve.
unbound-ops:
	go run ./scripts/unboundops > internal/api/unbound-ops.txt.tmp
	mv internal/api/unbound-ops.txt.tmp internal/api/unbound-ops.txt

# Drift detector for the above — fails when the baseline is stale, i.e. the
# server shipped an operation no command reaches (or one was just wired up).
# Cheap and offline: it reads the committed snapshot, not the server checkout.
unbound-ops-check:
	@go run ./scripts/unboundops -check

clean:
	rm -rf bin
