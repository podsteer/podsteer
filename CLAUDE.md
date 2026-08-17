# K8Sense

A desktop Kubernetes client built on Wails v2 (Go backend + the OS's native
webview) rather than Electron, so that it starts fast and stays small in
memory.

## Layout

Two halves, and the split is enforced rather than conventional:

- `app/` — **all** Go code.
- `web/` — **all** frontend source (Svelte 5 + Vite + Tailwind v4).

```text
app/
├── cmd/            composition root; the only place dependencies are built
├── domain/         entities, value objects, domain events — stdlib only
├── application/    use cases orchestrating the domain
├── ports/          inbound (driving) + outbound (driven) interfaces
├── adapters/
│   ├── k8s/        client-go + cli-runtime; satisfies the Kubernetes ports
│   ├── wails/      bound structs and DTOs; the frontend API contract
│   └── assets/     embeds the built frontend
└── config/         environment-driven configuration
```

Dependencies point inward. `app/domain` and `app/ports` import nothing outside
the standard library; if either ever needs `client-go`, something has been
wired backwards.

## Two structural facts that look like mistakes

**`main.go` sits at the repository root.** The Wails CLI runs `go build` with
its working directory set to the project root and no package argument
(`pkg/commands/build/base.go`), and exposes no setting to point it elsewhere.
The root file is a three-line shim; the real entry point is `app/cmd/main.go`,
which is `package cmd` for the same reason.

**Vite builds into `app/adapters/assets/dist`.** Go's `//go:embed` cannot
reference a parent directory, so the bundle has to land beside the package that
embeds it. Frontend *source* stays in `web/`; only build output crosses over.
See `web/vite.config.ts`.

That directory's contents are git-ignored except a tracked `.gitkeep`, because
`//go:embed all:dist` will not compile if the directory is missing from a fresh
clone. `emptyOutDir` deletes the placeholder on every build, so a small Vite
plugin (`k8sense:keep-embed-directory`) rewrites it — do not remove it.

## Commands

```sh
make dev        # wails dev — hot-reloads the frontend, rebuilds Go on change
make build      # packaged application into build/bin
make test       # go test -race ./...
make check      # gofmt + go vet + svelte-check
make bindings   # regenerate TypeScript bindings after changing a bound method
```

Regenerate bindings whenever a method on `ClusterAPI`/`WorkloadAPI` or a DTO in
`app/adapters/wails/dto.go` changes — `wails dev` and `wails build` do it
automatically, `go build` does not. The generated output in
`web/src/lib/wailsjs/` is **committed**, and the `bindings` CI job fails on any
drift, so a forgotten regeneration is caught in CI rather than at runtime.

## Branching and releases

Follows the ParliTrack standard: `develop` integrates, `main` holds released
code and is only reached by PR. Tags are `v1.2.3-dev-N` / `v1.2.3-rc-N` /
`v1.2.3`, cut with `make tag`. Full detail in `docs/RELEASING.md`.

Unlike a ParliTrack service, a tag here publishes artefacts and a GitHub
Release — there is no environment to deploy into and no `iac-argocd` step.

## Where this deviates from the Service Blueprint

Two deviations, both forced by Wails rather than chosen:

- **`go.mod` is at the repository root, not in `app/`.** The Wails CLI compiles
  the root package, so the `main` package must live there — and it must be
  inside the module. CI therefore uses `go-version-file: go.mod`, not
  `app/go.mod`.
- **No `app/internal/` layer.** The blueprint nests `application/`, `domain/`
  and `adapters/` under `app/internal/`; here they sit directly under `app/`,
  with `ports/` as a sibling package rather than interfaces living beside their
  domain models.

## External systems

The local kubeconfig (`$KUBECONFIG`, else `~/.kube/config`) and the API servers
it names. Nothing else: no telemetry, no update check, no network access from
the webview (see the CSP in `web/index.html`).

## Domain quirks worth knowing

- **`PodPhaseTerminating` is not a Kubernetes phase.** A deleting pod keeps
  reporting `Running`; the mapper substitutes `Terminating` when
  `DeletionTimestamp` is set, as kubectl does.
- **`Pod.IsHealthy` is not `phase == Running`.** A crash-looping pod reports
  `Running` while serving nothing, so `Running` additionally requires every
  container ready. `Succeeded` counts as healthy — otherwise every completed
  Job would flag.
- **Error classification crosses three layers.** `adapters/k8s/errors.go` maps
  client-go failures onto the `ports.Err*` sentinels; `adapters/wails/errors.go`
  maps those onto an `ErrorCode` and encodes it as a `[code] message` prefix,
  because Wails can only send an error as a string. `web/src/lib/api/errors.ts`
  parses it back. Changing the codes means changing both ends.

## Configuration

All optional, all prefixed `K8SENSE_`: `KUBECONFIG`, `QPS`, `BURST`,
`REQUEST_TIMEOUT`, `LOG_LEVEL`, `LOG_SOURCE`. See `app/config/config.go`.
