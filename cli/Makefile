# fernsicht CLI — build / test / release / size targets.
#
# Reproducible builds: when SOURCE_DATE_EPOCH is set, BUILD_DATE is
# derived from it so two builds at different wall-clock times produce
# identical binaries. The release pipeline sets it from the tag's
# commit timestamp; humans verifying a release set it from the same
# value documented in RELEASE.md.

BIN     := fernsicht
PKG     := github.com/MuteJester/fernsicht/cli/cmd/fernsicht
DIST    := dist

VERSION    ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo 0.0.0-dev)
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

# BUILD_DATE: respect SOURCE_DATE_EPOCH for reproducibility (the
# standard documented at https://reproducible-builds.org/specs/source-date-epoch/).
# Without it, fall back to current time. GNU date uses -d, BSD uses -r.
ifeq ($(strip $(SOURCE_DATE_EPOCH)),)
  BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
else
  BUILD_DATE := $(shell date -u -d "@$(SOURCE_DATE_EPOCH)" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || \
                date -u -r $(SOURCE_DATE_EPOCH) +%Y-%m-%dT%H:%M:%SZ)
endif

LDFLAGS := -X main.version=$(VERSION) \
           -X main.commit=$(COMMIT) \
           -X main.buildDate=$(BUILD_DATE)

# Stripped-build flags:
#   -s -w           drop symbol table + DWARF debug info
#   -buildid=       clear the build ID (random per build → kills repro)
#   -trimpath       remove filesystem paths from binary (and panic stacks)
#   -buildvcs=false don't embed git-state info (varies across checkouts)
STRIP_LDFLAGS := -s -w -buildid= $(LDFLAGS)
STRIP_FLAGS   := -trimpath -buildvcs=false -ldflags="$(STRIP_LDFLAGS)"

# Pure static binary on every platform.
export CGO_ENABLED := 0

# Release platforms.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: all build build-stripped install test vet size clean help \
        release cross-compile dist sha256sums verify-repro

all: build

## build — fast unstripped build for development.
build:
	@mkdir -p $(DIST)
	go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BIN) $(PKG)
	@echo "→ $(DIST)/$(BIN)"

## build-stripped — production-style build (stripped + trimpath).
build-stripped:
	@mkdir -p $(DIST)
	go build $(STRIP_FLAGS) -o $(DIST)/$(BIN) $(PKG)
	@echo "→ $(DIST)/$(BIN) (stripped, trimpath)"

## install — install to $GOBIN (or $GOPATH/bin).
install:
	go install -ldflags="$(LDFLAGS)" $(PKG)

## test — run all unit + integration tests.
test:
	go test ./...

## vet — go vet across the module.
vet:
	go vet ./...

## size — Phase 0 acceptance gate: stripped binary must be < 20 MB.
size: build-stripped
	@printf "\n=== Phase 0 binary-size gate ===\n"
	@ls -l $(DIST)/$(BIN) | awk '{ \
		size_bytes=$$5; \
		size_mb=size_bytes/1048576; \
		printf "Binary: $(DIST)/$(BIN)\n"; \
		printf "Size:   %d bytes (%.2f MB)\n", size_bytes, size_mb; \
		printf "Limit:  20.00 MB (Phase 0 acceptance criterion)\n"; \
		if (size_bytes < 20*1048576) \
			print "PASS — within Phase 0 budget."; \
		else \
			print "FAIL — exceeds Phase 0 budget; reconsider in-process linking."; \
	}'
	@echo

## cross-compile — Build stripped binaries for every release platform.
cross-compile:
	@mkdir -p $(DIST)
	@for plat in $(PLATFORMS); do \
		os=$${plat%/*}; arch=$${plat#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out="$(DIST)/$(BIN)-$$os-$$arch$$ext"; \
		echo "→ $$out"; \
		GOOS=$$os GOARCH=$$arch go build $(STRIP_FLAGS) -o $$out $(PKG) || exit 1; \
	done
	@echo "Cross-compile complete; binaries in $(DIST)/."

## sha256sums — generate SHA256SUMS for every artifact in dist/.
##              Output is in `sha256sum` format so it works with the
##              standard verification one-liner: sha256sum -c SHA256SUMS
sha256sums:
	@cd $(DIST) && \
		(command -v sha256sum >/dev/null && sha256sum $(BIN)-* > SHA256SUMS) || \
		(command -v shasum >/dev/null && shasum -a 256 $(BIN)-* > SHA256SUMS) || \
		(echo "neither sha256sum nor shasum available"; exit 1)
	@echo "→ $(DIST)/SHA256SUMS"
	@cat $(DIST)/SHA256SUMS

## dist — cross-compile + checksums. The release pipeline calls this,
##        then signs the result with cosign and uploads to GitHub.
dist: cross-compile sha256sums
	@echo
	@echo "Distribution artifacts ready in $(DIST)/."
	@echo "Next steps for a release:"
	@echo "  - cosign sign-blob \$$f --output-signature \$$f.sig (per binary)"
	@echo "  - upload to GH release (handled by .github/workflows/cli-release.yml)"

## verify-repro — Build twice with the same SOURCE_DATE_EPOCH and
##                compare SHA256s. Validates reproducibility locally.
verify-repro:
	@: $${SOURCE_DATE_EPOCH:?must be set, e.g.: SOURCE_DATE_EPOCH=1700000000 make verify-repro}
	@rm -rf $(DIST)
	@$(MAKE) cross-compile >/dev/null 2>&1
	@$(MAKE) sha256sums >/dev/null
	@cp $(DIST)/SHA256SUMS $(DIST)/SHA256SUMS.first
	@rm -f $(DIST)/$(BIN)-*
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

## release — Cuts a release locally (does NOT push). Use the GitHub
##           Actions workflow for actual publishing — see RELEASE.md.
release: dist
	@echo
	@echo "Local release artifacts built in $(DIST)/."
	@echo "To publish: tag commit as cli/vX.Y.Z and push; the GH"
	@echo "Actions workflow at .github/workflows/cli-release.yml"
	@echo "handles cross-compile, signing, and GitHub release creation."

## clean — remove build artifacts.
clean:
	rm -rf $(DIST)

## help — list documented targets.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s '—'
