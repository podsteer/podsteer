# PodSteer

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

## Multi-cluster is the core assumption

PodSteer holds several clusters open at once — one per tab — so **no port or
service has a notion of "the current cluster"**. Every call takes a
`domain.ClusterID`. `application.Registry` tracks what is open (in connection
order, which is the tab order); the frontend's `workspace.svelte.ts` mirrors it
and `session.svelte.ts` holds one tab's state.

If you find yourself wanting an "active cluster" in the backend, that is the
signal you are about to break tabs.

## Two tiers of resource support

The navigator has to cover far more kinds than anyone can hand-model, so there
are deliberately two paths:

- **Rich kinds** — Pod, Node, the six workload controllers, Namespace, Event.
  Purpose-built domain entities, derived status, chosen columns. Listed in
  `domain/catalog.go` with `Rich: true`.
- **Everything else** — served by `ResourcePort.ListTable`, which asks the API
  server to print the objects as a table (the same mechanism behind
  `kubectl get`). Columns come from the server, so a freshly installed
  operator's CRDs are browsable with no code written for them.

The **cluster overview is neither tier**: it is an assessment, not a list, so
it is deliberately absent from `domain/catalog.go` and the frontend pins it
above the categories under the id `podsteer/overview`. Putting it in the catalog
would offer it to every consumer that expects to be able to GET what it names.

`domain/catalog.go` is the single source of truth for the navigator. Adding a
section to the UI is an entry there, not a frontend change. Custom resources
are appended per cluster by `DiscoverCustomKinds` — never globally, because two
clusters run different operators.

## The overview is analysis, and it lives in the domain

`app/domain/overview.go` turns a cluster snapshot into a verdict: grouped
findings, capacity, inventory. It is a pure function — no I/O, no clock, no
ordering dependence — which is why its rules are argued over in
`overview_test.go` rather than observed in production. `OverviewService` only
gathers the snapshot, concurrently, letting each source fail on its own.

Three things there are easy to get wrong and are already handled:

- **Requests, not usage, decide what schedules.** A cluster can refuse pods
  while every usage gauge looks calm. `ResourceUsage` carries allocatable,
  requests, limits and usage separately for exactly this reason.
- **`Usage` is measured across nodes; `PodUsage` across pods.** Efficiency uses
  the latter — dividing node usage (which includes the kubelet, the runtime and
  the OS) by pod requests reports clusters as over 100% efficient.
- **A pod's controller is the ReplicaSet, not the Deployment.** `ownerIndex`
  resolves the one hop, which is why the overview lists ReplicaSets and Jobs it
  never displays. Without them, findings are labelled with generated hashes and
  cannot be matched to the workload they belong to.

Terminal pods are excluded from every capacity total (`Pod.OccupiesNode`), and
cordoned nodes contribute no pod slots. Both are the standard way to produce a
utilisation figure that is quietly wrong.

Metrics are optional by design: `ports.ErrMetricsUnavailable` is an ordinary
condition, not a fault, and every list must render without metrics-server.

**And the UI must say WHY they are absent.** `domain.MetricsStatus` on the
overview separates "no metrics-server installed" (404/503) from "not permitted
to read it" (403) from a transient failure, because those need opposite advice
— telling somebody to install metrics-server when it is already running and
merely unreadable sends them to argue with an administrator. Reading a dash and
being unable to tell an unconfigured cluster from a broken application is the
bug that put this here.

A kubelet fallback for CPU and memory was considered and **deliberately not
built**, even though `filesystems.go` already proves the mechanism works. See
[docs/decisions/0001-no-kubelet-metrics-fallback.md](docs/decisions/0001-no-kubelet-metrics-fallback.md)
for what would reverse that.

Three sources beyond metrics-server behave the same way and are worth knowing
about before adding a fourth:

- **Node disk occupancy comes from the kubelets**, not from any aggregated API
  — `app/adapters/k8s/filesystems.go` reads `/stats/summary` through the API
  server's node proxy. It needs the `nodes/proxy` permission, which plenty of
  clusters do not grant, so it degrades into `Unavailable` under its own name
  rather than under "metrics". It is one request per node, hence bounded
  concurrency and a one-minute cache; a partial answer is a success.
- **Kubernetes support windows are a hand-compiled table** in
  `app/domain/release.go`. It goes stale by construction, so a release it does
  not cover is reported as `SupportUnknown` and produces nothing. Never make an
  unknown version default to unsupported.
