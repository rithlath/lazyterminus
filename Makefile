BINARY    := lazyterminus
PKG       := ./cmd/lazyterminus
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
PREFIX    ?= $(HOME)/.local
GOLANGCI  ?= golangci-lint

.PHONY: all build install test race vet lint check dist mock clean

all: check build

build: ## Build for this machine into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

install: build ## Install to $(PREFIX)/bin (default ~/.local/bin)
	install -d $(PREFIX)/bin
	install -m 0755 bin/$(BINARY) $(PREFIX)/bin/$(BINARY)

test: ## Unit and UI tests (mock runner only; never calls the real Terminus)
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

lint:
	$(GOLANGCI) run ./...

check: vet lint test

dist: ## Static binaries for linux and darwin, amd64 and arm64, in dist/
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; out=dist/$(BINARY)-$(VERSION)-$$os-$$arch; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o $$out $(PKG) || exit 1; \
	done
	@cd dist && { command -v sha256sum >/dev/null && sha256sum $(BINARY)-$(VERSION)-* || shasum -a 256 $(BINARY)-$(VERSION)-*; } > SHA256SUMS

mock: build ## Run against built-in fixtures
	bin/$(BINARY) --mock

clean:
	rm -rf bin dist
