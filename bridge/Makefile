# Fernsicht Bridge — build & release Makefile.
#
# Targets:
#   build        — build for the current platform into dist/
#   test         — run unit + integration tests with race detector
#   vet          — go vet ./...
#   clean        — remove dist/ and any test artifacts
#   release      — cross-build all five target binaries (linux/darwin/windows)
#   tidy         — go mod tidy
#   verify-cgo   — assert the binary is fully static (no dyn deps)
#
# All builds use CGO_ENABLED=0 + -trimpath + -ldflags="-s -w" for
# reproducible, fully static binaries (see BRIDGE_IMPLEMENTATION_PLAN §11).

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
PKG       := ./cmd/fernsicht-bridge
BIN_NAME  := fernsicht-bridge
DIST      := dist

GO        := go
GOFLAGS   := -trimpath
LDFLAGS   := -s -w -X main.version=$(VERSION)

# Cross-build matrix (see plan §11).
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

.PHONY: all build test vet clean release tidy verify-cgo help

all: build

help:
	@echo "Fernsicht Bridge — make targets:"
	@echo "  make build       Build for current platform → $(DIST)/"
	@echo "  make test        Run go test -race ./..."
	@echo "  make vet         Run go vet ./..."
	@echo "  make clean       Remove $(DIST)/"
	@echo "  make release     Cross-build all platform binaries"
	@echo "  make tidy        go mod tidy"
	@echo "  make verify-cgo  Assert the local-platform binary is fully static"

build: $(DIST)
	@echo "==> building $(BIN_NAME) ($(VERSION)) for $$(go env GOOS)/$$(go env GOARCH)"
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' -o $(DIST)/$(BIN_NAME) $(PKG)
	@echo "==> built $(DIST)/$(BIN_NAME) ($$(du -h $(DIST)/$(BIN_NAME) | cut -f1))"

test:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf $(DIST)/

tidy:
	$(GO) mod tidy

# Verify the local-platform binary has zero dynamic library deps. ldd
# returns non-zero on a fully-static binary; we invert that and also
# pattern-match `file` output as a belt-and-suspenders check.
verify-cgo: build
	@echo "==> verifying $(DIST)/$(BIN_NAME) is statically linked"
	@if file $(DIST)/$(BIN_NAME) | grep -q "statically linked"; then \
		echo "    OK: file reports statically linked"; \
	else \
		echo "    FAIL: file does not report statically linked:"; \
		file $(DIST)/$(BIN_NAME); \
		exit 1; \
	fi
	@if ldd $(DIST)/$(BIN_NAME) 2>&1 | grep -q "not a dynamic executable"; then \
		echo "    OK: ldd reports no dynamic deps"; \
	else \
		echo "    FAIL: ldd shows dynamic deps:"; \
		ldd $(DIST)/$(BIN_NAME); \
		exit 1; \
	fi

release: clean $(DIST)
	@echo "==> cross-building $(BIN_NAME) $(VERSION) for $(words $(PLATFORMS)) platforms"
	@$(foreach P,$(PLATFORMS), \
		GOOS=$(firstword $(subst /, ,$(P))) \
		GOARCH=$(lastword $(subst /, ,$(P))) \
		CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' \
			-o $(DIST)/$(BIN_NAME)-$(subst /,-,$(P))$(if $(findstring windows,$(P)),.exe,) \
			$(PKG) && \
		echo "    built $(DIST)/$(BIN_NAME)-$(subst /,-,$(P))$(if $(findstring windows,$(P)),.exe,)" ; \
	)
	@echo "==> generating SHA256 sums"
	@cd $(DIST) && sha256sum $(BIN_NAME)-* > SHA256SUMS
	@cat $(DIST)/SHA256SUMS

$(DIST):
	@mkdir -p $(DIST)
