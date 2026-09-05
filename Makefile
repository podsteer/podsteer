# PodSteer developer tasks.
#
# THIS FILE IS THE BUILD, and under Wails v3 that is more literally true than
# it was. v2's CLI compiled the application, ran it to generate bindings, built
# the frontend and assembled the bundle, all behind `wails build`. v3 splits
# those apart: the binary is an ordinary `go build`, the bindings come from
# static analysis, and packaging is a handful of file copies. A v3 project
# scaffolded by `wails3 init` wires that up through a tree of Taskfiles;
# PodSteer already had a Makefile and CI doing the same job, so the CLI is used
# for the two things only it can do — generating bindings and rendering icons —
# and everything else is spelled out here where it can be read.
#
# The tagging targets follow the ParliTrack release standard: dev tags are cut
# on `develop`, and `main` promotes the latest one to production.

MAKEFLAGS += --no-print-directory

BASE = podsteer
RAW_NAME = podsteer
APPLICATION_NAME = PodSteer

WAILS ?= wails3
NPM   ?= npm
SHELL := /bin/bash
# `git branch --show-current` rather than `rev-parse --abbrev-ref HEAD`: the
# latter reports the literal string "HEAD" on a branch with no commits yet, so
# every branch guard below would reject a freshly initialised repository.
BRANCH := $(shell git branch --show-current 2>/dev/null)

# Build tags.
#
# `production` is v3's own: it strips the devtools and the dev-server support
# out of the binary, and is what every release build carries. It is NOT set for
# `make dev`, which needs both.
#
comma := ,
# On Linux the tag still matters, in the other direction. v3 DEFAULTS to gtk4
# with webkitgtk-6.0, which Ubuntu 22.04 LTS does not carry at all, so taking
# the default would drop a distribution PodSteer supports today and give the
# operator nothing they can see for it. v3 ships its gtk3 backend as a
# first-class build tag against the same gtk+-3.0 and webkit2gtk-4.1 v2 used,
# so Linux builds opt into that and the system dependencies are unchanged.
# Moving to gtk4 is a user-visible platform decision, not a migration detail.
LINUX_BACKEND_TAG :=
ifeq ($(shell uname -s),Linux)
LINUX_BACKEND_TAG := gtk3
endif
RELEASE_TAGS := production$(if $(LINUX_BACKEND_TAG),$(comma)$(LINUX_BACKEND_TAG))

# Whether the build links C, stated rather than inherited. macOS links Cocoa
# and Linux links GTK, so both need a toolchain; v3's Windows backend is pure
# Go (its own pkg/w32, where v2 used go-webview2), so Windows needs none.
# Leaving this to the environment is what broke the Windows package: the
# runner carries mingw on PATH, so cgo defaulted on, and the link failed
# looking for a runtime/cgo that a pure-Go target never built.
# C linking is on: macOS links Cocoa and Linux links GTK. Windows' backend is
# pure Go and needs none, but leaving cgo on there costs nothing because
# nothing imports C, and stating one value is clearer than a conditional
# whose only branch never mattered.
#
# EXPORTED, not written as a recipe prefix: a `VAR=value cmd` prefix is shell
# syntax, and make on Windows may hand a recipe to cmd.exe, which has no such
# form — the flag would then appear in the echoed line and never reach the
# compiler.
export CGO_ENABLED := 1

# WINDOWS BUILD FLAGS, TAKEN FROM THE WAILS CLI'S OWN RECIPE rather than
# invented here. `wails3 build` is a thin wrapper around a Taskfile task, and
# this project deliberately has no Taskfile — but the task it would run is in
# the framework's templates, and this is what it passes:
#
#   -tags production -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui"
#
# `-H windowsgui` is the one that matters and the one this migration had
# dropped: without it the PE subsystem stays console, and a double-clicked
# PodSteer opens a command window behind itself. No buildmode or linkmode
# override, because the framework does not use one and every override tried
# here made the link worse rather than better.
WINDOWS_LINK :=
WINDOWS_LDFLAGS :=
ifeq ($(OS),Windows_NT)
WINDOWS_LDFLAGS := -H windowsgui
endif
BUILD_TAGS := $(LINUX_BACKEND_TAG)

