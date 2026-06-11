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

.PHONY: build test lint generate schema clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/hadron

test:
	go test ./...

lint:
	golangci-lint run

# Regenerate genqlient code from the committed schema snapshot.
generate:
	go tool genqlient

# Refresh the schema snapshot from the hadron-server checkout, then
# regenerate. Requires hadron-server#259 (schema:export script).
schema:
	cd $(HADRON_SERVER_DIR) && pnpm -s tsx scripts/export-graphql-sdl.mjs > $(abspath schema/schema.graphql)
	$(MAKE) generate

clean:
	rm -rf bin
