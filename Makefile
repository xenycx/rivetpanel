GO ?= go
BIN := bin
# Authoritative version lives in VERSION. Release automation may still override
# it explicitly (for example `make release VERSION=1.2.3-rc.1`).
VERSION ?= $(shell tr -d '\r\n' < VERSION)

.PHONY: all web build test check integration e2e run-dev clean release version-check namespace-check

all: build

# Build the static frontend and copy it into the embed directory.
web:
	cd web && npm ci && npm run build
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -r web/build/. internal/webui/dist/

# Production binary (frontend embedded). Build the frontend first.
build: version-check web
	mkdir -p $(BIN)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o $(BIN)/rivetpanel ./cmd/rivetpanel
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o $(BIN)/rivet-agent ./cmd/rivet-agent

test:
	$(GO) vet ./...
	$(GO) test ./...

# Real-Docker tests. Needs a DISPOSABLE daemon:
#   RIVET_TEST_DOCKER_HOST=unix:///path/docker.sock [RIVET_TEST_IMAGE_PREFIX=mirror.gcr.io/library/] [RIVET_TEST_ROOTLESS=1]
integration:
	$(GO) test -tags integration -count=1 -timeout 60m -v ./tests/integration

check: version-check namespace-check test
	cd web && npm run check

# Historical release notes retain the former identity; active source,
# deployment files and documentation must use the RivetPanel namespace.
namespace-check:
	@if git grep -nEI '(botforge|botpanel|BOTPANEL)' -- ':!CHANGELOG.md' ':!Makefile' ':!docs/implementation-handoff.md'; then \
		echo 'legacy product namespace remains outside historical release notes' >&2; exit 1; \
	fi

# Keep the human-readable source of truth and npm metadata aligned. This is
# intentionally dependency-free so it also runs in minimal release builders.
version-check:
	@version="$$(tr -d '\r\n' < VERSION)"; \
	printf '%s\n' "$$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$$' || { echo "VERSION is not valid SemVer: $$version" >&2; exit 1; }; \
	package="$$(sed -n 's/^[[:space:]]*"version": "\([^"]*\)",/\1/p' web/package.json | head -1)"; \
	lock="$$(sed -n 's/^[[:space:]]*"version": "\([^"]*\)",/\1/p' web/package-lock.json | head -1)"; \
	test "$$version" = "$$package" || { echo "VERSION ($$version) != web/package.json ($$package)" >&2; exit 1; }; \
	test "$$version" = "$$lock" || { echo "VERSION ($$version) != web/package-lock.json ($$lock)" >&2; exit 1; }

# Browser checks of the core workflows against a throwaway panel (no Docker
# needed). Needs a built binary and a local Chromium (see web/tests/e2e/run.mjs).
e2e: build
	cd web && npm run test:e2e

# Versioned Linux release archives (amd64, arm64) with the binary, the
# deployment files and the documentation, plus SHA256SUMS. Set VERSION.
RELEASE_ARCHES ?= amd64 arm64
release: version-check web
	rm -rf dist && mkdir -p dist
	set -e; for arch in $(RELEASE_ARCHES); do \
		name=rivetpanel-$(VERSION)-linux-$$arch; \
		mkdir -p dist/$$name; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o dist/$$name/rivetpanel ./cmd/rivetpanel; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o dist/$$name/rivet-agent ./cmd/rivet-agent; \
		cp -r deploy dist/$$name/deploy; \
		cp -r docs dist/$$name/docs; \
		cp README.md CHANGELOG.md VERSION dist/$$name/; \
		tar -C dist --owner=0 --group=0 --sort=name -czf dist/$$name.tar.gz $$name; \
		rm -rf dist/$$name; \
	done
	cd dist && sha256sum *.tar.gz > SHA256SUMS
	@cat dist/SHA256SUMS

# Run against ./.dev-data; needs no Docker or system directories.
run-dev:
	RIVET_ENV=development $(GO) run ./cmd/rivetpanel

clean:
	rm -rf $(BIN) dist web/build web/.svelte-kit
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