# Where the build leaves things. On macOS the executable is inside the .app
# bundle this file assembles; elsewhere it sits directly in build/bin.
BIN_DIR := build/bin
ifeq ($(shell uname -s),Darwin)
	APP_BIN := $(BIN_DIR)/PodSteer.app/Contents/MacOS/podsteer
	APP_BUNDLE := $(BIN_DIR)/PodSteer.app
	DEV_BUNDLE := $(BIN_DIR)/PodSteer.dev.app
	DEV_BIN := $(DEV_BUNDLE)/Contents/MacOS/podsteer
	# v3's Cocoa layer is compiled against a 12.0 deployment target. All three
	# are needed and they are not redundant: MACOSX_DEPLOYMENT_TARGET is what
	# the compiler reads, and the two CGO flags are what the LINKER reads —
	# without them every build ends in a page of "object file was built for
	# newer macOS version than being linked" and the binary claims a floor it
	# cannot honour. Matches LSMinimumSystemVersion in build/darwin/Info.plist.
	export MACOSX_DEPLOYMENT_TARGET := 12.0
	export CGO_CFLAGS := -mmacosx-version-min=12.0
	export CGO_LDFLAGS := -mmacosx-version-min=12.0
else ifeq ($(OS),Windows_NT)
	APP_BIN := $(BIN_DIR)/podsteer.exe
	DEV_BIN := $(BIN_DIR)/podsteer.exe
else
	APP_BIN := $(BIN_DIR)/podsteer
	DEV_BIN := $(BIN_DIR)/podsteer
endif

