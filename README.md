# PodSteer

[![CI/CD Pipeline](https://github.com/podsteer/podsteer/actions/workflows/ci-cd.yaml/badge.svg)](https://github.com/podsteer/podsteer/actions/workflows/ci-cd.yaml)
[![Production](https://img.shields.io/badge/Production-v0.1.1-green)](https://github.com/podsteer/podsteer/tags)
[![Staging](https://img.shields.io/badge/Staging-No%20Candidate-lightgrey)](https://github.com/podsteer/podsteer/tags)
[![Development](https://img.shields.io/badge/Development-v0.1.1--dev--1-blue)](https://github.com/podsteer/podsteer/tags)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://github.com/podsteer/podsteer/blob/HEAD/LICENSE)

A fast, native desktop client for Kubernetes.

**[podsteer.com](https://podsteer.com)** &nbsp;|&nbsp; [What it does](https://podsteer.com/#what-it-does) &nbsp;|&nbsp; [Compared to k9s, Lens and others](https://podsteer.com/compare/) &nbsp;|&nbsp; [FAQ](https://podsteer.com/#faq)

&nbsp;

Every other Kubernetes client hands you a pod list and leaves you to work out
what is wrong. PodSteer reads the cluster and tells you — findings ranked and
grouped, capacity measured against what pods actually reserved, and every custom
resource your operators install browsable without an update here.

It is built on [Wails](https://wails.io) v2: a Go backend talking to the
operating system's own webview, rather than a bundled Chromium. There is no
second browser engine in the process tree, which is where most of an
Electron-based client's memory and startup time go.

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

## Installing

**macOS** — signed with a Developer ID, notarised by Apple, and the ticket is
stapled, so it opens without a Gatekeeper warning.

```sh
brew tap podsteer/tap
brew trust podsteer/tap
brew install --cask podsteer
```

&nbsp;

`brew trust` is not a formality and not specific to PodSteer: Homebrew refuses
to load a cask from a third-party tap until you say you trust it, because a
cask is code that runs on your machine at install time. Every tap outside
Homebrew's own needs it. You can read what you are trusting first — it is one
file, [`Casks/podsteer.rb`](https://github.com/podsteer/homebrew-tap/blob/HEAD/Casks/podsteer.rb).

&nbsp;

**Linux and Windows** — download from
[the latest release](https://github.com/podsteer/podsteer/releases/latest) and
unzip it. Linux needs `libgtk-3` and `libwebkit2gtk-4.1`.

The Windows build is **not signed**, so SmartScreen will warn on first launch —
"Windows protected your PC". That is the absence of a certificate, not a
verdict about the file. Verify the download against the published
`checksums.txt` if you want to be sure of what you have; a code-signing
certificate is a cost decision that has not been taken yet, and this note stays
here until it is.

Every release publishes SHA-256 checksums and a CycloneDX SBOM alongside the
binaries.

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

## Status

What works today: several clusters open at
once, one per tab; an overview that assesses a cluster rather than listing it,
with ranked findings and capacity measured against requests rather than usage;
purpose-built views for pods, nodes, events and the six workload controllers,
with the derived status that makes a list actually diagnostic — a crash-looping
container, a pod stuck pulling an image, one that is terminating rather than
merely running. Everything else in the cluster, custom resources included, is
browsable through the API server's own table output, so a freshly installed
operator's CRDs need no code here. Beyond reading: log streaming, an
interactive shell, scaling, restarting, editing manifests and deleting objects.

Capacity is sampled every 30 seconds while the application is open and kept
locally, which is the only way to have a trend at all — Kubernetes reports only
the present — and is deliberately presented as the narrow window it is.

[podsteer.com/download](https://podsteer.com/download/) carries the same
downloads with a little more context.

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

## Requirements

- Go 1.26+
- Node.js 20+
- The [Wails CLI](https://wails.io/docs/gettingstarted/installation):
  `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- A kubeconfig at `$KUBECONFIG` or `~/.kube/config`

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

## Running it

```sh
make deps     # frontend dependencies + go mod tidy
make run      # build it, then launch it with logs in your terminal
```

&nbsp;

`make run` produces the real packaged artefact and starts it in the foreground,
so application logs land in the terminal and Ctrl-C stops it. Add
`PODSTEER_LOG_LEVEL=debug` for the full picture of what it is asking your
cluster.

While iterating, `make dev` is the faster loop — it hot-reloads the frontend on
save and rebuilds the Go side on change, rather than repackaging each time. On
macOS, `make open` launches the built `.app` the way Finder would, with its own
Dock icon.

A locally built app is self-signed by Wails, so macOS runs it without
complaint. Downloaded release artefacts are a different matter — see
[docs/RELEASING.md](docs/RELEASING.md#code-signing).

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

## Architecture

Hexagonal (ports and adapters) with a domain-driven core. The whole point of
the layering is the direction of the arrows: **dependencies point inward**, and
the domain knows nothing about Kubernetes, Wails, or the UI.

&nbsp;

```text
                    ┌─────────────────────────────┐
   Svelte UI ─────► │  adapters/wails  (driving)  │
                    └──────────────┬──────────────┘
                                   │ ports (inbound)
                    ┌──────────────▼──────────────┐
                    │        application          │  use cases
                    └──────────────┬──────────────┘
                                   │ ports (outbound)
                    ┌──────────────▼──────────────┐
                    │   adapters/k8s  (driven)    │ ─────► API server
                    └─────────────────────────────┘

                        app/domain — entities, value
                        objects, events; stdlib only
```

&nbsp;

| Directory             | Responsibility                                                |
| :---                  | :---                                                          |
| `app/domain`          | `Cluster`, `Namespace`, `Pod`; invariants and derived status  |
| `app/ports`           | Interfaces: what the app offers, and what it needs            |
| `app/application`     | Use cases; orchestration, ordering, event publication         |
| `app/adapters/k8s`    | `client-go` + `cli-runtime`; the anti-corruption layer        |
| `app/adapters/wails`  | Bound structs and DTOs — the frontend API contract            |
| `app/cmd`             | Composition root; the only place dependencies are constructed |
| `web/src`             | Svelte 5 UI, Material Design 3 tokens over Tailwind v4        |

A concrete payoff: the entire `application` test suite runs against hand-written
fakes with no cluster, no HTTP and no Kubernetes types in sight.

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

### Performance choices

- **Protobuf, not JSON**, for core API requests — faster to decode and far
  lighter on allocations for large pod lists.
- **Clients cached per cluster.** Building one re-reads the kubeconfig and may
  spawn a credential-plugin process; that cost is paid once.
- **client-go's 5 QPS default raised**, because it is sized for a controller's
  background load, not for a UI that fans out on navigation.
- **No sourcemaps, no webfonts, no icon font** in the embedded bundle. The
  frontend is ~67 kB of JavaScript and ~21 kB of CSS.

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

## Configuration

Everything is optional and prefixed `PODSTEER_`:

| Variable                    | Default                         | Purpose                                         |
| :---                        | :---                            | :---                                            |
| `PODSTEER_KUBECONFIG`       | `$KUBECONFIG`, `~/.kube/config` | Alternative kubeconfig                          |
| `PODSTEER_QPS`              | `50`                            | Sustained request rate                          |
| `PODSTEER_BURST`            | `100`                           | Burst allowance                                 |
| `PODSTEER_REQUEST_TIMEOUT`  | `30s`                           | Per-call deadline                               |
| `PODSTEER_LOG_LEVEL`        | `info`                          | `debug`/`info`/`warn`/`error`                   |
| `PODSTEER_LOG_SOURCE`       | `false`                         | Include source file and line                    |
| `PODSTEER_UPDATE_CHECK`     | `true`                          | `false` disables the update check machine-wide  |

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

## Contributing and releasing

Contributions are welcome under Apache-2.0, signed off under the
[Developer Certificate of Origin](DCO.md) — `git commit -s`. There is no CLA.
[CONTRIBUTING.md](CONTRIBUTING.md) covers what the build expects of a change
and the handful of CI gates worth anticipating.

`develop` is the integration branch; `main` holds released code and is only
ever reached by a PR from `develop`. Tags follow the ParliTrack standard —
`v1.2.3-dev-N` for development, `v1.2.3-rc-N` for candidates, `v1.2.3` for
production — and are cut with `make tag`.

See [docs/RELEASING.md](docs/RELEASING.md) for the full cycle, what each guard
protects against, and why the generated Wails bindings are committed.

```sh
make check    # gofmt, go vet, golangci-lint, svelte-check
make test     # go test -race ./...
make tag show # what is actually released right now
```

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

## Security

PodSteer reads *and* writes: it can delete objects, scale and restart
workloads, apply edited manifests, and open a shell in a container. It does so
with the credentials your kubeconfig already grants, and it enforces no
permissions of its own — restricting what it may do is a matter of restricting
those credentials.

There is no account and no telemetry, and the webview is locked down by a CSP
that forbids every remote origin — all cluster traffic goes through the Go
process, never the page.

The one thing it contacts besides your clusters is GitHub, once a day, to see
whether a newer release exists — and only if you leave that on. It sends no
version, no platform and no identifier; the comparison happens locally. Switch
it off in Settings → Notifications, or set `PODSTEER_UPDATE_CHECK=false` to
disable it for a whole machine. [SECURITY.md](SECURITY.md) says exactly what is
and is not sent.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

&nbsp;

🔝 [back to top](#podsteer)

&nbsp;

&nbsp;

---

### PodSteer

[Website](https://podsteer.com) &nbsp;|&nbsp; [LinkedIn](https://www.linkedin.com/company/podsteer) &nbsp;|&nbsp; [BlueSky](https://bsky.app/profile/podsteer.com) &nbsp;|&nbsp; [GitHub](https://github.com/podsteer)

A [Cloudresty](https://cloudresty.com/) project

<sub>&copy; PodSteer</sub>

&nbsp;
