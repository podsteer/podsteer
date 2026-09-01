# 5. The update check is opt-outable and tells GitHub nothing about you

Date: 2026-09-01

## Status

Accepted. **Reverses a commitment made in ADR-free prose in `README.md`,
`SECURITY.md` and `CLAUDE.md` for v0.1.0 and v0.1.1.**

## Context

Until v0.1.2 PodSteer promised, in four places, that it performed "no telemetry,
no update check" and "talks to your clusters and to nothing else". That promise
had a property worth naming: **it was verifiable by a stranger in ten seconds**.
`grep -rl net/http app/` returned one file, the Kubernetes adapter. A reviewer
did not have to trust us.

Against that, a real cost. PodSteer can delete namespaces, exec into
containers, and apply edited manifests. A security fix only protects people who
install it, and the channels that would tell them are weaker than they look:

- **Homebrew is pull, not push.** Nothing runs unless somebody types
  `brew upgrade`. A user who never does is never told.
- **Linux and Windows have no package manager in the picture at all.** They are
  download-and-unzip.
- A disclosure policy with no notification channel is half a policy.

## Alternatives considered

**A PodSteer-operated endpoint** (`updates.podsteer.com`), which was the first
suggestion. **Rejected, and it is the worst option, not the safest.** It would
put every user's IP and launch schedule in logs we control — a dataset to hold,
to correlate with the planned server-side paid tier, and to be compelled to
produce. It is also the documented path by which an update channel becomes a
licence check: Lens fuses "check for software updates" with "validate Lens
Subscription… prevent unauthorized use", and that traffic survives their
telemetry opt-out. Insomnia's telemetry toggle is `disabled={isLoggedIn}`.
Asking GitHub keeps that door shut by construction, because **we receive
nothing**.

**Not building it**, and shipping only the local alternatives: version and a
releases link, and a local "what's new" after an upgrade. This remains the
purist position and was the researched recommendation. It was rejected because
those alternatives inform only people who go looking, which is not who an
unpatched cluster-admin client is dangerous for.

## Decision

Build it, on the terms Headlamp (CNCF Sandbox) and krew (Kubernetes SIG CLI)
already ship — both of which do this cleanly, on by default, disableable and
documented — and with the failure modes k9s demonstrates avoided by name.

- **Stateless GET to `api.github.com`, with no identifier.** No version, no
  platform, no machine id, no query string. GitHub *requires* a `User-Agent`
  and refuses requests without one, so `podsteer` is sent — deliberately not
  `podsteer/v0.1.1`, which would make each check a report of what is installed.
  Comparison happens locally on the answer.
- **On by default, off in one click.** Off by default would mean almost nobody
  learns a security fix shipped, which is the whole point of the feature.
- **`PODSTEER_UPDATE_CHECK=false` overrides the setting machine-wide**, for
  packagers who ship their own update path and for deny-by-default egress
  policies. An unparseable value reads as `false`: a typo in a variable set to
  suppress a network call must not restore it.
- **Off means no request, and that is TESTED on both sides.** The opt-out has
  silently broken in k9s (#3394), Terraform (#15943, open for years), dotnet,
  JetBrains and Docker Desktop. `TestDisablingSuppressesTheRequestEntirely`
  counts calls to the source rather than asserting the returned state, and the
  frontend has the equivalent.
- **Never on the startup path.** k9s #932 made the application *unstartable*
  when `api.github.com` was unreachable. The first check is a minute after
  launch, and nothing waits on it.
- **Failures are cached** for four hours. k9s caches successes only, so a
  machine behind a firewall retries continuously — its users describe it as
  "it tries hard every other second".
- **Four states, three of them silent.** Only "available" shows anything.
  "unknown" is the ordinary condition on an airgapped machine, or behind a NAT
  that has spent GitHub's 60-per-hour anonymous budget, and must never read as
  a fault.
- **PodSteer does not install anything.** The badge opens the release page.
  Self-updating is a far larger promise than this feature makes.

## Consequences

- **The grep-verifiable claim is gone, permanently.** `app/adapters/updates` is
  now a second `net/http` importer. Nothing restores the old property; the most
  that can be said is that the code is small, isolated in one package, and says
  what it does at the top of the file.
- **`SECURITY.md`, `README.md` and `CLAUDE.md` were rewritten in the same
  commit as the code.** A promise reversed quietly is the thing that costs
  projects their community — Audacity's telemetry PR drew 1,091 comments and
  was never merged; Lens's account requirement produced Freelens. The reversal
  being deliberate and documented is the whole mitigation.
- **The rate limit means this is unreliable at enterprise scale**, and that is
  accepted rather than solved. 60 requests an hour per IP is shared across
  everyone behind a corporate NAT. It degrades to "unknown", which is silent.
- **If a paid tier ever wants a client call, it does not get to reuse this
  one.** That is the precise failure this ADR exists to make visible later.
