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

# Reproducible-build flags (mirrors cli/Makefile):
#   -trimpath         remove filesystem paths from binary
#   -buildvcs=false   don't embed git-state info (varies across checkouts)
#   -s -w             strip symbol table + DWARF debug info
#   -buildid=         clear build ID so repeated builds produce identical SHAs
# SOURCE_DATE_EPOCH support is purely vestigial here (bridge has no
# BUILD_DATE var to inject), but honouring the env var keeps parity
# with the CLI's release pipeline story.
GOFLAGS   := -trimpath -buildvcs=false
LDFLAGS   := -s -w -buildid= -X main.version=$(VERSION)

# Cross-build matrix.
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

.PHONY: all build test vet clean release dist sha256sums verify-repro tidy verify-cgo help

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

## release / dist — cross-build all five platforms + SHA256SUMS. Both
## names exist for backward compat; `dist` matches the CLI Makefile
## and is what the release workflow invokes.
release: dist
dist: cross-compile sha256sums

cross-compile:
	@mkdir -p $(DIST)
	@echo "==> cross-building $(BIN_NAME) $(VERSION) for $(words $(PLATFORMS)) platforms"
	@$(foreach P,$(PLATFORMS), \
		GOOS=$(firstword $(subst /, ,$(P))) \
		GOARCH=$(lastword $(subst /, ,$(P))) \
		CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' \
			-o $(DIST)/$(BIN_NAME)-$(subst /,-,$(P))$(if $(findstring windows,$(P)),.exe,) \
			$(PKG) && \
		echo "    built $(DIST)/$(BIN_NAME)-$(subst /,-,$(P))$(if $(findstring windows,$(P)),.exe,)" ; \
	)

sha256sums:
	@cd $(DIST) && \
		(command -v sha256sum >/dev/null && sha256sum $(BIN_NAME)-* > SHA256SUMS) || \
		(command -v shasum >/dev/null && shasum -a 256 $(BIN_NAME)-* > SHA256SUMS) || \
		(echo "no sha256 tool found"; exit 1)
	@cat $(DIST)/SHA256SUMS

## verify-repro — build twice with the same SOURCE_DATE_EPOCH and
## confirm SHA256s match. The release pipeline runs this; humans can
## run it locally to validate reproducibility before a release.
verify-repro:
	@: $${SOURCE_DATE_EPOCH:?must be set, e.g.: SOURCE_DATE_EPOCH=1700000000 make verify-repro}
	@rm -rf $(DIST)
	@$(MAKE) cross-compile >/dev/null 2>&1
	@$(MAKE) sha256sums >/dev/null
	@cp $(DIST)/SHA256SUMS $(DIST)/SHA256SUMS.first
	@rm -f $(DIST)/$(BIN_NAME)-*
	@$(MAKE) cross-compile >/dev/null 2>&1
	@$(MAKE) sha256sums >/dev/null
	@if diff -q $(DIST)/SHA256SUMS.first $(DIST)/SHA256SUMS >/dev/null; then \
		echo "PASS — both builds produced identical SHA256SUMS:"; \
		cat $(DIST)/SHA256SUMS; \
	else \
		echo "FAIL — SHA256SUMS differ between two builds:"; \
		diff $(DIST)/SHA256SUMS.first $(DIST)/SHA256SUMS; \
		exit 1; \
	fi
