# ===== Shell / base =====
SHELL := /bin/bash

# ===== Apps: name:path_to_main =====
APPS := \
  psstelebot:./cmd/psstelebot \
  psstelebot-bot:./cmd/psstelebot-bot

# ===== Build matrix / dirs =====
DIST         ?= dist
OS_LIST      ?= linux darwin windows
ARCH_LIST    ?= amd64 arm64
GOX_PAR      ?= 4

PLATFORM_DIR := $(DIST)/artifacts
PKG_DIR      := $(DIST)/packages
CHECKSUM_FILE:= $(DIST)/checksums.txt

# ===== Version / ldflags =====
GIT_TAG    := $(shell git describe --tags --always --dirty --abbrev=7 2>/dev/null || echo dev)
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS    := -s -w -X main.version=$(GIT_TAG) -X main.buildDate=$(BUILD_DATE)

# ===== .env staging (optional) =====
INCLUDE_ENV  ?= 0
ENV_FILE     ?= .env
ENV_BASENAME ?= .env

# ===== GitHub release via gh =====
GH_REPO            ?=
RELEASE_TAG        ?= $(GIT_TAG)
RELEASE_TITLE      ?= "pss $(GIT_TAG)"
RELEASE_BODY       ?= "Automated build at $(BUILD_DATE)"
RELEASE_DRAFT      ?= false
RELEASE_PRERELEASE ?= false
RELEASE_OVERWRITE  ?= true

# ===== Go env =====
export CGO_ENABLED=0

# ===== Helpers =====
app_name = $(firstword $(subst :, ,$1))
app_path = $(lastword  $(subst :, ,$1))

# ===== Targets =====
.PHONY: all deps clean gox-install build-local build-all build-one stage-env package checksum release tree \
        gh-check release-gh release-gh-newtag

all: release

deps:
	@echo "==> go mod tidy"
	go mod tidy

gox-install:
	@echo "==> installing gox"
	go install github.com/mitchellh/gox@latest

clean:
	@echo "==> clean $(DIST)"
	@rm -rf "$(DIST)"

# --- Local build (current OS/ARCH) ---
build-local: deps
	@echo "==> local build"
	@mkdir -p "$(PLATFORM_DIR)/local"
	@$(foreach A,$(APPS), \
	  name=$(call app_name,$(A)); \
	  path=$(call app_path,$(A)); \
	  echo " -> $$name from $$path"; \
	  go build -trimpath -ldflags "$(LDFLAGS)" -o "$(PLATFORM_DIR)/local/$$name" $$path; \
	)

