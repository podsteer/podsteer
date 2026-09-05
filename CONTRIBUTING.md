# Contributing to PodSteer

Thanks for considering it. This file covers the two things that are specific to
this project — how contributions are licensed, and what the build expects of a
change. Everything about the architecture is in [CLAUDE.md](CLAUDE.md), which is
written for both humans and coding agents and is the fastest way to understand
why the code is laid out as it is.

## Licensing: sign your work

PodSteer is Apache-2.0, and contributions come in under the same licence —
inbound equals outbound. To confirm you have the right to contribute what you
are contributing, add a `Signed-off-by` line to each commit:

```sh
git commit -s -m "your message"
```

which appends:

```text
Signed-off-by: Your Name <your.email@example.com>
```

That line means you agree to the [Developer Certificate of Origin](DCO.md).
Use your real name and an address you can be reached at; the record is public
and permanent. If you forget, `git commit --amend -s` fixes the last commit and
`git rebase --signoff <base>` fixes a branch.

There is no CLA, and there is not going to be one. See [DCO.md](DCO.md) for why
that is a deliberate choice rather than an oversight.

## Before you open a pull request

Branch from `develop` — `main` only ever receives released code, by PR from
`develop`. Then:

```sh
make check     # gofmt, go vet, golangci-lint, svelte-check
make test      # go test -race ./...
make notices   # only if you added or removed a dependency
make bindings  # only if you changed a bound method or a DTO
```

CI runs all of these, and three of them fail in ways worth anticipating:

- **`make bindings` output is committed.** `wails3 generate bindings` reads the
  Go source and writes `web/src/lib/bindings/`. Nothing else regenerates it —
  not `make build`, not `make dev` — so a forgotten `make bindings` leaves the
  frontend compiling against a contract the backend no longer honours. The
  `bindings` job fails on any drift, so that is caught in CI rather than at
  runtime.
- **`make notices` is a policy gate, not a formatting step.** It regenerates
  the dependency inventory *and* enforces
  [docs/LICENCE-POLICY.md](docs/LICENCE-POLICY.md) in one pass. A new
  dependency under an unclassifiable licence stops the build by design. If your
  change needs an exception, propose it in the same PR and say why.
- **The frontend must be built before any Wails command.** On a clean checkout
  the first Wails run exits 1 without an embedded bundle. `make dev`, `make
  build` and `make bindings` already depend on `web-build`; if you are invoking
  Wails by hand, build the frontend first.

## What a good change looks like here

- **Dependencies point inward.** `app/domain` and `app/ports` import nothing
  outside the standard library. If either needs `client-go`, something has been
  wired backwards.
- **No "current cluster".** PodSteer holds several clusters open at once, one
  per tab, so every port and service takes a `domain.ClusterID`. Wanting an
  active-cluster global is the signal you are about to break tabs.
- **Navigator entries are catalog entries.** Adding a section to the UI is an
  entry in `app/domain/catalog.go`, not a frontend change.
- **Goroutines have owners.** One owner, one way to stop, and it waits for work
  in flight. `HistoryService`'s sampler is the model, and currently the only
  long-lived one.
- **Domain logic gets a table-driven test.** `app/domain/overview.go` is a pure
  function precisely so its rules can be argued over in `overview_test.go`
  rather than observed in production.

Match the surrounding style rather than introducing your own. The comment
density here is higher than most Go projects on purpose: comments explain *why*
a thing is the way it is, especially where the obvious implementation is wrong.

## Reporting a security issue

Do not open a public issue. See [SECURITY.md](SECURITY.md).
