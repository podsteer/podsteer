# podsteer

PodSteer is a desktop Kubernetes client (Apache-2.0) built on Wails v3 (Go backend + the OS's native webview, Svelte 5 frontend), not Electron. It talks only to the API servers named in the operator's kubeconfig (plus `api.github.com` for the opt-out update check); `podsteer mcp` exposes read-only tools to a coding agent over stdio.

## Build / run / test
- `make dev` (hot-reload), `make run`, `make build` (packaged app in `build/bin`), `make web-build`, `make bindings`, `make icons`.
- `make test` = `go test -race -count=1 ./...`. `make check` = gofmt, `go vet`, `golangci-lint run --build-tags <tags>`, svelte-check, vitest, `node --test build/licences`.
- Frontend: `npm --prefix web run check|test|build`; layout benchmark `npm --prefix web run bench:layout` (never part of `npm test`).
- Build tags: release builds use `production`; Linux adds `gtk3`; macOS adds `private_mac_apis` (Makefile `PLATFORM_TAGS`; a hand-written `go build` on a Mac must add it). CI lint is `golangci-lint run --build-tags gtk3` even though no `.golangci` file exists.
- Local gate before `make tag`: gofmt, `go vet ./...`, `go test -race ./app/...`, golangci-lint, `npm run check && npm test`, `npm run build`, `make notices` (run from the MAIN checkout, not a /private/tmp worktree).
- Run `make bindings` whenever a bound method or a DTO in `app/adapters/wails/` changes; `web/src/lib/bindings/` is committed and CI fails on drift.
- Releases: `make tag` (develop = dev tag, main = promote), `make tag minor|major|rc`; see `docs/RELEASING.md`. A tag publishes artefacts and a GitHub Release; there is no `iac-argocd` step.
- Licence policy is enforced by `make notices` (`docs/LICENCE-POLICY.md`, `build/licence-policy.json`).

## Layout
- `main.go` is a 3-line shim; the real entry and composition root is `app/cmd/main.go` (`app/cmd/mcp.go` for `podsteer mcp`).
- `app/domain` entities, pure analysis and planning, stdlib only. `app/ports` inbound/outbound interfaces, stdlib only.
- `app/application` use cases (registry, workload, overview, fleet, topology, traffic, metricsquery, history, forwardkeeper).
- `app/adapters/k8s` client-go adapter (read cache, watch mirror, apply, drain, shells, Helm, Prometheus discovery).
- `app/adapters/wails` bound services and DTOs (the frontend API contract); `adapters/mcp` read-only MCP; `adapters/settings`, `localshell`, `vendorcli`, `archive`, `shellpath`, `assets`.
- `app/safego` goroutine panic containment (stdlib only); `app/config` env config.
- `web/` all frontend source (Svelte 5, Vite, Tailwind v4); Vite builds into `app/adapters/assets/dist` for `go:embed`.
- `build/` packaging, licence collector (`build/licences/`); `docs/` licence policy and releasing.

## External systems
- Local kubeconfig (`$KUBECONFIG` or `~/.kube/config`, plus `PODSTEER_KUBECONFIG_DIR` and settings sources) and the API servers it names. No telemetry, no account, no webview network access (CSP `connect-src 'self'`).
- `api.github.com` update check (`PODSTEER_UPDATE_CHECK=false` disables it). Optional cloud CLIs the operator already has (`vendorcli`).
- State on disk: `os.UserConfigDir()/PodSteer/settings.json` and `history/` (capacity figures only); no object names.
- Env vars (all optional, prefix `PODSTEER_`): `KUBECONFIG`, `KUBECONFIG_DIR`, `QPS`, `BURST`, `REQUEST_TIMEOUT`, `LOG_LEVEL`, `LOG_SOURCE`, `LIVE_WATCH`, `UPDATE_CHECK`, `COPY_MAX_BYTES`, `COPY_MAX_ENTRIES` (`app/config/config.go`).
- Sibling repos: `podsteer/podsteer.com` (site; composes release asset names), `podsteer/homebrew-tap`, `podsteer/business-docs` (ADRs).

## Invariants
- `app/domain` and `app/ports` import only the standard library; adapters never import each other.
- No port or service has a "current cluster"; every call takes a `domain.ClusterID`.
- Wails v3 beta is pinned exactly in four places: `go.mod`, `web/package.json`, `ci-cd.yaml`, README install line.
- Never persist object names to disk (timeline, recent objects, listings, snoozes stay in memory or localStorage); the only settings exceptions are `PreferredBackend` and `keptForwards`.
- Secrets are read on explicit request only (`RevealSecretKey`); never on render, never listed by topology/graph reads, never reachable from MCP.
- Nothing polls on the refresh tick except pod/workload lists; Helm, RBAC, reachability, image, topology and metrics-query reads run on a gesture.
- A List with a `Limit` must not use `cachedResourceVersion "0"`; `TestNoLimitedListAsksForTheWatchCache` enforces it.
- `UpdateResource` requires a `resourceVersion`; declared intent goes through `ApplyResource` (server-side apply, fieldManager `podsteer`).
- Every long-lived goroutine has one owner, a waiting stop, `safego.Recover`, and a line in `OnShutdown`.
- A bare launch of the binary must open the window; subcommands only when named.
- An empty or failed read is never rendered as "none found" or as `[]`; refused, bounded and absent are distinct statuses.
- The MCP server has no write tools and takes narrowed reader interfaces.
- Every shipped dependency is a licence-policy decision; Go and npm deps are checked by `make notices`.
- Keep the Linux backend on `gtk3` and the macOS build on `private_mac_apis`.

## Knowledge in SynapCTX
Decisions, pitfalls and history for this repo live in SynapCTX org memory (podsteer). Use `recall_memory` with your task. File-specific traps surface automatically when you edit those files.
