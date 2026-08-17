# K8Sense

[![CI/CD Pipeline](https://github.com/k8sense/k8sense/actions/workflows/ci-cd.yaml/badge.svg)](https://github.com/k8sense/k8sense/actions/workflows/ci-cd.yaml)
[![Production](https://img.shields.io/badge/Production-Not%20Released-lightgrey)](https://github.com/k8sense/k8sense/tags)
[![Staging](https://img.shields.io/badge/Staging-No%20Candidate-lightgrey)](https://github.com/k8sense/k8sense/tags)
[![Development](https://img.shields.io/badge/Development-No%20Dev%20Release-lightgrey)](https://github.com/k8sense/k8sense/tags)
[![License](https://img.shields.io/badge/License-Proprietary-red.svg)](https://github.com/k8sense/k8sense/blob/main/LICENSE.md)

A fast, native desktop client for Kubernetes.

K8Sense is built on [Wails](https://wails.io) v2: a Go backend talking to the
operating system's own webview, rather than a bundled Chromium. There is no
second browser engine in the process tree, which is where most of an
Electron-based client's memory and startup time go.

## Status

Foundation. K8Sense connects to any cluster in your kubeconfig and lists its
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
make dev      # hot-reloading development window
make build    # packaged application in build/bin
```

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

Everything is optional and prefixed `K8SENSE_`:

| Variable                  | Default                         | Purpose                       |
| ------------------------- | ------------------------------- | ----------------------------- |
| `K8SENSE_KUBECONFIG`      | `$KUBECONFIG`, `~/.kube/config` | Alternative kubeconfig        |
| `K8SENSE_QPS`             | `50`                            | Sustained request rate        |
| `K8SENSE_BURST`           | `100`                           | Burst allowance               |
| `K8SENSE_REQUEST_TIMEOUT` | `30s`                           | Per-call deadline             |
| `K8SENSE_LOG_LEVEL`       | `info`                          | `debug`/`info`/`warn`/`error` |
| `K8SENSE_LOG_SOURCE`      | `false`                         | Include source file and line  |

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

K8Sense is read-only. It never writes to a cluster, never sends anything off
the machine, and the webview is locked down by a CSP that forbids every remote
origin — all cluster traffic goes through the Go process, never the page.