- **A monitoring stack already in the cluster is discovered, and only pointed
  at** — `app/adapters/k8s/prometheus.go` lists Services by two label selectors
  and ranks the matches by name, because a kube-prometheus-stack install
  returns several and only one answers PromQL. Finding nothing is the ordinary
  answer and never reaches `Unavailable`: a cluster with no Prometheus is not a
  degraded cluster. PodSteer does not query it — see
  [docs/decisions/0004-usage-history-is-sampled-not-stored.md](docs/decisions/0004-usage-history-is-sampled-not-stored.md)
  for why advice is the whole feature, and why 48 hours of per-object usage on
  disk was rejected.

Dependencies point inward. `app/domain` and `app/ports` import nothing outside
the standard library; if either ever needs `client-go`, something has been
wired backwards.

## The pod pane assesses too, and there is a rule for where logic lives

`app/domain/pod_assessment.go` is the same idea at pod scope: a pure function
of one pod and a clock, returning ranked findings with advice. Its rules are
the differentiator — every other client in this category shows the fields and
leaves the conclusions to the reader.

**The line is quotation versus verdict, and it decides where code goes.** The
pod detail pane parses the manifest client-side, which is allowed for anything
that is a QUOTATION of what is already on screen in the YAML tab: a port
number, a mount path, a probe rendered into kubectl's own string. Anything
involving a comparison, a threshold or a conclusion goes through the domain,
where it can be argued with in a test. "Liveness delay is 30s" is frontend;
"this delay is shorter than the container's observed startup, which is why it
restarts" is domain.

Three rules there have subtleties worth not re-deriving:

- **The reason disambiguates an exit code, not the other way round.** 137 is
  SIGKILL and means OOMKilled only when the kubelet also said so; without that
  reason it is a grace period expiring. `domain/termination.go` has a test
  asserting we never send somebody to raise a memory limit for the second.
- **Image drift is grouped by ReplicaSet, never by Deployment.** A rollout is
  supposed to have two digests running, and those pods are in two ReplicaSets.
  Within one ReplicaSet the template is fixed, so two digests can only be a
  moved tag. StatefulSets and DaemonSets are excluded because they update in
  place.
- **A correctly configured pod produces no findings**, and a test asserts it. A
  panel that always has something to say is one people stop reading.

## Secrets are read on request, never on render

See `docs/decisions/0003-secrets-are-read-on-request-only.md` before touching
anything that displays a Secret. The short version: nothing resolves a Secret
when a pane opens, because Kubernetes' own guidance tells cluster operators to
alert on exactly that pattern; `RevealSecretKey` returns one key and discards
the rest in the adapter; and a Secret's values in the YAML tab are replaced
with their decoded size before the object is serialised, because base64 is an
encoding and not a cipher.

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
plugin (`podsteer:keep-embed-directory`) rewrites it — do not remove it.

**The frontend must be built before any Wails command.** Wails generates its
bindings by compiling *and running* the application, and it does that before it
builds the frontend. `assets.FS()` refuses to start without an embedded bundle,
so on a clean checkout that first run exits 1 and takes `wails build`,
`wails dev` and `wails generate module` down with it. This is why `dev`, `build`
and `bindings` all depend on `web-build` in the Makefile, and why CI builds the
frontend before invoking Wails. `-s` then stops Wails repeating the work.

Do not "fix" this by softening the check in `app/adapters/assets/assets.go` —
it is what turns "compiled without a frontend" into a startup error instead of
a blank window nobody can diagnose.

## History is sampled, and says so

Kubernetes reports only the present: the metrics API has no notion of a series,
so a chart of anything needs a record somebody kept. `HistoryService` samples
each connected cluster every 30 seconds while the application runs and writes
0600 files under `os.UserConfigDir()/PodSteer/history`, never anywhere else.
That is `~/Library/Application Support/PodSteer` on macOS — **not**
`~/.config`, which is only where it lands on Linux, and which this file used to
claim unconditionally. Windows is `%AppData%\PodSteer`. The Homebrew cask's
`zap` list names the macOS paths and has to be kept in step.

That makes the coverage the window the app was open, which is weaker than a
monitoring stack and **must be presented as such** — `SeriesResult.spanSeconds`
exists so the UI can say "the last 40 minutes" instead of implying more.

- **The sampler is the only long-lived goroutine.** One owner, one way to stop
  (`Close`), and it waits for the write in flight before returning. It is
  started from `OnStartup` and stopped from `OnShutdown`.
- **Retention lives in Go, not in the UI preferences.** It governs what reaches
  the disk, so the process doing the writing owns it. Zero means record nothing
  *and* erase what exists — an operator choosing it means both.
- **A sample is derived from the overview**, not from a second read of the
  cluster, so the chart and the numbers above it can never disagree.
- Samples hold capacity figures only: no object names, no logs, no manifests.

## Licences are policy, and the build enforces it

