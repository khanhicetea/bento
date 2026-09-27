# Bento Go backend. Toolchain: Go 1.27 (see mise.toml). Pure-Go SQLite
# (modernc.org/sqlite), so every build is CGO_ENABLED=0.
VERSION ?= 0.2.0-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION)+$(COMMIT)
GO      ?= go
DIST    ?= ../../dist

.PHONY: build web release generate-types check-generated fmt-check vet test test-race test-integration ci clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/bento ./cmd/bento

# Build the React UI and embed it into the binary.
web:
	cd ../web && bun run build
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R ../web/dist/. internal/webui/dist/

release: web
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/bento-linux-amd64 ./cmd/bento
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/bento-linux-arm64 ./cmd/bento
	cd $(DIST) && sha256sum bento-linux-amd64 bento-linux-arm64 > SHA256SUMS

# The single reproducible command for frontend wire types.
generate-types:
	$(GO) tool tygo generate --config tygo.yaml

check-generated: generate-types
	git diff --exit-code -- ../web/src/api/generated/types.ts
	@test -z "$$(git status --porcelain -- ../web/src/api/generated)" || (echo "generated types are not committed" && git status --porcelain -- ../web/src/api/generated && exit 1)

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:" && gofmt -l . && exit 1)

vet:
	$(GO) vet ./...

# Unit and contract tests. Some tests need root (app-owned directories).
test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

# Real Docker integration tests against disposable stack roots.
test-integration:
	BENTO_DOCKER_TESTS=1 $(GO) test -count=1 -timeout 60m ./internal/integration/... ./internal/runtime/...

ci: fmt-check vet test-race check-generated build

clean:
	rm -f $(DIST)/bento $(DIST)/bento-linux-amd64 $(DIST)/bento-linux-arm64 $(DIST)/SHA256SUMS
