# Releasing

PodSteer follows the ParliTrack branch and tag standard. What differs is what a
tag *does*: a desktop application has no environment to deploy into, so tags
produce artefacts and GitHub Releases rather than a rollout.

&nbsp;

## Branches

| Branch    | Purpose                                                  |
| --------- | -------------------------------------------------------- |
| `develop` | Integration branch. All work merges here first.          |
| `main`    | Released code. Only ever reached by a PR from `develop`. |

Feature work happens on a branch off `develop` and returns by PR. `main` is
never committed to directly.

&nbsp;

## Tags

| Form           | Tier        | Produced by           | CI result               |
| -------------- | ----------- | --------------------- | ----------------------- |
| `v1.2.3-dev-4` | Development | `make tag` on develop | Artefacts, kept 14 days |
| `v1.2.3-rc-1`  | Staging     | `make tag rc`         | GitHub **pre-release**  |
| `v1.2.3`       | Production  | `make tag` on main    | GitHub **release**      |

The base version is carried forward: `v1.2.3-dev-7` promotes to `v1.2.3`. That
is why the production tag is a *promotion* rather than a fresh number — the
artefact CI publishes is built from the same commit the dev tag validated.

&nbsp;

## The normal cycle

```sh
# On develop, after merging some work
make check                  # gofmt, vet, lint, svelte-check
make test                   # go test -race
make tag                    # pushes develop, cuts v1.2.3-dev-N

# When the version itself should move
make tag minor              # cuts v1.3.0-dev-1

# Optional: hand something to testers
make tag rc                 # cuts v1.3.0-rc-1, a GitHub pre-release

# Ship it
gh pr create --base main --head develop
# ...after the PR merges:
git switch main && git pull
make tag                    # promotes to v1.3.0
```

`make tag show` prints the latest local and remote tag in each tier, which is
the fastest way to answer "what is actually released right now".

&nbsp;

## Guards, and why each exists

`make tag` refuses to run when any of these fail:

- **Not on `develop` or `main`.** Tagging a feature branch produces a release
  from a commit that was never reviewed.
- **Uncommitted changes.** The tag would point at a commit that does not
  contain what you just tested.
- **Nothing new since the last tag.** Two tags on one commit make the history
  lie about what shipped when.
- **On `main`: `develop` not merged in.** Stops a production tag being cut at a
  commit that bypassed review.
- **On `main`: the target version already exists.** Bump on `develop` first.

Two failure modes are guarded explicitly because they are silent otherwise:

- **The push to `develop` is checked before tagging.** CI pushes badge commits
  to `develop`, so a local branch goes stale within minutes. If the push is
  rejected and the tag is created anyway, pushing the tag pushes its objects —
  so CI builds and publishes perfectly happily while `develop` never receives
  the commit. Nothing fails; the branch is just quietly missing the work.
- **`HEAD` is verified to be level with `origin/develop` afterwards.** The
  belt to that braces.

&nbsp;

## What CI does

`.github/workflows/ci-cd.yaml`:

1. **`quality`** — frontend build and type-check, then `go build`, `go vet`,
   `go test -race`, `golangci-lint`. It carries **no `if:` condition**, and
   that is deliberate: every later job declares `needs: quality`, and a
   *skipped* dependency skips its dependents. Gate this job to branches and
   every tag-driven release stops silently — not a failure, just nothing
   published.
2. **`bindings`** — regenerates the Wails TypeScript bindings and fails if the
   committed ones differ. See below.
3. **`package`** — builds and packages on macOS, Windows and Linux.
4. **`release`** — on `-rc-` and production tags only, publishes a GitHub
   Release with all three archives.
5. **`tap`** — on production tags only, updates the Homebrew cask. See below.

`.github/workflows/update-version-badges.yaml` rewrites the README badges on
both branches whenever a tag is pushed. Its commits carry `[skip ci]`, without
which they would re-trigger the pipeline on a badge URL change.

&nbsp;

## Homebrew is the update channel

For macOS this is how a released version actually reaches somebody, so it is
part of shipping rather than a nicety: `brew upgrade --cask podsteer` is what an
installed user runs, and it serves whatever `podsteer/homebrew-tap` says.

**It is automated, and the automation is easy to believe is working when it is
not.** The `tap` job in `ci-cd.yaml` calls `homebrew.yaml`, which rewrites only
the `version` and `sha256` lines of `Casks/podsteer.rb` and pushes. Three things
about it are load-bearing:

