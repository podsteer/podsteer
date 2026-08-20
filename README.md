# PodSteer

[![CI/CD Pipeline](https://github.com/podsteer/podsteer/actions/workflows/ci-cd.yaml/badge.svg)](https://github.com/podsteer/podsteer/actions/workflows/ci-cd.yaml)
[![Production](https://img.shields.io/badge/Production-Not%20Released-lightgrey)](https://github.com/podsteer/podsteer/tags)
[![Staging](https://img.shields.io/badge/Staging-No%20Candidate-lightgrey)](https://github.com/podsteer/podsteer/tags)
[![Development](https://img.shields.io/badge/Development-No%20Dev%20Release-lightgrey)](https://github.com/podsteer/podsteer/tags)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://github.com/podsteer/podsteer/blob/main/LICENSE.md)

A fast, native desktop client for Kubernetes.

PodSteer is built on [Wails](https://wails.io) v2: a Go backend talking to the
operating system's own webview, rather than a bundled Chromium. There is no
second browser engine in the process tree, which is where most of an
Electron-based client's memory and startup time go.

## Status

Foundation. PodSteer connects to any cluster in your kubeconfig and lists its
pods, with the derived status that makes a pod list actually diagnostic — a
crash-looping container, a pod stuck pulling an image, one that is terminating
rather than merely running.

## Requirements

- Go 1.24+
- Node.js 20+
- The [Wails CLI](https://wails.io/docs/gettingstarted/installation):
  `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- A kubeconfig at `$KUBECONFIG` or `~/.kube/config`

## Running it

```sh
make deps     # frontend dependencies + go mod tidy
make run      # build it, then launch it with logs in your terminal
```

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

## Architecture

Hexagonal (ports and adapters) with a domain-driven core. The whole point of
the layering is the direction of the arrows: **dependencies point inward**, and
the domain knows nothing about Kubernetes, Wails, or the UI.

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

| Directory            | Responsibility                                                |
| -------------------- | ------------------------------------------------------------- |
| `app/domain`         | `Cluster`, `Namespace`, `Pod`; invariants and derived status  |
| `app/ports`          | Interfaces: what the app offers, and what it needs            |
| `app/application`    | Use cases; orchestration, ordering, event publication         |
| `app/adapters/k8s`   | `client-go` + `cli-runtime`; the anti-corruption layer        |
| `app/adapters/wails` | Bound structs and DTOs — the frontend API contract            |
| `app/cmd`            | Composition root; the only place dependencies are constructed |
| `web/src`            | Svelte 5 UI, Material Design 3 tokens over Tailwind v4        |

A concrete payoff: the entire `application` test suite runs against hand-written
fakes with no cluster, no HTTP and no Kubernetes types in sight.

### Performance choices

- **Protobuf, not JSON**, for core API requests — faster to decode and far
  lighter on allocations for large pod lists.
- **Clients cached per cluster.** Building one re-reads the kubeconfig and may
  spawn a credential-plugin process; that cost is paid once.
- **client-go's 5 QPS default raised**, because it is sized for a controller's
  background load, not for a UI that fans out on navigation.
- **No sourcemaps, no webfonts, no icon font** in the embedded bundle. The
  frontend is ~67 kB of JavaScript and ~21 kB of CSS.

## Configuration

Everything is optional and prefixed `PODSTEER_`:

| Variable                  | Default                         | Purpose                       |
| ------------------------- | ------------------------------- | ----------------------------- |
| `PODSTEER_KUBECONFIG`      | `$KUBECONFIG`, `~/.kube/config` | Alternative kubeconfig        |
| `PODSTEER_QPS`             | `50`                            | Sustained request rate        |
| `PODSTEER_BURST`           | `100`                           | Burst allowance               |
| `PODSTEER_REQUEST_TIMEOUT` | `30s`                           | Per-call deadline             |
| `PODSTEER_LOG_LEVEL`       | `info`                          | `debug`/`info`/`warn`/`error` |
| `PODSTEER_LOG_SOURCE`      | `false`                         | Include source file and line  |

## Contributing and releasing

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

## Security

PodSteer is read-only. It never writes to a cluster, never sends anything off
the machine, and the webview is locked down by a CSP that forbids every remote
origin — all cluster traffic goes through the Go process, never the page.