# --- Build one app (used by build-all) ---
build-one:
	@name=$(call app_name,$(APP)); \
	path=$(call app_path,$(APP)); \
	echo "==> build $$name from $$path"; \
	gox -parallel=$(GOX_PAR) \
	    -os="$(OS_LIST)" -arch="$(ARCH_LIST)" \
	    -ldflags="$(LDFLAGS)" \
	    -output="$(PLATFORM_DIR)/{{.OS}}-{{.Arch}}/$$name" \
	    $$path; \
	for exe in $(PLATFORM_DIR)/*/$$name; do \
	  [ -f "$$exe" ] || continue; \
	  case "$$exe" in *windows*) mv "$$exe" "$$exe.exe";; esac; \
	done

# --- Cross build all apps ---
build-all: deps gox-install
	@mkdir -p "$(PLATFORM_DIR)"
	@$(MAKE) build-one APP="psstelebot:./cmd/psstelebot"
	@$(MAKE) build-one APP="psstelebot-bot:./cmd/psstelebot-bot"

# --- Put .env next to psstelebot-bot (if requested and file exists) ---
stage-env:
	@set -e; \
	if [ "$(INCLUDE_ENV)" = "1" ]; then \
	  if [ -f "$(ENV_FILE)" ]; then \
	    echo "==> staging $(ENV_FILE) into platform bundles"; \
	    for dir in $(PLATFORM_DIR)/*; do \
	      [ -d $$dir ] || continue; \
	      base=$$(basename $$dir); os=$${base%-*}; \
	      ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	      if [ -f "$$dir/psstelebot-bot$$ext" ]; then \
	        cp -f "$(ENV_FILE)" "$$dir/$(ENV_BASENAME)"; \
	      fi; \
	    done; \
	  else \
	    echo "==> NOTE: $(ENV_FILE) not found, skip env staging"; \
	  fi; \
	else \
	  echo "==> INCLUDE_ENV!=1, skip env staging"; \
	fi

# --- Package by platform (both binaries + .env if present) ---
package: build-all stage-env
	@echo "==> package by platform"
	@mkdir -p "$(PKG_DIR)"
	@set -e; \
	for dir in $(PLATFORM_DIR)/*; do \
	  [ -d $$dir ] || continue; \
	  base=$$(basename $$dir); \
	  os=$${base%-*}; arch=$${base#*-}; \
	  echo " -> pack $$os $$arch"; \
	  if [ "$$os" = "windows" ]; then \
	    (cd $$dir && zip -9 -r "../../packages/pss_$(GIT_TAG)_$$os_$$arch.zip" *); \
	  else \
	    tar -C $$dir -czf "$(PKG_DIR)/pss_$(GIT_TAG)_$$os_$$arch.tar.gz" .; \
	  fi; \
	done

# --- Checksums ---
checksum: package
	@echo "==> generate checksums"
	@rm -f "$(CHECKSUM_FILE)"; touch "$(CHECKSUM_FILE)"
	@{ \
	  if command -v shasum >/dev/null 2>&1; then \
	    printf '%s\0' $(PKG_DIR)/* | sort -z | xargs -0 shasum -a 256; \
	  else \
	    printf '%s\0' $(PKG_DIR)/* | sort -z | xargs -0 sha256sum; \
	  fi; \
	} >> "$(CHECKSUM_FILE)"
	@echo "==> checksums saved to $(CHECKSUM_FILE)"

# --- Full local release pipeline ---
release: clean build-all stage-env package checksum
	@echo "==> artifacts:"
	@find "$(DIST)" -type f -maxdepth 3 | sed 's|^|  |'
	@echo "==> done."

tree:
	@command -v tree >/dev/null 2>&1 && tree -a -C "$(DIST)" || find "$(DIST)" -print

# ===== GitHub Release =====
gh-check:
	@command -v gh >/dev/null 2>&1 || { echo "✖ gh (GitHub CLI) not found. Install: https://cli.github.com/"; exit 1; }
	@gh auth status >/dev/null 2>&1 || { echo "✖ gh not authenticated. Run: gh auth login"; exit 1; }
	@echo "✓ gh ready"

release-gh: gh-check release
	@echo "==> create/update GitHub release $(RELEASE_TAG)"
	@repo_flag=""; [ -n "$(GH_REPO)" ] && repo_flag="--repo $(GH_REPO)"; \
	if [ "$(RELEASE_OVERWRITE)" = "true" ]; then \
	  gh release delete "$(RELEASE_TAG)" $$repo_flag -y >/dev/null 2>&1 || true; \
	fi; \
	gh release create "$(RELEASE_TAG)" \
	  $${repo_flag} \
	  $(if $(filter true,$(RELEASE_DRAFT)),--draft,) \
	  $(if $(filter true,$(RELEASE_PRERELEASE)),--prerelease,) \
	  --title "$(RELEASE_TITLE)" \
	  --notes "$(RELEASE_BODY)" \
	  "$(CHECKSUM_FILE)" $(PKG_DIR)/*

release-gh-newtag: gh-check
	@test -n "$(RELEASE_TAG)" || (echo "✖ RELEASE_TAG is empty" && exit 1)
	git tag -f "$(RELEASE_TAG)"
	git push -f origin "refs/tags/$(RELEASE_TAG)"
	@$(MAKE) release-gh RELEASE_OVERWRITE=true
