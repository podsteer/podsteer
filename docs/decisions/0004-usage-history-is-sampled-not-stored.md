# 4. Per-object usage history is sampled in memory, and Prometheus is pointed at rather than queried

Date: 2026-09-01

## Status

Accepted.

## Context

Kubernetes reports only the present. `metrics.k8s.io` serves one reading per
object with a `Timestamp` and a `Window` and takes no range parameter, so a
chart of anything needs a record somebody kept.

PodSteer keeps one: every list poll already carries usage for every row, and
`usageHistory.svelte.ts` retains it in memory so a drawer chart opens with a
shape rather than a blank frame. The window is a few minutes, configurable, and
dies with the process.

Two ways to make that window longer were considered.

## Writing 48 hours of per-object usage to disk

Rejected, on three grounds.

**It would falsify a claim we make in writing.** `SECURITY.md` and `CLAUDE.md`
both state exhaustively what reaches the disk, and `HistoryService` samples hold
capacity figures only — no object names. A file of per-pod series is a list of
every workload in every cluster somebody connects to, sitting at rest under
`os.UserConfigDir()`. Making that true would mean rewriting both documents, and
the reason they say what they say has not changed.

**The chart would be structurally misleading.** Coverage would be "whenever the
application happened to be open", so a gap conflates three different facts: the
app was closed, metrics were unavailable, and the pod did not exist. A 48-hour
axis with holes in it invites a conclusion about the hole.

**It duplicates the planned server-side tier.** Durable, continuous history is
the thing a service records, and `HistoryPort` already exists to be swapped for
one.

## Pointing at Prometheus instead

Most clusters that care about this already run a monitoring stack that has kept
months of the same figures. So `DiscoverMetricsBackend` looks for one — two
label-selectored `Service` lists, cached for thirty minutes — and the UI says so
under the charts whose window is short.

**It advises; it does not query.** PodSteer takes no reading from Prometheus,
and the note claims nothing about what the backend contains: a service listing
establishes that something named `prometheus-operated` exists, not that it
scrapes this cluster's kubelets or retains anything. Saying "this exists and
will have kept more" is defensible from what discovery actually knows. Drawing a
line from it would not be.

Querying it later is a separate decision, and a larger one. The API server's
service proxy makes the transport free — the same mechanism
`filesystems.go` uses for `nodes/proxy`, on the existing kubeconfig credential,
with no network route from the operator's machine and no second credential — but
correctness is the hard half: which recording rules exist, whether the retention
covers the range asked for, and what a PromQL error should look like in a
drawer.

## Consequences

- Finding nothing is the ordinary answer and is not an error. Most clusters run
  no Prometheus, and plenty of accounts may not list services across namespaces.
  Both produce a zero `MetricsBackend`, and neither appears in the overview's
  `Unavailable` list — a cluster with no monitoring stack is not degraded.
- A refusal is cached; a transport failure is not. An account that may not list
  services never will be able to, and retrying every poll writes denied requests
  into an audit log forever for a feature that is an offer.
- The selectors and service names in `prometheus.go` are a heuristic that will
  go stale, in the same way `domain/release.go`'s support table does. Guessing
  more widely is the wrong correction: a service called `metrics` may be
  anything, and a wrong match would have PodSteer naming somebody's application
  as their monitoring stack.