# Colors
GREEN  := \033[0;32m
YELLOW := \033[0;33m
RED    := \033[0;31m
BLUE   := \033[0;34m
CYAN   := \033[0;36m
NC     := \033[0m

.PHONY: help dev dev-build dev-run build package run open bindings icons notices sbom brand test check web-build embed-stub deps clean \
        tag tag-show-inner tag-patch-inner tag-minor-inner tag-major-inner \
        tag-rc-inner tag-main-inner bump-inner ensure-branch ensure-clean \
        ensure-notices fetch

.DEFAULT_GOAL := help

# Capture the second word so `make tag show` reads as a subcommand rather than
# a second target.
SUBCOMMAND := $(wordlist 2,2,$(MAKECMDGOALS))

# Stop make from trying to build the subcommand as a target.
%:
	@:

define BANNER
	@echo -e "${CYAN} PodSteer${NC} - a fast, native Kubernetes client"
	@echo ""
	@echo -e " Repository : ${YELLOW}https://github.com/$(BASE)/$(RAW_NAME)${NC}"
	@echo -e " Branch     : ${YELLOW}$(BRANCH)${NC}"
	@echo ""
	@echo " ---"
	@echo ""
endef

help:
	$(BANNER)
	@echo "Development:"
	@echo "  make dev                 - Run with a hot-reloading frontend (fastest loop)"
	@echo "  make run                 - Build, then launch it with logs in this terminal"
	@echo "  make open                - Build, then open the .app like Finder would (macOS)"
	@echo "  make build               - Package the application into build/bin"
	@echo "  make web-build           - Build the frontend bundle only"
	@echo "  make bindings            - Regenerate the TypeScript bindings"
	@echo "  make icons               - Re-render the packaging icons from build/appicon.png"
	@echo "  make deps                - Install frontend deps and tidy go.mod"
	@echo "  make clean               - Remove build output"
	@echo ""
	@echo "Quality:"
	@echo "  make test                - go test -race ./..."
	@echo "  make check               - gofmt, go vet, golangci-lint, svelte-check"
	@echo ""
	@echo "Compliance (see docs/LICENCE-POLICY.md):"
	@echo "  make notices             - Regenerate the licence inventory, enforce the policy"
	@echo "  make releases            - Refresh the Kubernetes support-window table"
	@echo "  make sbom                - Emit a CycloneDX SBOM into build/bin/sbom"
	@echo ""
	@echo "Brand (see brand/README.md):"
	@echo "  make brand               - Re-render logos, the app icon and the favicons"
	@echo ""
	@echo "Release (see docs/RELEASING.md):"
	@echo "  make tag show            - Show the latest local and remote tags"
	@echo "  make tag                 - develop: next dev tag / main: promote to production"
	@echo "  make tag patch           - develop: bump patch, restart at -dev-1"
	@echo "  make tag minor           - develop: bump minor, restart at -dev-1"
	@echo "  make tag major           - develop: bump major, restart at -dev-1"
	@echo "  make tag rc              - develop: cut a release candidate from the current base"
	@echo ""

# --- Development ------------------------------------------------------------

# THE FRONTEND IS STILL BUILT FIRST, but for one reason now rather than two.
#
# Under v2 there were two: `//go:embed all:dist` needs a bundle to compile at
# all, AND binding generation compiled and RAN the application, which
# app/adapters/assets refuses to let start without one. v3 generates bindings
# by reading the source, so only the first reason survives — and it is still
# enough, which is why `build` depends on web-build and `bindings` on the
# lighter embed-stub.

# `wails3 dev` watches the Go files listed in build/config.yml and re-runs the
# commands there: `make dev-build`, Vite, then `make dev-run`. It exports
# FRONTEND_DEVSERVER_URL, which is what points the webview at Vite instead of
# at the embedded bundle.
#
# web-build first all the same: the embed has to compile before the first
# dev-build can, and the bundle it produces is what the window falls back to
# if Vite is not up yet.
dev: web-build
	$(WAILS) dev -config ./build/config.yml -port 5173

# The development binary, and on macOS the .dev.app around it.
#
# THE BUNDLE IS NOT OPTIONAL ON macOS, and this is the one thing about the dev
# loop that changed shape: v3's notification service refuses to start without a
# bundle identifier, so a bare binary reports notifications as unsupported and
# that half of the application cannot be exercised at all. The bundle costs
# three file copies. `-tags production` is deliberately absent — dev wants the
# devtools and the dev-server support that tag strips out.
dev-build: embed-stub icons
	@mkdir -p $(BIN_DIR)
	go build -gcflags=all="-l" -o $(DEV_BIN) .
ifdef DEV_BUNDLE
	@mkdir -p $(DEV_BUNDLE)/Contents/Resources
	@cp build/darwin/Info.dev.plist $(DEV_BUNDLE)/Contents/Info.plist
	@cp build/darwin/iconfile.icns $(DEV_BUNDLE)/Contents/Resources/
	@# Ad-hoc, because macOS will not hand an unsigned bundle a stable
	@# identity — and the identity is the whole reason the bundle exists.
	@codesign --force --sign - $(DEV_BUNDLE) 2>/dev/null || true
endif

dev-run:
	@$(DEV_BIN)

# `notices` as well, because the licence inventory is EMBEDDED in the binary
# this produces. A packaged application whose Credits pane disagrees with the
# dependencies actually linked into it fails the obligation it exists to meet,
# so the check runs on every real build rather than only in CI. It costs about
# two seconds and also enforces the policy — a forbidden dependency stops the
# build here rather than at release.
#
# Deliberately NOT a dependency of `dev`: the inner loop should let somebody
# try a dependency before deciding to keep it. `build`, `run` and `open` are
# where the artefact becomes real, and that is where the rule applies.
# VERSION is what the binary reports in its status bar and its logs, and it is
# what somebody quotes in a bug report — so a build from a working tree must
# not claim to be a release. `git describe --exact-match` names the tag ONLY
# when HEAD is exactly that tag; anything else, including one commit past it,
# falls back to "dev" and says so.
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
LDFLAGS = -X github.com/podsteer/podsteer/app/config.version=$(VERSION) -w -s

# PLATFORM selects the target, and exists for exactly one caller: CI's macOS
# job, which asks for `darwin/universal`. Anything else builds for this host.
PLATFORM ?=

# The packaged application, plus the two gates that must hold before one is
# real: a built frontend to embed, and a licence inventory that matches what
# the binary links.
build: web-build notices package

# The compile and the bundling, without those gates.
#
# SPLIT OUT FOR CI, which builds the frontend in a step of its own and runs the
# licence gate once in the `quality` job rather than three more times across
# the packaging matrix. Locally `make build` is still the whole thing.
#
# v2's `wails build` did the compile, the icon rendering and the bundling in
# one command. Here they are three steps, and the bundle is assembled from
# build/darwin/ — which is why that directory's Info.plist is now a literal
# file rather than a Go template the v2 CLI expanded.
package: icons
	@rm -rf $(BIN_DIR)
	@mkdir -p $(BIN_DIR)
ifeq ($(PLATFORM),darwin/universal)
	@# ONE UNIVERSAL BINARY, for the reason CI's comment gives: it is the
	@# single URL a Homebrew cask needs and it runs on Intel Macs, which a
	@# runner-native arm64 build silently does not. v2 spelled this
	@# `-platform darwin/universal`; v3 has no such flag, so it is two builds
	@# and a lipo — which is what that flag did.
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags $(RELEASE_TAGS) -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/podsteer-amd64 .
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags $(RELEASE_TAGS) -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/podsteer-arm64 .
	lipo -create -output $(BIN_DIR)/podsteer $(BIN_DIR)/podsteer-amd64 $(BIN_DIR)/podsteer-arm64
	@rm -f $(BIN_DIR)/podsteer-amd64 $(BIN_DIR)/podsteer-arm64
else
	go build -tags $(RELEASE_TAGS) $(WINDOWS_LINK) -trimpath -buildvcs=false -ldflags "$(LDFLAGS) $(WINDOWS_LDFLAGS)" -o $(BIN_DIR)/$(notdir $(APP_BIN)) .
endif
ifdef APP_BUNDLE
	@# The EXECUTABLE stays lowercase: it is what a Linux package and a
	@# Homebrew cask put on a PATH. The BUNDLE is what a person sees in
	@# /Applications, so it is capitalised — in one place, rather than in CI
	@# and locally separately.
	@mkdir -p $(APP_BUNDLE)/Contents/MacOS $(APP_BUNDLE)/Contents/Resources
	@mv $(BIN_DIR)/podsteer $(APP_BUNDLE)/Contents/MacOS/podsteer
	@cp build/darwin/iconfile.icns $(APP_BUNDLE)/Contents/Resources/
	@# The version is substituted into the COPY, never into the source file:
	@# a working tree that reported itself as a release would be the same
	@# mistake VERSION above exists to prevent.
	@sed 's|<string>0\.0\.0</string>|<string>$(VERSION)</string>|g' \
		build/darwin/Info.plist > $(APP_BUNDLE)/Contents/Info.plist
endif

# Renders the packaging assets from build/appicon.png and build/windows/.
#
# Generated on every build rather than committed, for the reason the frontend
# bundle is: they are a function of a source that IS committed, and a stale
# binary copy of one is worse than no copy. Under v2 the CLI did this silently
# inside `wails build`; v3 exposes it, so it is a step with a name.
icons:
	@mkdir -p build/darwin build/windows
	@$(WAILS) generate icons -input build/appicon.png \
		-macfilename build/darwin/iconfile.icns \
		-windowsfilename build/windows/icon.ico >/dev/null
ifeq ($(OS),Windows_NT)
	@# The resource object carries the icon, the version metadata and the
	@# DPI-aware manifest. It has to sit beside main.go for the linker to
	@# find it, which is why it is git-ignored at the root.
	@$(WAILS) generate syso -icon build/windows/icon.ico \
		-info build/windows/info.json \
		-manifest build/windows/podsteer.exe.manifest \
		-out podsteer.syso >/dev/null
endif

# Builds a release binary and launches it in the foreground, so application
# logs land in this terminal rather than in the system log. Ctrl-C stops it.
#
# Use `make dev` while iterating — it hot-reloads the frontend instead of
# rebuilding from scratch. `make run` is for checking the real, packaged
# artefact behaves the way the development window did.
run: build
	@echo ""
	@echo -e "${BLUE}Launching $(APPLICATION_NAME)... (Ctrl-C to stop)${NC}"
	@echo -e "${CYAN}Tip: PODSTEER_LOG_LEVEL=debug make run${NC} for verbose logging"
	@echo ""
	@PODSTEER_LOG_LEVEL=$${PODSTEER_LOG_LEVEL:-info} ./$(APP_BIN)

ifdef APP_BUNDLE
# Launches the packaged .app the way Finder would — detached, with its own Dock
# icon. Logs go to Console.app rather than this terminal; use `make run` when
# you want to read them.
open: build
	@echo -e "${BLUE}Opening $(APP_BUNDLE)${NC}"
	@open $(APP_BUNDLE)
endif

# Bindings are generated from the Go SOURCE alone, so this deliberately does
# NOT build the frontend. Building it first would deadlock the natural
# workflow: adding a new bound API means the frontend imports bindings that do
# not exist yet, so the build fails, so the bindings never get generated. A
# stub is all the embed needs to type-check.
# Refreshes the Kubernetes support-window table from the release team's own
# schedule. Needs network; run it when preparing a release, not on every build,
# so an offline build stays reproducible.
releases:
	go run ./tools/releasegen
	@gofmt -w app/domain/release_schedule.go

# `-ts` for TypeScript, `-i` for interfaces rather than classes.
#
# INTERFACES ARE A PERFORMANCE DECISION, not a taste one. With classes the
# generated code reconstructs every returned object through the runtime's
# `Create` helpers — which on the pod list means deep-copying five thousand
# rows on every tick, on top of the bridge payload the performance notes
# already call the second-worst cost in the application. Nothing here treats a
# DTO as anything but data, so the copy buys nothing.
#
# The output mirrors the Go IMPORT PATH under -d, which is why the frontend
# reaches it through the `$bindings` alias — see web/vite.config.ts.
bindings: embed-stub
	$(WAILS) generate bindings -ts -i -d web/src/lib/bindings ./...
	@# The generator writes files out of Go's module cache in places, so their
	@# permission bits vary by platform and by Wails version. None of them are
	@# executable. Left alone, regeneration produces a mode-only diff and the
	@# CI drift check fails on a change that has no content behind it at all.
	@find web/src/lib/bindings -type f -exec chmod 644 {} +

web-build:
	$(NPM) --prefix web run build

# Regenerates the third-party licence inventory AND enforces the licence
# policy in build/licence-policy.json.
#
# Depends on embed-stub because the generator asks `go list` what the binary
# links, and `//go:embed all:dist` will not evaluate against an empty
# directory. It also needs web/node_modules, and says so rather than silently
# reporting an empty npm set — an empty set would satisfy every policy check,
# which is the dangerous direction to fail in.
#
# Committed output, so a build needs neither the Go module cache nor
# node_modules to produce a compliant artefact. Run it after adding, removing
# or upgrading any dependency. See docs/LICENCE-POLICY.md.
notices: embed-stub
	@node build/generate-notices.mjs

# Emits a CycloneDX SBOM into build/bin/sbom, for release artefacts and for
# anyone's procurement process. Shares its collector with `notices`, so the
# two describe provably the same set of packages.
sbom: embed-stub
	@node build/generate-sbom.mjs

# Re-renders every brand PNG from its SVG source, and rewrites the two places
# the brand is wired into the application: build/appicon.png and the frontend's
# favicons. Run it after changing anything in brand/. See brand/README.md.
brand:
	@bash build/generate-brand.sh

# Ensures the embed directory holds *something*, so `go build` and the bindings
# run on a clean checkout. Never overwrites a real bundle.
embed-stub:
	@mkdir -p app/adapters/assets/dist
	@test -f app/adapters/assets/dist/index.html || \
		printf '<!doctype html><title>PodSteer</title><p>Run `make web-build`.\n' \
			> app/adapters/assets/dist/index.html

deps:
	$(NPM) --prefix web install
	go mod tidy

clean:
	rm -rf build/bin build/badges

# --- Quality ----------------------------------------------------------------

test:
	go test -race -count=1 ./...

check:
	@echo -e "${BLUE}gofmt${NC}"
	@test -z "$$(gofmt -l app main.go)" || (gofmt -l app main.go; echo -e "${RED}Files need formatting.${NC}"; exit 1)
	@echo -e "${BLUE}go vet${NC}"
	@go vet ./...
	@echo -e "${BLUE}golangci-lint${NC}"
	@golangci-lint run $(if $(BUILD_TAGS),--build-tags $(BUILD_TAGS)) ./...
	@echo -e "${BLUE}svelte-check${NC}"
	@$(NPM) --prefix web run check
	@echo -e "${BLUE}vitest${NC}"
	@$(NPM) --prefix web run test
	@echo -e "${BLUE}node --test build/licences${NC}"
	@node --test build/licences/*.test.mjs
	@echo ""
	@echo -e "${GREEN}All checks passed.${NC}"

# --- Release: guards --------------------------------------------------------

fetch:
	@git fetch --all --tags --quiet

ensure-branch:
	@if [ "$(BRANCH)" != "develop" ] && [ "$(BRANCH)" != "main" ]; then \
		echo -e "${RED}Error: You must be on 'develop' or 'main'. Current: $(BRANCH)${NC}"; \
		echo ""; \
		exit 1; \
	fi

# Refuses to tag a commit whose licence inventory does not match its
# dependencies.
#
# CI checks the same thing, but it checks it AFTER the tag exists — and a tag
# is the one thing in this workflow that cannot simply be amended, because
# pushing it is what triggers a build and a release. Catching the drift here
# costs two seconds and saves deleting a published tag.
ensure-notices:
	@node build/generate-notices.mjs > /dev/null
	@if [ -n "$$(git status --porcelain -- app/adapters/notices/notices.json)" ]; then 		echo -e "${RED}Error: the licence inventory is out of date.${NC}"; 		echo ""; 		echo -e "${YELLOW}It has been regenerated. Review and commit it, then tag again.${NC}"; 		echo ""; 		git --no-pager diff --stat -- app/adapters/notices/notices.json; 		echo ""; 		exit 1; 	fi

ensure-clean:
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo -e "${RED}Error: You have uncommitted changes.${NC}"; \
		echo ""; \
		git status --short; \
		echo ""; \
		echo -e "${YELLOW}Please commit or stash your changes before tagging.${NC}"; \
		echo ""; \
		exit 1; \
	fi

# --- Release: information ---------------------------------------------------

tag-show-inner: ensure-branch fetch
	@echo -e "${CYAN}Current branch '$(BRANCH)'${NC}"
	@echo ""
	@echo -e "${YELLOW}[Remote]${NC} (From origin)"
	@R_PROD=$$(git ls-remote --tags origin | awk '{print $$2}' | grep -v "{}" | sed 's/refs\/tags\///' | grep -v -- "-dev-" | grep -v -- "-rc-" | sort -V | tail -n1 || echo "None"); \
	R_RC=$$(git ls-remote --tags origin | awk '{print $$2}' | grep -v "{}" | sed 's/refs\/tags\///' | grep -- "-rc-" | sort -V | tail -n1 || echo "None"); \
	R_DEV=$$(git ls-remote --tags origin | awk '{print $$2}' | grep -v "{}" | sed 's/refs\/tags\///' | grep -- "-dev-" | sort -V | tail -n1 || echo "None"); \
	echo "  Production : $${R_PROD:-None}"; \
	echo "  Staging    : $${R_RC:-None}"; \
	echo "  Development: $${R_DEV:-None}"; \
	echo ""
	@echo -e "${YELLOW}[Local]${NC} (Your machine)"
	@L_PROD=$$(git tag | grep -v -- "-dev-" | grep -v -- "-rc-" | sort -V | tail -n1 || echo "None"); \
	L_RC=$$(git tag | grep -- "-rc-" | sort -V | tail -n1 || echo "None"); \
	L_DEV=$$(git tag | grep -- "-dev-" | sort -V | tail -n1 || echo "None"); \
	echo "  Production : $${L_PROD:-None}"; \
	echo "  Staging    : $${L_RC:-None}"; \
	echo "  Development: $${L_DEV:-None}"; \
	echo ""

# --- Release: develop -------------------------------------------------------

# Shared logic for auto/patch/minor/major.
#
# The "is there anything to tag" question asks whether there are commits since
# the last tag — NOT whether there are unpushed commits. Pushing as you go is
# normal, and conflating the two refuses perfectly taggable work.
bump-inner: fetch ensure-branch ensure-notices ensure-clean
	@if [ "$(BRANCH)" != "develop" ]; then \
		echo -e "${RED}Error: Development tags are only cut on 'develop'.${NC}"; \
		echo ""; \
		exit 1; \
	fi
	@echo -e "${BLUE}Checking for commits since the last release...${NC}"
	@LAST=$$(git describe --tags --match "v*-dev-*" --abbrev=0 2>/dev/null || echo ""); \
	if [ -z "$$LAST" ]; then \
		NEW=$$(git log --oneline | head -n1); \
	else \
		NEW=$$(git log $$LAST..HEAD --oneline); \
	fi; \
	if [ -z "$$NEW" ]; then \
		echo -e "${RED}Nothing new to tag - HEAD is already released as $$LAST.${NC}"; \
		exit 1; \
	fi
	@echo -e "${GREEN}Found commits to tag.${NC}"
	@echo ""
	@REMOTE_PROD=$$(git ls-remote --tags origin | awk '{print $$2}' | grep -v "{}" | sed 's/refs\/tags\///' | grep -v -- "-dev-" | grep -v -- "-rc-" | sort -V | tail -n1); \
	REMOTE_PROD=$${REMOTE_PROD:-v0.0.0}; \
	LOCAL_TAG_FULL=$$(git describe --tags --match "v*-dev-*" --abbrev=0 2>/dev/null || echo "v0.0.0-dev-0"); \
	LOCAL_BASE=$$(echo $$LOCAL_TAG_FULL | sed 's/-dev-.*//'); \
	HIGHEST_BASE=$$(printf "%s\n%s" "$$LOCAL_BASE" "$$REMOTE_PROD" | sort -V | tail -n1); \
	CLEAN_VER=$$(echo $$HIGHEST_BASE | sed 's/^v//'); \
	IFS='.' read -r major minor patch <<< "$$CLEAN_VER"; \
	if [ "$(type)" = "auto" ]; then \
		if [ "$$REMOTE_PROD" = "$$HIGHEST_BASE" ] && [ "$$REMOTE_PROD" != "$$LOCAL_BASE" ]; then \
			patch=$$((patch + 1)); \
			NEW_TAG="v$$major.$$minor.$$patch-dev-1"; \
		else \
			DEV_NUM=$$(echo $$LOCAL_TAG_FULL | awk -F'-dev-' '{print $$2}'); \
			NEXT_DEV=$$((DEV_NUM + 1)); \
			NEW_TAG="$$LOCAL_BASE-dev-$$NEXT_DEV"; \
		fi; \
	elif [ "$(type)" = "patch" ]; then \
		patch=$$((patch + 1)); \
		NEW_TAG="v$$major.$$minor.$$patch-dev-1"; \
	elif [ "$(type)" = "minor" ]; then \
		minor=$$((minor + 1)); patch=0; \
		NEW_TAG="v$$major.$$minor.$$patch-dev-1"; \
	elif [ "$(type)" = "major" ]; then \
		major=$$((major + 1)); minor=0; patch=0; \
		NEW_TAG="v$$major.$$minor.$$patch-dev-1"; \
	fi; \
	COMMIT_SHA=$$(git rev-parse HEAD); \
	echo -e "${YELLOW}Pushing code to origin/develop...${NC}"; \
	if ! git push origin develop; then \
		echo -e "${RED}Push to origin/develop FAILED - not tagging.${NC}"; \
		echo "Run 'git fetch origin && git merge origin/develop' and try again."; \
		exit 1; \
	fi; \
	echo -e "${GREEN}Creating tag: $$NEW_TAG at $$COMMIT_SHA${NC}"; \
	git tag $$NEW_TAG $$COMMIT_SHA || exit 1; \
	echo -e "${YELLOW}Pushing tag to origin...${NC}"; \
	if ! git push origin $$NEW_TAG; then \
		echo -e "${RED}Tag push FAILED. Local tag left in place - delete it with 'git tag -d $$NEW_TAG' before retrying.${NC}"; \
		exit 1; \
	fi; \
	if [ "$$(git rev-list --count origin/develop..HEAD)" != "0" ]; then \
		echo -e "${RED}WARNING: HEAD is still ahead of origin/develop after tagging.${NC}"; \
		exit 1; \
	fi; \
	echo ""; \
	echo -e "${GREEN}Tagged $$NEW_TAG${NC}"; \
	echo ""

tag-patch-inner:
	@$(MAKE) -s bump-inner type=patch

tag-minor-inner:
	@$(MAKE) -s bump-inner type=minor

tag-major-inner:
	@$(MAKE) -s bump-inner type=major

# Cuts a release candidate from the current development base. Unlike a
# production tag this does not require a merge to main — a candidate exists
# precisely to be tested before that merge happens.
tag-rc-inner: fetch ensure-branch ensure-notices ensure-clean
	@if [ "$(BRANCH)" != "develop" ]; then \
		echo -e "${RED}Error: Release candidates are cut on 'develop'.${NC}"; \
		echo ""; \
		exit 1; \
	fi
	@LAST_DEV=$$(git describe --tags --match "v*-dev-*" --abbrev=0 2>/dev/null || echo ""); \
	if [ -z "$$LAST_DEV" ]; then \
		echo -e "${RED}Error: No development tag found. Run 'make tag' first.${NC}"; \
		exit 1; \
	fi; \
	BASE_VER=$$(echo $$LAST_DEV | sed 's/-dev-.*//'); \
	LAST_RC=$$(git tag -l "$$BASE_VER-rc-*" | sort -V | tail -n1); \
	if [ -z "$$LAST_RC" ]; then \
		NEXT_RC=1; \
	else \
		NEXT_RC=$$(( $$(echo $$LAST_RC | awk -F'-rc-' '{print $$2}') + 1 )); \
	fi; \
	NEW_TAG="$$BASE_VER-rc-$$NEXT_RC"; \
	echo -e "Cutting candidate ${GREEN}$$NEW_TAG${NC} from ${YELLOW}$$LAST_DEV${NC}"; \
	git tag $$NEW_TAG "$$LAST_DEV^{}" || exit 1; \
	if ! git push origin $$NEW_TAG; then \
		echo -e "${RED}Tag push FAILED. Delete the local tag with 'git tag -d $$NEW_TAG' before retrying.${NC}"; \
		exit 1; \
	fi; \
	echo ""; \
	echo -e "${GREEN}Tagged $$NEW_TAG${NC}"; \
	echo ""

# --- Release: main ----------------------------------------------------------

# Promotes the latest development tag's base version to production. The merge
# check is what stops a production tag being cut at a commit that never went
# through review.
tag-main-inner: fetch ensure-branch ensure-notices ensure-clean
	@# BOTH PROMOTION PATHS TAG THE COMMIT THAT WAS BUILT, not the head of the
	@# branch they run on. They already SAID they promoted $$LAST_DEV; they
	@# tagged HEAD, which is not the same commit and routinely not even close.
	@#
	@# CI pushes "Update version badges [skip ci]" to develop and main after
	@# every build, so once a dev tag has been through the pipeline that badge
	@# commit is the head. Tagging it produced a tag GitHub then REFUSED to
	@# build: a push whose head commit message contains [skip ci] is skipped,
	@# and that includes a tag push. So the production release could never be
	@# cut — deterministically, and silently. The tag appeared, and no workflow
	@# ever started.
	@#
	@# Tagging the dev tag's own commit is also what promotion MEANS: the
	@# artefacts that were built, signed and notarised are the ones released,
	@# rather than a superset nobody tested.
	@echo -e "${BLUE}Checking if develop is merged into main...${NC}"
	@if ! git merge-base --is-ancestor origin/develop HEAD; then \
		echo -e "${RED}Error: 'develop' is not merged into 'main'.${NC}"; \
		echo "Please merge your code via PR first."; \
		exit 1; \
	fi
	@echo -e "${GREEN}develop is merged into main.${NC}"
	@echo ""
	@LAST_DEV=$$(git describe --tags --match "v*-dev-*" --abbrev=0 origin/develop 2>/dev/null || echo ""); \
	if [ -z "$$LAST_DEV" ]; then \
		echo -e "${RED}Error: No development tag found.${NC}"; \
		echo "Create one on 'develop' first with 'make tag'."; \
		exit 1; \
	fi; \
	NEW_TAG=$$(echo $$LAST_DEV | sed 's/-dev-.*//'); \
	if git rev-parse "$$NEW_TAG" >/dev/null 2>&1; then \
		echo -e "${RED}Error: $$NEW_TAG already exists.${NC}"; \
		echo "Bump the version on 'develop' with 'make tag patch|minor|major' first."; \
		exit 1; \
	fi; \
	echo -e "Promoting ${YELLOW}$$LAST_DEV${NC} to production: ${GREEN}$$NEW_TAG${NC}"; \
	echo ""; \
	git tag $$NEW_TAG "$$LAST_DEV^{}" || exit 1; \
	echo -e "${YELLOW}Pushing tag to origin...${NC}"; \
	if ! git push origin $$NEW_TAG; then \
		echo -e "${RED}Tag push FAILED. Delete the local tag with 'git tag -d $$NEW_TAG' before retrying.${NC}"; \
		exit 1; \
	fi; \
	echo ""; \
	echo -e "${GREEN}Successfully created and pushed production tag: $$NEW_TAG${NC}"; \
	echo ""

# --- Release: dispatch ------------------------------------------------------

# Everything that WRITES propagates failure. Only `show` suppresses errors: it
# is read-only and legitimately noisy on a repository with no tags yet.
tag:
	$(BANNER)
	@if [ "$(SUBCOMMAND)" = "show" ]; then \
		$(MAKE) -s tag-show-inner 2>/dev/null || true; \
	elif [ "$(BRANCH)" = "main" ]; then \
		if [ -n "$(SUBCOMMAND)" ]; then \
			echo -e "${RED}Error: 'main' only promotes. Run 'make tag' with no subcommand.${NC}"; \
			exit 1; \
		fi; \
		$(MAKE) -s tag-main-inner; \
	elif [ "$(BRANCH)" = "develop" ]; then \
		if [ "$(SUBCOMMAND)" = "patch" ]; then \
			$(MAKE) -s tag-patch-inner; \
		elif [ "$(SUBCOMMAND)" = "minor" ]; then \
			$(MAKE) -s tag-minor-inner; \
		elif [ "$(SUBCOMMAND)" = "major" ]; then \
			$(MAKE) -s tag-major-inner; \
		elif [ "$(SUBCOMMAND)" = "rc" ]; then \
			$(MAKE) -s tag-rc-inner; \
		elif [ -z "$(SUBCOMMAND)" ]; then \
			$(MAKE) -s bump-inner type=auto; \
		else \
			echo -e "${RED}Error: Unknown tag subcommand '$(SUBCOMMAND)'${NC}"; \
			echo "Usage: make tag [show|patch|minor|major|rc]"; \
			exit 1; \
		fi; \
	else \
		echo -e "${RED}Error: You must be on 'develop' or 'main'. Current: $(BRANCH)${NC}"; \
		echo ""; \
		exit 1; \
	fi
