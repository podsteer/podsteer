# 1. No kubelet fallback for CPU and memory

Decided 2026-08-29. Status: **accepted**.

## Context

On a cluster with no metrics-server, every CPU and memory figure renders as a
dash. A new cluster therefore looks indistinguishable from a broken
application — which is exactly how this came up: a fresh cluster, no
metrics-server, and no way to tell which of the two was wrong.

Two responses were weighed:

- **A. Explain.** Detect that no metrics API is served and say so, naming
  metrics-server.
- **B. Fall back.** Read usage from the kubelets ourselves when metrics-server
  is absent.

## The fallback is technically viable, and cheaper than expected

Measured against a real cluster on 2026-08-29 (self-managed, v1.36.4, four
nodes, metrics-server confirmed absent — `metrics.k8s.io` not even registered):

`/api/v1/nodes/<node>/proxy/stats/summary` returns HTTP 200 carrying
`node.cpu.usageNanoCores`, `node.memory.workingSetBytes`, and the same two per
pod. Three properties make it more attractive than a DIY metrics pipeline
usually is:

- **Cost is O(nodes), not O(pods).** One call per node returns every pod on it.
- **`usageNanoCores` is already a rate.** The kubelet has done the
  differentiation, so there are no cumulative counters, no sampling window to
  get wrong, and no useless first sample.
- **It is the same source metrics-server scrapes**, so the numbers are not an
  approximation of `kubectl top` — they are one hop upstream of it.

**And PodSteer already does this.** `app/adapters/k8s/filesystems.go` reads that
exact endpoint through the same proxy for node disk occupancy: same permission,
same per-node fan-out, same bounded concurrency, same treatment of denial as
ordinary. A fallback would be additional fields out of a response already being
fetched, not a new adapter.

## Decision

**Do A. Do not do B.**

The decisive argument is that **the reported problem is an explanation gap, not
a data gap.** A fallback does not close it. When `nodes/proxy` is denied — and
this codebase already assumes that is common; `filesystems.go` says so in its
own doc comment — the operator is back to unexplained dashes, now arriving
unpredictably: usage on some clusters, nothing on others, and no way to tell
why. Inconsistent absence is worse than consistent absence.

Three supporting reasons:

- **Provenance.** The project's standard is that a figure on screen is a claim.
  Kubelet readings are not identical to metrics-server's — different windowing,
  and working-set is not what every reader assumes — so showing them silently
  would present one measurement as another. Labelling them honestly means
  building the label too, which is more work than the fallback itself.
- **The cache does not transfer.** `filesystemTTL` is a minute, deliberately,
  because disks do not fill in ten seconds. CPU does. At a ten-second refresh
  the same sweep costs roughly six times more — on a fifty-node cluster, 300
  kubelet requests a minute instead of 50. The existing cache exists precisely
  because that sweep is the expensive part.
- **Sequencing.** A is small and closes the actual complaint. B is a second
  usage source to keep correct forever, for clusters that mostly could install
  metrics-server instead.

## What was built

- `domain.MetricsStatus` on the overview: `measured`, `not-installed`,
  `forbidden`, `failed`. One field, not a state threaded through every list and
  DTO — the distinction already existed in the port sentinels, and this is only
  where it becomes readable.
- The overview names **metrics-server** and gives the two causes different
  sentences. "Not installed" and "not allowed to read it" need opposite advice;
  telling the second person to install it sends them to argue with an
  administrator about software that is already running.
- The Pods list uses the `hasMetrics` flag it was already given. A dash there
  had meant *no metrics source*, *not loaded yet* and *genuinely idle* at once,
  and the Nodes list had already been doing this correctly — the two views
  disagreed.

## What would reverse this

Any of:

- Evidence that `nodes/proxy` is broadly granted where metrics-server is
  absent. The distribution is currently unknown; the claim that managed
  providers block the endpoint is **unverified**, and the likelier story is
  that restricted roles simply omit the permission. One EKS or GKE cluster
  would settle it.
- Users asking for it. Nobody has.
- A cluster class where metrics-server genuinely cannot be installed but
  `nodes/proxy` is available.

If it is reversed, the shape is already clear: extend the existing
`filesystems.go` sweep rather than adding an adapter, give it its own shorter
TTL, and label the figures as kubelet-sourced rather than passing them off as
`kubectl top` equivalents.

## Uncertainties, recorded rather than resolved

- Which metrics-server version moved from `stats/summary` to
  `/metrics/resource` (approximately v0.6). Irrelevant to this decision;
  relevant if B is ever built.
- Whether `usageNanoCores` survives the CRI-stats migration on every runtime.
- Per-provider `nodes/proxy` behaviour on EKS, GKE and AKS.

None of the three changes the verdict. All three make B less predictable, which
is the argument against it.
