# Bento Go backend. Toolchain: Go 1.27 (see mise.toml). Pure-Go SQLite
# (modernc.org/sqlite), so every build is CGO_ENABLED=0.
VERSION ?= 0.2.0-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION)+$(COMMIT)
GO      ?= go
DIST    ?= ../../dist

.PHONY: build web release vendor-minicrond-ui generate-types check-generated fmt-check vet test test-race test-integration ci clean

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

# Vendor minicrond's built web UI (the minicrond-web-<version>.tar.gz release
# asset, checked against its published .sha256) into internal/schedulerui. The
# scheduler gateway serves it so no page or script comes from the app-owned
# socket. Run after bumping MINICROND_VERSION and review the diff: the committed
# files are what ships.
MINICROND_VERSION = $(shell sed -n 's/.*"MINICROND_VERSION": *"\([^"]*\)".*/\1/p' internal/domain/catalog.go)
vendor-minicrond-ui:
	@test -n "$(MINICROND_VERSION)" || (echo "MINICROND_VERSION not found in catalog.go" && exit 1)
	set -eu; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	rel="https://github.com/khanhicetea/minicrond/releases/download/$(MINICROND_VERSION)"; \
	asset="minicrond-web-$(MINICROND_VERSION).tar.gz"; \
	curl -fsSL -o "$$tmp/web.tar.gz" "$$rel/$$asset"; \
	curl -fsSL -o "$$tmp/web.sha256" "$$rel/$$asset.sha256"; \
	want=$$(cut -d' ' -f1 "$$tmp/web.sha256"); got=$$(sha256sum "$$tmp/web.tar.gz" | cut -d' ' -f1); \
	test "$$want" = "$$got" || { echo "$$asset: sha256 $$got, release says $$want"; exit 1; }; \
	mkdir "$$tmp/assets"; tar -xzf "$$tmp/web.tar.gz" -C "$$tmp/assets" --no-same-owner --no-same-permissions; \
	rm -rf internal/schedulerui/assets; mkdir internal/schedulerui/assets; \
	cp "$$tmp"/assets/index.html "$$tmp"/assets/app-*.js "$$tmp"/assets/style-*.css internal/schedulerui/assets/; \
	printf '%s %s\n' "$(MINICROND_VERSION)" "$$got" > internal/schedulerui/VERSION; \
	cat internal/schedulerui/VERSION

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
