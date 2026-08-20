<!--
Target `develop`. `main` only ever receives released code, by PR from `develop`.
-->

## What this changes

<!-- And why. If it fixes an issue, "Fixes #123". -->

## Checklist

- [ ] Every commit is signed off (`git commit -s`) — see [DCO.md](../DCO.md)
- [ ] `make check` and `make test` pass locally
- [ ] `make bindings` re-run and the result committed, if a bound method or DTO changed
- [ ] `make notices` re-run, if a dependency was added or removed
- [ ] Repo docs updated, if this moved a package, a command, or an architectural boundary