- **It is `needs: release`, and a separate job rather than a step.** The
  checksum is computed by downloading the asset as GitHub serves it, which is
  only possible once the release job has finished uploading. Folding it into
  the release job races the upload.
- **It runs on `workflow_call`, not on the `release: published` event.** That
  trigger is still declared and still looks sufficient, and is not: the release
  is created by a workflow using `GITHUB_TOKEN`, and GitHub deliberately does
  not start workflows from events raised by that token. v0.1.0 shipped with the
  cask untouched for exactly this reason, and nothing failed to say so.
- **It authenticates with a GitHub App, not a PAT.** `GITHUB_TOKEN` cannot push
  to another repository, and a fine-grained PAT expires — a year later a release
  would publish, this job would fail, and `brew install` would quietly keep
  serving the previous version until somebody complained.

**Production tags only.** A `-dev-` or `-rc-` build is not something anybody
should get from `brew install`, and the job exits early on one.

**Homebrew is pull, not push.** There is no daemon: a user who never runs
`brew upgrade` is never told anything. That is the gap the in-app update check
addresses, and it is why the cask being current is necessary but not sufficient.
Linux and Windows have no package-manager channel at all — they are
download-and-unzip, which is why the release notes and the checksums matter more
there.

**A notarisation failure is usually Apple, and is retried.** `build/notarise.sh`
wraps both submissions: it retries when Apple does not answer — a gateway
timeout, a queue that never drains — and does NOT retry when Apple has looked
at the binary and said no, because another half hour arrives at the same
answer with the reason buried under three copies of itself. The distinction
exists because a release makes two submissions now, so it is twice as exposed
to a service that returned a bare `504` fifteen seconds after accepting a
submission with the same credentials.

**macOS publishes two assets, and neither is spare.** The `.dmg` is what
podsteer.com links from its download button, because a zip unpacks to a `.app`
in `~/Downloads` that most people then run from there. The `.zip` is what the
cask job fetches **by exact name** to compute its checksum — so removing it to
promote the image would leave `brew install --cask podsteer` pointing at an
asset that is not there, and it would fail on the upgrade path rather than at
release time. Both are signed, notarised and stapled; the image is assessed
with `spctl --type open`, because `--type execute` reports a disk image as
rejected whatever its state.

One universal image, not one per architecture. Browsers on Apple Silicon
report `Intel Mac OS X` in the user-agent for compatibility, so a download page
cannot detect a visitor's chip — splitting would mean asking people to know
their own hardware, and getting it wrong produces an application that will not
launch.

After a production tag, verify rather than assume: the cask's `version` should
name the new release and its `sha256` should match the published asset. Both are
one `curl` away, and a silently stale cask is invisible from this repository.

&nbsp;

## Generated bindings are committed

`web/src/lib/bindings/` is generated by `wails3 generate bindings` from the
bound Go services, and it is **tracked in git**. That is a deliberate trade:

- Committed, a fresh clone type-checks and IDEs resolve imports without anyone
  having installed Go or the Wails CLI.
- The risk is staleness — change a DTO, forget to regenerate, and the frontend
  compiles against a contract the backend no longer honours. Nothing catches
  that until runtime.

The `bindings` CI job removes the risk: it regenerates and fails on any diff.
So run `make bindings` and commit the result whenever you change a bound method
signature or a DTO in `app/adapters/wails/dto.go`. Nothing else regenerates
them: under Wails v2 a build did it as a side effect, and under v3 it does not.

&nbsp;

## Code signing

The published macOS and Windows artefacts are **unsigned**. macOS will refuse
to open the app until it is cleared in System Settings → Privacy & Security,
and Windows SmartScreen will warn.

Signing needs an Apple Developer ID certificate and a Windows code-signing
certificate held as repository secrets, plus a notarisation step for macOS.

HOW the artefacts get signed is documented where it happens: the signing steps
in `.github/workflows/ci-cd.yaml` carry the reasoning behind every flag inline —
why the hardened runtime is required, why `ditto` rather than `zip`, why the
ticket is stapled, and why no entitlements are needed for a Wails application.
Read those before changing any of it.

WHICH certificates to obtain, from whom and at what cost is a procurement
question rather than an engineering one, and lives in the private
`podsteer/business-docs` repository. It used to be `docs/SIGNING.md` here; it
was moved on 2026-08-27 because vendor costs, lead times and a running list of
controls not yet in place are not a contributor's business.
That is a prerequisite for distributing to anyone outside the team.