The policy lives in two halves that must be edited together:
`docs/LICENCE-POLICY.md` (the reasoning, the tiers, the exception process) and
`build/licence-policy.json` (the machine-readable form). `make notices`
regenerates the inventory AND enforces the policy in one pass, so the two can
never describe different dependency sets. CI runs it inside the `quality` gate.

Three things about the scope are easy to get wrong and are handled in
`build/licences/collect.mjs`:

- **Shipped Go is the UNION across all three release platforms.** Running
  `go list -deps` once on the host is a trap that already caught us:
  `go-webview2` and two others are reached only under `GOOS=windows`, so a
  macOS-generated inventory omitted modules the Windows binary contains.
  `CGO_ENABLED=1` is forced, or cross-GOOS silently prunes cgo dependencies.
- **A package imported by `web/src/` ships, whatever `package.json` says.**
  The import scan cross-checks this and fails the build; mislabelling one as a
  `devDependency` would hide it from the inventory and break
  `npm ci --omit=dev`. This has happened here twice.
- **Build-only tooling is judged separately** (`buildOverrides`), because
  nothing obliges you to credit a compiler you did not distribute. A
  build-scope exception is guarded by a cross-check that fails if its package
  ever enters the shipped tree.

`UNKNOWN` is a blocking tier, not an error — an unclassifiable licence must
stop a build rather than be omitted from one. A package whose declared licence
and licence text disagree resolves to `UNKNOWN` too.

`make sbom` emits CycloneDX 1.6 from the same collector; CI attaches it to
every release. `app/adapters/notices/notices_test.go` re-asserts the important
properties from Go, so `go test ./...` catches a hand-edited inventory on a
machine with no Node.

## Commands

```sh
make dev        # wails dev — hot-reloads the frontend, rebuilds Go on change
make build      # packaged application into build/bin
make test       # go test -race ./...
make check      # gofmt + go vet + svelte-check
make bindings   # regenerate TypeScript bindings after changing a bound method
make notices    # regenerate the licence inventory and enforce the policy
make sbom       # emit a CycloneDX SBOM into build/bin/sbom
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

## Licensing, and why the seam matters

The application is Apache-2.0 and is intended to stay that way, whole — not an
open core with features held back. Contributions come in under the same licence
with a DCO sign-off (`git commit -s`); there is deliberately no CLA, because a
CLA's usual purpose is to preserve a relicensing option this project does not
want. See `CONTRIBUTING.md` and `DCO.md`.

A future paid tier is planned as **server-side**: storage, alerting and
notifications living in services that are not in this repository, reached by a
client adapter that will be Apache-2.0 like everything else here. Two
consequences for code written now:

- **The community build must never require an account** or contact anything
  PodSteer operates — no telemetry, no update check, no sign-in. That is a
  product commitment, and the CSP plus the absence of any HTTP client outside
  `adapters/k8s` is what keeps it honest.
- **Anything remote is an outbound port with a local implementation first.**
  `HistoryPort` is the model and says so in its own doc comment: the store has
  to be swappable because the obvious next implementation records outside the
  application entirely.

## External systems

The local kubeconfig (`$KUBECONFIG`, else `~/.kube/config`) and the API servers
it names. Nothing else: no telemetry, no update check, no network access from
the webview (see the CSP in `web/index.html`).

The kubeconfig is **read on every call and written in exactly one place**:
`KubeconfigPort.Merge`, behind Add cluster. Everything about that write is
shaped by the fact that the file holds credentials — the paste is parsed and
the plan computed before the file is opened, an existing context name is
refused rather than replaced, symlinks are resolved so a `~/.kube` pointing
into a synced folder is written THROUGH rather than over, the previous
contents are copied to `<path>.podsteer.bak`, and the new contents reach a
temporary file in the same directory which is synced and renamed over the
target, preserving the mode. `app/adapters/k8s/kubeconfig_merge_test.go`
asserts each of those, because every one of them is a way to lose somebody's
access to a cluster quietly.

`current-context` is never touched. Adding a cluster is not a request to
switch to it, and kubectl in another terminal must not change target because
somebody pasted a config here.

## Domain quirks worth knowing

- **A Job is judged by whether it failed, not by whether it finished.**
  `Workload.IsHealthy` special-cases Jobs, because "0 of 1 completions"
  describes a job that started ten seconds ago exactly as it describes one that
  will never finish. `IsRunning` and `HasFailed` tell those apart.
- **`domain.Event` is a *Kubernetes* Event.** The application's own internal
  notifications are `domain.DomainEvent`. Getting this backwards is easy and
  the compiler will not always catch it.
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

All optional, all prefixed `PODSTEER_`: `KUBECONFIG`, `QPS`, `BURST`,
`REQUEST_TIMEOUT`, `LOG_LEVEL`, `LOG_SOURCE`. See `app/config/config.go`.
