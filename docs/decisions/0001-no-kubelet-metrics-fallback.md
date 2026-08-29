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

## The wider question: replacing metrics-server entirely

Asked and answered on 2026-08-29, because "we rely on metrics-server rather a
lot" is the natural next thought and deserves recording rather than
rediscovering.

**Every comparable tool depends on the same API.** k9s, Headlamp, Aptakube and
`kubectl top` all read `metrics.k8s.io` and show nothing without it. Lens is
the only one that does more, and it goes the OPPOSITE way — offering to install
Prometheus into the cluster rather than collecting anything itself. Nobody
scrapes kubelets from the client. That unanimity is evidence, not inertia.

**The reason is architectural, not effort.** metrics-server scrapes each
kubelet's Summary API *directly*, from inside the cluster, deliberately
bypassing the API server so that metric collection adds no control-plane load.
A desktop client cannot do that: it is outside the cluster, so every kubelet
read must be proxied through the API server — the exact path metrics-server was
designed to avoid.

The cost also scales the wrong way. One request per node per refresh, at ten
seconds, is 3,000 proxied requests a minute on a 500-node cluster — **per
user**. Ten engineers with PodSteer open makes it 30,000. metrics-server does
that work once, for everybody, without touching the control plane. Building it
in means building a per-user, out-of-cluster metrics-server whose cost scales
with nodes × users instead of nodes.

Two further costs are permanent rather than one-off: `/stats/summary` is a
kubelet implementation detail rather than a versioned API — metrics-server
tracks its changes on our behalf, and the CRI-stats migration is what that
looks like — and any figure we compute ourselves becomes ours to defend when it
disagrees with `kubectl top`.

**Conclusion: the dependency stays.** It is the correct dependency, held for
the same reasons every peer holds it. The thing worth being better at is the
experience of not having it, which is what this decision built.

### The one piece worth reconsidering

NODE-level usage, as distinct from per-pod. `node.cpu.usageNanoCores` and
`node.memory.workingSetBytes` are already in the `/stats/summary` response that
`filesystems.go` fetches and parses today — so node usage would add no requests
we do not already make. Per-pod is where the cost lives; per-node is where a
good share of the value is. That is a small, self-contained assessment of its
own and has not been done.

### And what is explicitly NOT this decision

An IN-CLUSTER collector operated by PodSteer. Every argument above turns on the
client being outside the cluster; something running inside has none of those
problems. That is a different product with a different deployment model — and
it is the shape the planned server-side tier already has (see CLAUDE.md), where
it would also solve the thing metrics-server genuinely cannot: history beyond
the few seconds it retains, which is why the trend chart currently has to
caveat itself as covering only the time the application was open.

Filed under a different heading, not rejected.

## Uncertainties, recorded rather than resolved

- Which metrics-server version moved from `stats/summary` to
  `/metrics/resource` (approximately v0.6). Irrelevant to this decision;
  relevant if B is ever built.
- Whether `usageNanoCores` survives the CRI-stats migration on every runtime.
- Per-provider `nodes/proxy` behaviour on EKS, GKE and AKS.

None of the three changes the verdict. All three make B less predictable, which
is the argument against it.
