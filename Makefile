# PodSteer developer tasks.
#
# The Wails CLI drives dev and release builds; everything else is the native
# tooling for the two halves of the codebase. The tagging targets follow the
# ParliTrack release standard: dev tags are cut on `develop`, and `main`
# promotes the latest one to production.

MAKEFLAGS += --no-print-directory

BASE = podsteer
RAW_NAME = podsteer
APPLICATION_NAME = PodSteer

WAILS ?= wails
NPM   ?= npm
SHELL := /bin/bash
# `git branch --show-current` rather than `rev-parse --abbrev-ref HEAD`: the
# latter reports the literal string "HEAD" on a branch with no commits yet, so
# every branch guard below would reject a freshly initialised repository.
BRANCH := $(shell git branch --show-current 2>/dev/null)

# Ubuntu ships webkit2gtk 4.1 while Wails still defaults to 4.0, so Linux
# builds must opt in or cgo cannot resolve the package.
ifeq ($(shell uname -s),Linux)
	BUILD_TAGS := -tags webkit2_41
else
	BUILD_TAGS :=
endif

# Where `wails build` leaves the executable. On macOS it is buried inside the
# .app bundle; elsewhere it sits directly in build/bin.
ifeq ($(shell uname -s),Darwin)
	APP_BIN := build/bin/PodSteer.app/Contents/MacOS/podsteer
	APP_BUNDLE := build/bin/PodSteer.app
else ifeq ($(OS),Windows_NT)
	APP_BIN := build/bin/podsteer.exe
else
	APP_BIN := build/bin/podsteer
endif

# Colors
GREEN  := \033[0;32m
YELLOW := \033[0;33m
RED    := \033[0;31m
BLUE   := \033[0;34m
CYAN   := \033[0;36m
NC     := \033[0m

.PHONY: help dev build run open bindings notices sbom brand test check web-build embed-stub deps clean \
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

# Every Wails command below depends on web-build for the same reason: Wails
# generates bindings by COMPILING AND RUNNING the application, and it does that
# BEFORE it builds the frontend. PodSteer refuses to start without an embedded
# bundle (app/adapters/assets), so on a clean checkout — where the embed
# directory holds only its .gitkeep — that first run exits 1 and takes the whole
# command with it. Building the frontend first breaks the cycle.
#
# `-s` then tells Wails to skip its own frontend step rather than repeat ours.

dev: web-build
	$(WAILS) dev $(BUILD_TAGS)

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
build: web-build notices
	$(WAILS) build -clean -trimpath -s $(BUILD_TAGS)
ifdef APP_BUNDLE
	@# Wails names the bundle after `outputfilename`, which has to stay
	@# lowercase: it is also the executable name, and that is what a Linux
	@# package and a Homebrew cask put on a PATH. The BUNDLE is what a person
	@# sees in /Applications, so it is renamed here — in one place, rather
	@# than in CI and locally separately.
	@# Through a temporary name, because APFS is case-INSENSITIVE by default:
	@# `podsteer.app` and `PodSteer.app` are the same path, so removing the
	@# destination first deletes the bundle being renamed, and a direct `mv`
	@# is a no-op that leaves the old casing in place.
	@if [ -d build/bin/podsteer.app ]; then \
		mv build/bin/podsteer.app build/bin/.podsteer-rename.app; \
		mv build/bin/.podsteer-rename.app $(APP_BUNDLE); \
	fi
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

# Bindings are generated from the Go types alone, so this deliberately does NOT
# build the frontend. Building it first would deadlock the natural workflow:
# adding a new bound API means the frontend imports bindings that do not exist
# yet, so the build fails, so the bindings never get generated. A stub is all
# the embed needs to compile.
# Refreshes the Kubernetes support-window table from the release team's own
# schedule. Needs network; run it when preparing a release, not on every build,
# so an offline build stays reproducible.
releases:
	go run ./tools/releasegen
	@gofmt -w app/domain/release_schedule.go

bindings: embed-stub
	$(WAILS) generate module $(BUILD_TAGS)
	@# Wails writes the go/ bindings 755 but copies the runtime/ files straight
	@# out of Go's read-only module cache, so their permission bits vary by
	@# platform and by Wails version. None of them are executable. Left alone,
	@# regeneration produces a mode-only diff and the CI drift check fails on a
	@# change that has no content behind it at all.
	@find web/src/lib/wailsjs -type f -exec chmod 644 {} +

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
	@golangci-lint run ./...
	@echo -e "${BLUE}svelte-check${NC}"
	@$(NPM) --prefix web run check
	@echo -e "${BLUE}vitest${NC}"
	@$(NPM) --prefix web run test
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
