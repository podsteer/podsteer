# PodSteer

A desktop Kubernetes client built on Wails v2 (Go backend + the OS's native
webview) rather than Electron, so that it starts fast and stays small in
memory.

## Layout

Two halves, and the split is enforced rather than conventional:

- `app/` — **all** Go code.
- `web/` — **all** frontend source (Svelte 5 + Vite + Tailwind v4).

```text
app/
├── cmd/            composition root; the only place dependencies are built
├── domain/         entities, value objects, domain events — stdlib only
├── application/    use cases orchestrating the domain
├── ports/          inbound (driving) + outbound (driven) interfaces
├── adapters/
│   ├── k8s/        client-go + cli-runtime; satisfies the Kubernetes ports
│   ├── wails/      bound structs and DTOs; the frontend API contract
│   ├── mcp/        read-only tools for a coding agent, over stdio
│   └── assets/     embeds the built frontend
└── config/         environment-driven configuration
```

## Multi-cluster is the core assumption

PodSteer holds several clusters open at once — one per tab — so **no port or
service has a notion of "the current cluster"**. Every call takes a
`domain.ClusterID`. `application.Registry` tracks what is open (in connection
order, which is the tab order); the frontend's `workspace.svelte.ts` mirrors it
and `session.svelte.ts` holds one tab's state.

If you find yourself wanting an "active cluster" in the backend, that is the
signal you are about to break tabs.

**The registry also carries a per-cluster read-only policy, set entirely by
the client.** An operator marks a group read-only in OrganiseDialog; the
frontend calls `ClusterAPI.SetReadOnly` right after Connect succeeds and again
whenever the group setting or the cluster's group changes; `Registry.SetReadOnly`
remembers the flag until `Close` clears it. `ManagementService` checks it
before every write and returns `ports.ErrReadOnly`, and `TerminalAPI.StartSession`
checks it before opening a shell. **This is a guard against the frontend's own
bugs, never a security boundary** — the flag lives in this process's memory,
not in the cluster, so it cannot be what stands between an account and a
write it is otherwise credentialed to make. The backend enforces it anyway
because the frontend disabling a button is one code path, and a stray context
menu, a stale cache, or a future control that forgets to check the group's
setting is exactly the class of bug a client-only guard cannot catch. RBAC —
set on the cluster, not on this machine — is the only thing that actually
decides what a write may do; see SECURITY.md, "What PodSteer can do".

## Two tiers of resource support

The navigator has to cover far more kinds than anyone can hand-model, so there
are deliberately two paths:

- **Rich kinds** — Pod, Node, the six workload controllers, Namespace, Event.
  Purpose-built domain entities, derived status, chosen columns. Listed in
  `domain/catalog.go` with `Rich: true`.
- **Everything else** — served by `ResourcePort.ListTable`, which asks the API
  server to print the objects as a table (the same mechanism behind
  `kubectl get`). Columns come from the server, so a freshly installed
  operator's CRDs are browsable with no code written for them.

The **cluster overview is neither tier**: it is an assessment, not a list, so
it is deliberately absent from `domain/catalog.go` and the frontend pins it
above the categories under the id `podsteer/overview`. Putting it in the catalog
would offer it to every consumer that expects to be able to GET what it names.

`domain/catalog.go` is the single source of truth for the navigator. Adding a
section to the UI is an entry there, not a frontend change. Custom resources
are appended per cluster by `DiscoverCustomKinds` — never globally, because two
clusters run different operators.

**Applying a manifest reads the same way: any kind, not a fixed list.**
`Adapter.UpdateResource` (`app/adapters/k8s/apply.go`) goes through the
DYNAMIC client rather than a typed clientset switch, so a CRD applies exactly
like a Deployment. The kind is resolved to its REST resource and scope by a
`meta.RESTMapper` built from discovery, cached per cluster and rebuilt
EXACTLY ONCE when a lookup reports `meta.NoKindMatchError` — a CRD installed
a minute ago must apply without reconnecting the cluster, but re-querying
discovery on every apply of an ordinary built-in kind would erase the whole
point of caching it. The write is optimistic-locked by the manifest's OWN
`resourceVersion`: present, it is sent as a PUT the API server enforces the
lock on, and a stale one comes back as `ports.ErrConflict` — reload the
object and re-apply the edit, never retry the same request; absent, the
object is created, and an `AlreadyExists` on that create falls back to
fetching the live `resourceVersion` and replacing the object with it (full
replace semantics, matching what pasting a whole manifest over an existing
object means by Apply). Validation is a SERVER-SIDE dry run
(`metav1.DryRunAll`), not a client-side diff: `ManagementAPI.ValidateResource`
sends the same manifest through the same path with nothing persisted, so the
API server's own admission chain — schema validation, webhooks — is what
answers, and that answer is shown close to verbatim
(`ports.ErrManifestRejected`) rather than paraphrased.

## The watch is an optimisation; polling is the truth

`app/adapters/k8s/watch.go` mirrors a cluster's pods locally so a refresh reads
memory instead of the network. Everything about it follows from one rule: **a
read is never blocked on the store**. The path that answers when the store is
not ready is the path that answered before the store existed, so there is no
sync timeout to tune, no first-refresh stall, and the worst it can do is not
help.

That rule is also what makes RBAC tractable. An account scoped to one
namespace cannot list pods cluster-wide; an account with a list/get Role
cannot watch at all. Both are ordinary, both surface as a 403 on the
reflector's first call, and both end the same way — the store is marked
degraded, one debug line is logged, and reads carry on down the path they were
using anyway. There are no per-namespace informers and no permission probe:
the cost of finding out is one refused request per connection.

Three things to know before touching it:

- **Anything rendering a pod TEMPLATE must read the object's own manifest,
  never the watch store.** A ReplicaSet in the store has had its template
  stripped to the container images — that is most of why watching it is worth
  anything — so a panel sourcing a template from there would show a container
  with no environment, no volumes and no probes, and would be right about the
  images. The drawer already fetches the full manifest; use that.
- **Every transform has a contract test, and they are not optional.** The
  stores hold stripped objects, so anything a mapper reads that its transform
  removes goes quietly blank on clusters where the watch happens to be serving
  — and stays correct everywhere else, which is the worst way to find out. It
  has already caught one: probes. Extend a mapper, and the matching
  `TestStripping…ChangesNothing…` fails.
- **A set is never reused.** `Invalidate` cancels and *waits*, and a reconnect
  builds a fresh set against the fresh client, so a reflector from before a
  disconnect cannot write into a store a later read answers from. The client
  is invalidated FIRST and the watch forgotten second; reversing that opens a
  window in which a racing read ensures a set against the stale client and the
  reconnect keeps it.
- **A transient error demotes a store, and a supervisor promotes it back.**
  There is no "the watch recovered" callback in client-go, so recovery is
  detected from the reflector's last synced resource version moving past the
  one recorded when the error arrived — evidence, never a timer. A cluster too
  quiet to advance it stays on the network path, which costs a cluster that
  quiet nothing. Promotion is a compare-and-swap in both places, because an
  account with `list` and without `watch` can otherwise have its condemnation
  overwritten by the sync that preceded it.
- **The read cache stays in front of the store.** Reading it is free on the
  wire and not free in CPU — five thousand pods mapped into domain values —
  and the assessment and the open list still want them in the same instant.

**Three kinds, and only three**: pods, ReplicaSets and Jobs — see
`watchedKinds`. Pods are the great majority of what a poll transfers. The
other two are the intermediates a controller's usage is attributed through, so
the Deployment and CronJob pages read them on every refresh, and a ReplicaSet
carries a whole pod template of which the mapper reads one field: on a
201-pod cluster measured here there were 186 ReplicaSets, which is not the
intuition anybody starts with. Their templates are therefore stripped to their
images.

Nothing else. Deployments, StatefulSets, DaemonSets, CronJobs, namespaces and
nodes are small lists already coalesced by `readcache.go`. Events are
high-churn and the store would hold the churn. Metrics can never be watched at
all — `metrics.k8s.io` serves no watch verb — which sets the floor on what a
refresh can cost.

**A set nobody reads is stopped** after `idleAfter`, and the next read starts a
fresh one exactly as the first did. That is what keeps "manual only" honest:
somebody who chose to stop talking to their cluster should not have a stream
open between button presses.

On by default; `PODSTEER_LIVE_WATCH=false` is the way back, and it is not an
approximation of the old behaviour — it is the same code path. One consequence
worth knowing: a watch is a background stream, so an operator who set refresh
to "manual only" still has one open once they have read a list. It carries
only changes and is strictly less traffic than the polling it replaces, but it
is a connection they did not press a button for.

`live_test.go` exercises the real streaming list against the current
kubeconfig context, which the fake clientset cannot — every other test in the
package pins the fallback.

## The polled lists are coalesced, and it is a singleflight not a cache

Every refresh fires the assessment AND the open list at the same instant, and
they overlap: on the namespace list both want every pod in the cluster; on a
controller list both want that kind, and the consumption sums want the
namespace's pods and metrics. Two identical requests leaving together is not a
caching problem — it is the same request twice.

`readcache.go` wraps the whole-collection reads a poll repeats — `ListPods`,
`ListWorkloads`, `ListNamespaces`, `ListNodes`, `PodMetrics`, `NodeMetrics`.
Identical reads in flight share one answer; a repeat within `readTTL` reuses
the last.

Three rules it holds to, each with a test:

- **The window is shorter than the fastest refresh the application offers**
  (5s), so it can never serve one tick's data to the next. It collapses a
  pile-up inside one tick and nothing more. This is the opposite trade from
  `filesystemCache` (a minute) and `backendCache` (a day), which hold answers
  because those questions move slowly.
- **A failure is never reused.** Handing the same refusal to every caller for
  two seconds turns one denied read into a pane that stays broken after the
  permission is granted.
- **The shared fetch is detached from whoever started it.** Whoever arrives
  first runs it and everyone else waits, so running it on that caller's
  context makes one caller's cancellation everybody's —
  `ListNamespaceSummaries` runs the namespace list and the cluster-wide pod
  list under one errgroup, and on an account without `list namespaces` the
  403 killed a pod list that account was permitted. The DEADLINE is kept and
  the cancellation is not: how long an answer is worth waiting for is as true
  for the second caller as the first. Waiters leave on their own context, so
  a wedged fetch cannot pin them.
- **Every write drops the cluster's cached reads** (`forgetReads`, deferred on
  entry in each `ManagementPort` method). Deleting a pod and then being handed
  the list that still contains it reads as the application ignoring what it
  was told.

Narrowed reads — one object, one node's pods, one workload's pods — go
straight through. Nothing on-demand is cached.

## Custom columns quote metadata, and annotations travel by projection

An operator can put any label or annotation key on any list as a column
(`web/src/lib/customColumns.ts`, persisted per KIND in
`preferences.svelte.ts` — a catalogue id and a key, never an object name).
Labels ship on every row of every kind, rich and generic alike. **Annotations
do not: only the keys somebody has put on a column travel**, passed as a
`domain.Projection` through every list call — `ListPods`, `ListWorkloads`,
`ListNodes`, `ListNamespaces`, `ListEvents`, `ListTable` — and the empty
projection is what every non-list caller (the assessment, the sampler, the
consumption sums) passes. The reason is one key: kubectl's
`last-applied-configuration` is a copy of the whole manifest, tens of
kilobytes on a Deployment, and shipping the map wholesale would re-send it on
every row of every refresh. That key is refused outright by `NewProjection`,
and not only for its size — the watch store strips it, so a column of it
would read blank on a cluster the watch is serving and the manifest on one it
is not, two answers decided by something the operator cannot see.

Two consequences. **The projection is part of the read-cache key**: a mapped
pod carries only what it was asked for, so a list view with an annotation
column reads beside the assessment's list rather than sharing it — one extra
list per refresh, paid only by whoever configured such a column, and only CPU
where the watch is serving. And **the mappers take the projection as a
parameter** rather than reading it off the adapter, so the stripping contract
tests in `watch_test.go` can map a stored object and its original under the
same projection and compare them. The generic table reads both labels and the
projected annotations from the `PartialObjectMetadata` the server already
attaches to each row (`includeObject=Metadata`) — never a GET per row.

## Counting is `limit=1`, never `len(list)`

Kubernetes has no endpoint that reports how many objects a namespace holds, and
two places here need one: the namespace list's Pods column and the namespace
panel's Contents section.

`ResourcePort.CountResources` asks for **one** object and reads
`metadata.remainingItemCount` — the server's own count of what it did not send.
One request, constant payload, and no Secret's contents cross the wire to
arrive at a number. `len(ListTable(...))` would be all three of those things
wrong, and would silently cap at `tableListLimit`.

Two rules that fall out of it, both tested:

- **A refused count is not zero.** An account with `list pods` and without
  `list secrets` must be told the Secrets count is unknown. `ResourceCount`
  carries `Unreadable` for that, and the UI renders it *instead of* a number.
- **The total is of built-in kinds.** Custom resources are excluded by
  `domain.CountableKinds` because their number is unbounded — a cluster with
  200 CRDs would make one panel 200 requests — so anything showing the total
  says what it counted.

The namespace LIST is different again: it counts pods by listing them
cluster-wide once (`ClusterService.ListNamespaceSummaries`), because a count
per namespace would be one request per row. That is the same cost the pod list
already pays with the namespace filter on "all", and it is why
`ListNamespaces` — which feeds the filter — stays a cheap read of names and is
a separate call.

## A pod belongs to the controller that OWNS it, not the one that selects it

Attribution — which pods count towards a Deployment's CPU meter — goes through
the pod's controlling `ownerReference`, resolved one hop where Kubernetes puts
an intermediate: a Deployment's pods are owned by its ReplicaSets, a CronJob's
by its Jobs. `domain.WorkloadConsumption` is the one rule, and both the list
row and the detail panel go through it so they cannot disagree.

It was briefly done by matching the controller's label selector instead, and
that is worth recording as a mistake rather than rediscovering:

- **A selector is not ownership.** Two controllers with overlapping selectors
  are each charged the whole shared set, so a column sums to more than the
  namespace it is in; a bare ReplicaSet wearing a Deployment's labels is
  charged to the Deployment; an orphaned pod is charged to whatever still
  matches it.
- **The selector reaching the domain is lossy.** `matchLabels` in the k8s
  mapper keeps `spec.selector.matchLabels` and drops `matchExpressions`, which
  is fine for display and fatal for attribution: a controller selecting purely
  by expression arrives with an EMPTY selector and is charged nothing, so a
  healthy workload reports zero pods.
- **It saved nothing.** The argument was that the owner chain costs a
  ReplicaSet list per refresh — while the same refresh already lists every POD
  in the namespace, an order of magnitude more.

A pod nothing in the list owns is charged to **nobody**. Attributing an
unattributable pod to something that merely looks similar reports usage
against a thing that is not causing it.

## "Nothing measured" and "no metrics API" are different, and both get said

`domain.AggregateUsage` carries `Measured` (how many pods reported), and
`MetricsAvailable` (whether the cluster served metrics at all). Collapsing
them tells somebody with a working metrics-server to install one, every time a
CronJob's pods finish or a Deployment scales to zero.

`Measurable` is the third: metrics-server never reports a finished or
unscheduled pod, so it is the denominator for "is this total short". Measuring
against every pod made any namespace holding a completed Job permanently
"partial" while claiming a total that was not short at all.

## The overview is analysis, and it lives in the domain

`app/domain/overview.go` turns a cluster snapshot into a verdict: grouped
findings, capacity, inventory. It is a pure function — no I/O, no clock, no
ordering dependence — which is why its rules are argued over in
`overview_test.go` rather than observed in production. `OverviewService` only
gathers the snapshot, concurrently, letting each source fail on its own.

Three things there are easy to get wrong and are already handled:

- **Requests, not usage, decide what schedules.** A cluster can refuse pods
  while every usage gauge looks calm. `ResourceUsage` carries allocatable,
  requests, limits and usage separately for exactly this reason.
- **`Usage` is measured across nodes; `PodUsage` across pods.** Efficiency uses
  the latter — dividing node usage (which includes the kubelet, the runtime and
  the OS) by pod requests reports clusters as over 100% efficient.
- **A pod's controller is the ReplicaSet, not the Deployment.** `ownerIndex`
  resolves the one hop, which is why the overview lists ReplicaSets and Jobs it
  never displays. Without them, findings are labelled with generated hashes and
  cannot be matched to the workload they belong to.

Terminal pods are excluded from every capacity total (`Pod.OccupiesNode`), and
cordoned nodes contribute no pod slots. Both are the standard way to produce a
utilisation figure that is quietly wrong.

Metrics are optional by design: `ports.ErrMetricsUnavailable` is an ordinary
condition, not a fault, and every list must render without metrics-server.

**And the UI must say WHY they are absent.** `domain.MetricsStatus` on the
overview separates "no metrics-server installed" (404/503) from "not permitted
to read it" (403) from a transient failure, because those need opposite advice
— telling somebody to install metrics-server when it is already running and
merely unreadable sends them to argue with an administrator. Reading a dash and
being unable to tell an unconfigured cluster from a broken application is the
bug that put this here.

A kubelet fallback for CPU and memory was considered and **deliberately not
built**, even though `filesystems.go` already proves the mechanism works: it
would need `nodes/proxy` on every node, report a different measurement from
metrics-server under the same column heading, and turn one absent add-on into
two code paths that disagree. Do not add one without a decision recorded in
`podsteer/business-docs`.

Three sources beyond metrics-server behave the same way and are worth knowing
about before adding a fourth:

- **Node disk occupancy comes from the kubelets**, not from any aggregated API
  — `app/adapters/k8s/filesystems.go` reads `/stats/summary` through the API
  server's node proxy. It needs the `nodes/proxy` permission, which plenty of
  clusters do not grant, so it degrades into `Unavailable` under its own name
  rather than under "metrics". It is one request per node, hence bounded
  concurrency and a one-minute cache; a partial answer is a success.
- **Kubernetes support windows are a hand-compiled table** in
  `app/domain/release.go`. It goes stale by construction, so a release it does
  not cover is reported as `SupportUnknown` and produces nothing. Never make an
  unknown version default to unsupported.
- **API deprecations and removals are a second hand-compiled table**, in
  `app/domain/deprecations.go`, transcribed from the Kubernetes deprecated API
  migration guide rather than typed out from memory — the file comment names
  which entries it could confirm and which it deliberately left out because
  the guide did not state them. `UpgradeImpact` matches a cluster's served
  group/versions against it, and those come from **discovery's own group
  list** (`Adapter.ServedAPIs`), never from `domain/catalog.go`: the catalogue
  holds only the CURRENT version PodSteer targets for each kind, so it can
  never contain a deprecated one for this table to match against. It goes
  stale the same way the support table does: an API this table does not cover
  produces no finding, never a guess, and a CRD can never match because
  Kubernetes reserves its own API groups.
  **An object does not use an API version — a WRITER does.** Kubernetes
  stores one copy of an object and serves it through every version the API
  server offers, so a `limit=1` count under a deprecated version would equal
  the count under its replacement and could never distinguish them — an
  earlier version of this feature counted objects this way and it would have
  marked every 1.29–1.31 cluster critical for its own default FlowSchemas,
  which every such cluster carries and nobody wrote. What actually breaks at
  removal is whoever keeps WRITING through the old version, which Kubernetes
  already records per object in `metadata.managedFields[].apiVersion`.
  `Adapter.APIWriters` (`app/adapters/k8s/upgrade.go`) reads exactly that,
  through the metadata client (`k8s.io/client-go/metadata`) rather than the
  typed or dynamic ones — it lists `PartialObjectMetadata` only, names,
  labels and managedFields, never an object's body or a Secret's contents —
  bounded to `apiWriterScanLimit` (2000) objects so a busy old cluster's
  Events cannot turn into a full scan, and cached for five minutes
  (`upgradeCacheTTL`) with a refusal (403/401) cached too, the same discipline
  `DiscoverMetricsBackend` follows and for the same reason: an account that
  may never list something should not have that retried into its audit log
  every poll.
  An object annotated `apf.kubernetes.io/autoupdate-spec=true` is the control
  plane's own — a default FlowSchema or PriorityLevelConfiguration it
  bootstraps and keeps current — and keeps a stale `managedFields` entry from
  an OLDER producer for a while after an upgrade even once the running
  producer has moved on. That exclusion is made in the domain
  (`operatorWriters`, `deprecations.go`), never filtered out in the adapter:
  the adapter reports every writer it finds, annotation included, so the
  exclusion is a rule `deprecations_test.go` can argue with rather than a
  silent drop nothing tests.
- **A monitoring stack already in the cluster is discovered, and only pointed
  at** — `app/adapters/k8s/prometheus.go` lists Services by two label selectors
  and ranks the matches by name, because a kube-prometheus-stack install
  returns several and only one answers PromQL. Finding nothing is the ordinary
  answer and never reaches `Unavailable`: a cluster with no Prometheus is not a
  degraded cluster. **PodSteer does not query it, and advice is the whole
  feature**: a service listing establishes that something named
  `prometheus-operated` exists, not that it scrapes this cluster or retains
  anything, so the note claims only the former. Per-object usage is not written
  to disk either — the recorded cluster history deliberately carries no object
  names, and a file of per-pod series would reverse that.

Dependencies point inward. `app/domain` and `app/ports` import nothing outside
the standard library; if either ever needs `client-go`, something has been
wired backwards.

Per-workload sizing rules — which workload is over-reserved, throttled at its
CPU limit, or over its memory request — live in `app/domain/sizing.go`, and
deliberately reuse `wasteRatio` from the cluster-wide `capacity:waste` finding
above rather than declaring their own, so the cluster verdict and the
workload verdict cannot disagree.

## The pod pane assesses too, and there is a rule for where logic lives

`app/domain/pod_assessment.go` is the same idea at pod scope: a pure function
of one pod and a clock, returning ranked findings with advice. Its rules are
the differentiator — every other client in this category shows the fields and
leaves the conclusions to the reader.

**The line is quotation versus verdict, and it decides where code goes.** The
pod detail pane parses the manifest client-side, which is allowed for anything
that is a QUOTATION of what is already on screen in the YAML tab: a port
number, a mount path, a probe rendered into kubectl's own string. Anything
involving a comparison, a threshold or a conclusion goes through the domain,
where it can be argued with in a test. "Liveness delay is 30s" is frontend;
"this delay is shorter than the container's observed startup, which is why it
restarts" is domain.

Three rules there have subtleties worth not re-deriving:

- **The reason disambiguates an exit code, not the other way round.** 137 is
  SIGKILL and means OOMKilled only when the kubelet also said so; without that
  reason it is a grace period expiring. `domain/termination.go` has a test
  asserting we never send somebody to raise a memory limit for the second.
- **Image drift is grouped by ReplicaSet, never by Deployment.** A rollout is
  supposed to have two digests running, and those pods are in two ReplicaSets.
  Within one ReplicaSet the template is fixed, so two digests can only be a
  moved tag. StatefulSets and DaemonSets are excluded because they update in
  place.
- **A correctly configured pod produces no findings**, and a test asserts it. A
  panel that always has something to say is one people stop reading.

## The dependency map is three shapes, not one

`app/domain/graph.go` builds two of them and `app/domain/object_graph.go` the
third, and they are separate functions because the SUBJECT decides the
structure: a pod's map is a chain with the pod in the middle, a workload's is a
fan — one controller over however many pods it currently has — and any other
object's is a neighbourhood. Pretending they are one shape would mean a pod
field that is sometimes a list, and edges that mean different things depending
on which it was.

**EVERY EDGE IS A RELATIONSHIP KUBERNETES ACTUALLY HAS.** That rule is worth
stating on its own, because breaking it is always the convenient thing to do
and the map is used to reason about a cluster. A Service selects PODS and knows
nothing about what created them; a ReplicaSet does not read a Secret, it
carries a template declaring what the pods it creates will read. Both were once
drawn to the controller — one edge instead of one per replica — and both were
false. Connecting to the pods also surfaces the case the shortcut hid: a
Service reaching only SOME of a workload's pods, which is what a half-finished
rollout looks like.

The one exception is a workload with NO pods, where nothing is mounting
anything and the only true statement left is that the template declares them.
The edge from the subject means that, and only in that case.

Four more rules there are load-bearing and were each found the hard way:

- **An empty selector matches nothing.** In the Kubernetes API an empty
  selector on a Service means it has none at all — an ExternalName, or
  Endpoints managed by hand. Read as "matches everything" it draws an edge to
  every pod in the namespace.
- **Container boxes are keyed by pod.** Every replica runs containers with the
  same names, so keying on the name alone collapses three replicas' containers
  into one box with three edges into it.
- **One box per attached resource, however many pods read it.** Every replica
  mounts the same ConfigMap, so the node is added once and each pod draws an
  edge to it — three replicas must not produce three identical boxes.

**A CronJob does not own pods.** It owns Jobs and those own pods, so its map
carries the Job tier between them — anything else draws a relationship
Kubernetes does not have, and loses the only thing that says which run a pod
belongs to. `ListPodsForWorkload` finds them by **ownerReference, not by the
`job-name` label**: the label is just a label, and a pod relabelled by hand
would be claimed by a Job that never created it.

**Attached resources come from the workload's own template, never from a
running pod.** Sampling a pod is one read instead of a typed one per kind, and
wrong twice: a CronJob between runs and a Deployment scaled to zero have no pod
to sample, so their configuration did not appear at all; and a pod left from a
previous revision carries the OLD template. A CronJob nests one level deeper
than the rest — `spec.jobTemplate.spec.template.spec`.

**What counts as a dependency is wider than the volume list.** `attachedFromSpec`
reads volumes, **projected volume sources** (how most pods actually mount a CA
bundle beside a ConfigMap), `envFrom` and `valueFrom`, generic **ephemeral**
volumes, a CSI driver's `nodePublishSecretRef`, and **imagePullSecrets** — the
last being one of the few whose absence stops a pod before its own
configuration is read. Init containers count, because one that cannot find its
Secret stops the pod before the application starts. The `default` service
account is deliberately not drawn: every pod has one, so it would add a box to
every map that distinguishes nothing.

**Folding is a VIEW decision; the graph is always complete.** The backend emits
every pod, every container and every edge — a map that quietly omitted a
replica would be a map nobody could trust — and `web/src/lib/graphFold.ts`
collapses a sibling set larger than five into one box that stands for its
members, rewriting their edges onto it and deduplicating. Thirty pods reading
one ConfigMap draw one line instead of thirty; expanding puts them back.

Three rules keep folding honest, each with a test:

- **A folded set is unwell if any member is**, and says how many. Folding must
  never hide the thing somebody opened the map to find.
- **The resource count in the toolbar is the COMPLETE one**, never the drawn
  one, so a collapsed set cannot under-report a cluster.
- **Nothing is invented.** A group node IS its members; every edge on it is an
  edge one of them has, and the count is on the box so it never reads as one
  thing.

The domain marks sibling sets with `GraphNode.Group` — the workload that
manages a pod, the pod that runs a container. Attached resources have none:
they are shared between pods and belong to no single one.

**The layout is dagre's, and that is deliberate.** `web/src/lib/graphLayout.ts`
hand-rolled a layered layout and an orthogonal edge router for several
iterations, and every fix traded one geometric case for another — siblings
looping, a line through a box, a route arriving backwards. It is a hard,
well-studied problem; dagre is the port of what Graphviz's `dot` does, and
ArgoCD draws its own resource tree with it. What stays ours is the drawing: the
boxes, the Lucide icons, hover, pan and zoom, and the rounding of the corners.

**A followable node passes its Kubernetes `Kind` verbatim.** The drawer
resolves references against the navigator's catalogue, which is keyed by
`Kind` — so a lowercased plural matches nothing and the click silently does
nothing at all. That is what it did on every node of every kind until it was
noticed.

### The third shape is a neighbourhood, and it is offered on every kind

`NewObjectGraph` (`app/domain/object_graph.go`) draws anything the generic
table lists — a Service, a ConfigMap, a Secret, an Ingress, a PVC, a CRD
instance — with the subject in the middle, what its spec names below it and
what owns it above. It is a third function rather than a widening of the other
two for the reason the other two are separate: those two subjects have a
structure worth asserting, and an arbitrary object has none beyond "it names
some objects and some name it".

**It is still every-edge-is-real, and the rules are in the domain.**
`ReferencesFromManifest` (`app/domain/object_references.go`) is a pure function
over the DECODED MANIFEST — `map[string]any`, so no client-go and no Go type
for a CRD — that says which field of which kind names what: an Ingress's
`ingressClassName`, its backends (the default one included) and its
`tls[].secretName`; a PVC's `volumeName`, `storageClassName` and data source; a
PersistentVolume's `claimRef` (the one genuinely cross-namespace reference the
map meets) and class; a ServiceAccount's secrets and pull secrets; an HPA's
`scaleTargetRef`; a binding's `roleRef` and its ServiceAccount subjects, never
its User or Group ones, which are strings an authenticator produced rather than
objects. **A kind with no rule draws its owner chain and nothing else.**
Guessing at a field that merely looks like a reference — anything ending in
`Ref` — would draw lines out of a CRD's spec its author never meant as
references, which is the same class of mistake as reading a label as ownership.

**A reference that resolves to nothing is drawn, marked, and never dropped.**
`GraphNode.Missing` is its own field beside `Healthy`, because an object that
exists and is failing and one that was named and is absent call for opposite
next steps; the map outlines a missing box dashed, and `graphFold.ts` counts
missing members separately from unwell ones so a folded set says "3 not found"
rather than "3 not ready". **A refusal is not an absence**: a 403 resolving a
reference leaves the box unmarked and names the refusal in `Unreadable`, since
telling an account that may not read Secrets that the Secret does not exist
sends somebody to recreate an object that is sitting there.

### Downward navigation is bounded, and the panel says why

Upward is free and is taken: `ownerReferences` are already on the object, so
each hop is one GET of a name Kubernetes itself wrote. It is still capped at
`domain.ObjectOwnerDepth` (three) and terminates on a repeat keyed by UID —
Kubernetes does not forbid an ownerReference cycle, and an unbounded walk is a
drawer somebody else's object can make issue reads without limit. Outward is
capped too: `graphReferenceLimit` (12) distinct references, resolved four at a
time, with anything past the bound named rather than silently dropped.

**Downward is attempted only where Kubernetes gives a cheap answer**, which
today is one case: a Service's selector against one list of one kind in one
namespace, read through `readcache.go` so it coalesces with the tab's own poll.
Everything else somebody might want below an object — which pods mount this
ConfigMap, which PVCs use this StorageClass — has no server-side index at all,
and answering it would mean listing every kind in the namespace every time a
pane opened: a request per kind per open drawer, which is exactly the polling
storm the read-cache section above exists to prevent. So it is not done, and
`domain.DownwardBound` puts one short line in the panel instead.

**That line is `PodGraph.Bounded`, and it is NOT `Unreadable`.** Collapsing the
two would be the mistake this codebase keeps refusing to make elsewhere
(`MetricsStatus`, `ClusterReadStatus`): `Unreadable` means a read was attempted
and refused, so a permission would fix it; `Bounded` means no read was
attempted, deliberately, and no permission changes that. Empty space under an
object with neither line would read as "nothing depends on this", which is a
claim nothing here checked.

Nothing on this path runs on a refresh tick — `BrowseAPI.ObjectGraph` is called
when the pane opens and not again — because a neighbourhood changes when
somebody changes it, and redrawing a map under a reader is worse than it being
a few seconds stale.

## Secrets are read on request, never on render

The rules here are load-bearing, so read them before touching anything that
displays a Secret: nothing resolves a Secret
when a pane opens, because Kubernetes' own guidance tells cluster operators to
alert on exactly that pattern; `RevealSecretKey` returns one key and discards
the rest in the adapter; and a Secret's values in the YAML tab are replaced
with their decoded size before the object is serialised, because base64 is an
encoding and not a cipher. A Secret key can be WRITTEN the same way it is
read: `SetSecretKey` takes one key, on explicit request, only after that key
has been revealed in the pane doing the writing, and is audited the same way
— cluster, namespace, name and key, never the value — never through a read of
the whole object.

`InspectTLSSecret` follows the same rule for a TLS Secret's certificate: the
certificate itself is public material, but it lives beside the private key in
the same object, so it is parsed only on the same deliberate, per-Secret
request `RevealSecretKey` requires, never when the Secret pane opens.

## Files are copied by tar, and unpacked only in Go

Copying a file to or from a container (`FileCopyAPI`, from the pod drawer) is
`kubectl cp`'s mechanism and nothing more: the container runs
`tar cf - -C <dir> <base>` or `tar xf - -C <dir>` over a non-TTY exec session
(`Adapter.CopyFromPod`/`CopyToPod`, sharing `execCommand` with `ExecInPod`),
and `app/adapters/archive` packs or unpacks the local side. **The local side
is written by Go and never delegated to the webview**, and that is not an
implementation preference: the stream is produced by an image somebody else
built, and tar has carried path-traversal attacks for as long as it has
existed. `archive.Local.Extract` refuses absolute names, any `..` component
and any symlink whose target leaves the chosen directory, writes through
`os.Root` so a link already sitting in that directory cannot be followed out
of it either, drops setuid/setgid/sticky bits, and enforces
`domain.TransferLimits` as bytes arrive — each with a test that tries the
attack. A webview handed a path and a blob could check none of that, and
would have to be trusted, unauthenticated, to land content wherever it said.
`ArchivePort` is a port rather than a helper so `ManagementService`'s
orchestration — the pipe between exec and archive, the read-only refusal on
upload only, the one audit line per transfer, and `transferOutcome`'s rule
for which of two simultaneous failures to report — is tested against fakes
while the real archive is tested on a temp directory. A container without
`tar` is the ordinary failure and has its own sentinel (`ports.ErrTarMissing`)
and code (`tar_missing`); tar's stderr is never discarded — carried verbatim in
`ports.ErrCommandFailed` on failure, logged on success.

## Escape belongs to one layer, and the layers say which

Seventeen components listen for Escape on the window, so `stopPropagation`
between them is meaningless — they share a target, and nothing propagates.
`web/src/lib/escape.ts` holds a stack instead: each layer claims while it is
open and only the innermost claim acts. Add a claim to anything new that
closes on Escape, or it will close alongside whatever is underneath it — which
is how one keystroke aimed at a row menu used to close the drawer and discard
an unsaved YAML draft with it.

The dialogs are the other half of the same idea. `aria-modal` without a focus
trap is worse than neither: it tells assistive technology the background does
not exist while Tab walks straight into it. `use:modal` (`web/src/lib/modal.ts`)
is what makes the claim true — focus in, focus trapped, focus restored, and the
background marked `inert`. Anything carrying `aria-modal` must use it.

## The command palette searches memory first, and never fans out

⌘⇧P / ⌘P (`web/src/stores/palette.svelte.ts`, `web/src/lib/components/
CommandPalette.svelte`) is global search across kinds, objects and open
clusters, and it is bound by the same rule as the poll it sits beside: **a
keystroke must never turn into a cluster-wide LIST of every kind.** Every
group but one is built from data the application already has — the catalogue
and namespace list `buildCommands` (`web/src/lib/palette/commands.ts`) is
handed, the current view's own already-polled rows (pods, workloads, nodes,
namespaces, events — whichever `ClusterSession` is holding), and
`session.recentObjects`. None of that costs a request.

The one exception is a `kind:` pill — or typing a kind's own name followed by
a space, which is read the same way (see `web/src/lib/palette/parse.ts`) —
naming a kind OTHER than the one on screen. That is an explicit ask to search
something nothing has polled, and it costs exactly **one** `ListTable` read,
in the tab's current namespace, debounced behind the keyboard and cached for
as long as that palette instance stays open — never one call per keystroke,
never re-fetched for a scope already answered. Scoping to the kind already
displayed costs nothing at all: its rows are already in memory. A second open
cluster's objects are never fetched either; if its tab is open, that tab's own
poll is already the source, and the palette only ever reads what a tab already
has — it does not open one.

## The All-clusters view is a pseudo-entry, renders what answered, and polls only on screen

`podsteer/fleet` is the third pinned pseudo-entry beside the overview and
Applications, and for the same reason: it is every open tab's pods, workloads
or events in ONE table — an aggregation, not a kind — so it is deliberately
absent from `domain/catalog.go`. A catalogue entry is offered to every
consumer that expects to GET what it names, from one cluster, and no cluster
knows what the other tabs are. The fan-out lives in Go —
`application.FleetService`, bound as `FleetAPI` — so a tick is one bridge call
however many clusters are open. Each cluster is read through the same
`WorkloadService`/`EventService` call its own tab makes, so `readcache.go`
coalesces the two when they land together; at most `fleetConcurrency`
clusters are read at once; results follow the registry's tab order whatever
order the frontend named them in; and the only error is naming a cluster that
is not open. Workloads are every controller kind but ReplicaSet
(`domain.FleetWorkloadKinds`), one request per cluster per kind.

**Partial results are mandatory, not a nicety.** Every cluster answers for
itself: `domain.ClusterRead` carries its own `ClusterReadStatus`, modelled on
`MetricsStatus` and for the same reason — forbidden, unreachable and slow call
for opposite actions, and a merged table that showed the same nothing for all
three would send somebody to check a VPN over a permission problem. A cluster
still unanswered at `fleetReadBudget` is reported slow and NOT waited for; its
read carries on, freed of the bridge call's cancellation but not its deadline,
and what it eventually returns is handed to the next read of the same thing.
That is what makes "slow" true rather than permanent: a merely slow cluster's
rows arrive a tick late instead of never, and one that is down is reported
unreachable once its dial has timed out instead of slow for ever. The frontend
keeps a slow or unreachable cluster's last rows, marked stale in the status
strip, and drops a refused one's — stale rows under a "forbidden" mark would
claim a view the account does not have. Refusing the whole call because one
cluster refused is the bug this section exists to prevent.

**Nothing polls off screen.** `$stores/fleet` holds the rows — they are the
workspace's, not any one tab's, so switching tabs does not refetch them — and
has no timer of its own. `ClusterSession.#fetch` calls `fleet.refresh` only
when the session's own poll fires with the fleet view selected, and a session
polls only while its tab is in front; select another kind, or another tab,
and the fan-out simply stops. The command palette reads the merged rows the
way it reads any view's own: only while that view is on screen, never by
fetching across clusters for a keystroke.

## The RBAC explorer quotes the API server, and flags only what it can argue

`podsteer/rbac` is the fourth pinned pseudo-entry, beside the overview,
Applications and All clusters, and it is one for the same reason they are:
"what can this kubeconfig do here" is a question asked of the authorization
review APIs, not an object anything can GET, so it is deliberately absent from
`domain/catalog.go` — a catalogue entry is offered to every consumer that
expects to fetch what it names. Roles and ClusterRoles themselves are ordinary
catalogue entries under Access Control and stay exactly where they were; this
entry is the interrogation, not the list.

**The API server decides; PodSteer only flags.** Three of the four panes are
quotations. `SelfSubjectRulesReview` answers "what may I do in this namespace"
in ONE request — never one access check per verb per resource — and
`SelfSubjectAccessReview`/`SubjectAccessReview` answer one question each, with
`allowed`, `denied` and `reason` carried across verbatim by
`app/adapters/k8s/rbac.go` and rendered as they arrived. Nothing here
evaluates a rule to reach a verdict about somebody's permissions, and that is
not fastidiousness: RBAC is only one authorizer in a chain that may also hold
Node, ABAC and a webhook, and being subtly wrong about what an account may do
is worse than saying nothing. `allowed` and `denied` are BOTH carried because
they are not opposites — an authorizer with no opinion leaves both false, and
rendering that as a denial claims a verdict nothing gave.

The fourth pane is the one verdict, so it lives in the domain with a test per
rule: `domain.AssessRole` (`app/domain/rbac.go`) flags wildcard verbs,
resources or API groups; `escalate`, `bind` and `impersonate`; a cluster-scoped
Secret read; `create` on pods (whoever may create one names the service
account it runs as, and the kubelet mounts that account's token); and a
binding to `cluster-admin`. Each says what it permits and why it matters, the
same shape `pod_assessment.go` uses — **and a role with none of them produces
no findings, with a test asserting exactly that**. The severity of a wildcard
depends on scope, because the identical rule means one namespace or the whole
cluster. Two rules are narrower than they look, on purpose: a namespaced
Secret read is not flagged at all (it is how most workloads are configured),
and only `pods` counts for the pod-creation flag — a Deployment rule reaches
the same place by a longer route, but claiming so would be a statement about
controller behaviour rather than a reading of the rule on screen.

**Nothing here is cached and nothing here polls.** `ClusterSession`'s tick has
a case for this view that fetches nothing at all: an allow re-shown from a
previous tick, or served from a cache, would keep reading as granted after the
permission behind it was revoked. The reverse lookup is the expensive half —
a ClusterRole is referenced by ClusterRoleBindings and by RoleBindings in any
namespace, so `ListBindings` lists both cluster-wide rather than narrowing to
a namespace and reporting a widely granted role as bound to nobody — and it is
bounded by WHEN it runs: `InspectRole` is three requests, made when somebody
presses Inspect. Which bindings actually reference a role is
`domain.BindingsReferencing`, not the adapter's filter, because the cases are
worth arguing about (a RoleBinding may reference a ClusterRole; a RoleBinding
to a Role reaches only its own namespace's).

**Being refused is an ordinary answer, not a broken pane.** `domain.ReviewStatus`
is modelled on `MetricsStatus` and separates a refusal from an absence from a
transient failure, and each pane renders the sentence naming the permission
that would fix it — creating a `SubjectAccessReview` about somebody else is
privileged and most accounts cannot, which is a fact about the cluster rather
than a fault. The role read and the binding list carry SEPARATE statuses,
because an account routinely holds one and not the other and a single refusal
must not blank the half that answered. A subject's name is an object name: it
is typed into the panel and shown, and it is never written to disk — the same
no-object-names commitment SECURITY.md makes, which is why recent subjects, if
they are ever offered, belong in memory beside the navigator's Recent section.

## Two structural facts that look like mistakes

**`main.go` sits at the repository root.** The Wails CLI runs `go build` with
its working directory set to the project root and no package argument
(`pkg/commands/build/base.go`), and exposes no setting to point it elsewhere.
The root file is a three-line shim; the real entry point is `app/cmd/main.go`,
which is `package cmd` for the same reason.

That is also why `cmd.Main` reads its arguments the way it does. Wails
generates bindings by compiling and RUNNING this binary with no arguments of
its own, so **the argument-free path must always be the window** — a
subcommand is additive and is reached only when the first argument names it.
`route` is split out of `dispatch` precisely so `main_test.go` can assert that
without starting a window, which is the one thing a test of this path cannot
do. Anything that put a flag, a prompt or a usage message in front of a bare
launch would take `wails build`, `wails dev` and `make bindings` down with it.

**Vite builds into `app/adapters/assets/dist`.** Go's `//go:embed` cannot
reference a parent directory, so the bundle has to land beside the package that
embeds it. Frontend *source* stays in `web/`; only build output crosses over.
See `web/vite.config.ts`.

That directory's contents are git-ignored except a tracked `.gitkeep`, because
`//go:embed all:dist` will not compile if the directory is missing from a fresh
clone. `emptyOutDir` deletes the placeholder on every build, so a small Vite
plugin (`podsteer:keep-embed-directory`) rewrites it — do not remove it.

**The frontend must be built before any Wails command.** Wails generates its
bindings by compiling *and running* the application, and it does that before it
builds the frontend. `assets.FS()` refuses to start without an embedded bundle,
so on a clean checkout that first run exits 1 and takes `wails build`,
`wails dev` and `wails generate module` down with it. This is why `dev`, `build`
and `bindings` all depend on `web-build` in the Makefile, and why CI builds the
frontend before invoking Wails. `-s` then stops Wails repeating the work.

Do not "fix" this by softening the check in `app/adapters/assets/assets.go` —
it is what turns "compiled without a frontend" into a startup error instead of
a blank window nobody can diagnose.

## History is sampled, and says so

Kubernetes reports only the present: the metrics API has no notion of a series,
so a chart of anything needs a record somebody kept. `HistoryService` samples
each connected cluster every 30 seconds while the application runs and writes
0600 files under `os.UserConfigDir()/PodSteer/history`, never anywhere else.
That is `~/Library/Application Support/PodSteer` on macOS — **not**
`~/.config`, which is only where it lands on Linux, and which this file used to
claim unconditionally. Windows is `%AppData%\PodSteer`. The Homebrew cask's
`zap` list names the macOS paths and has to be kept in step.

That makes the coverage the window the app was open, which is weaker than a
monitoring stack and **must be presented as such** — `SeriesResult.spanSeconds`
exists so the UI can say "the last 40 minutes" instead of implying more.

- **The sampler is the only long-lived goroutine.** One owner, one way to stop
  (`Close`), and it waits for the write in flight before returning. It is
  started from `OnStartup` and stopped from `OnShutdown`.
- **Retention lives in Go, not in the UI preferences.** It governs what reaches
  the disk, so the process doing the writing owns it. Zero means record nothing
  *and* erase what exists — an operator choosing it means both.
- **A sample is derived from the overview**, not from a second read of the
  cluster, so the chart and the numbers above it can never disagree.
- Samples hold capacity figures only: no object names, no logs, no manifests.

## The settings file carries arrangements, never anything about a cluster's contents

Settings → Export & import writes the two localStorage stores — `preferences`
and `organisation` — as one pretty-printed JSON document an operator can keep
in git and hand to a colleague (`web/src/lib/settingsFile.ts`,
`SettingsTransfer.svelte`). JSON rather than YAML because the stores are
already JSON, so the document is a projection rather than a translation and no
value can change meaning on the way through — YAML's implicit typing would
read a group called `no` as false; because `JSON.parse` is the platform's, so
nothing but code in this repository stands between somebody's file and their
settings; and because the file exists to live in git, where two-space JSON
diffs one setting to a line. JSON has no comments, so the header is a
`_readme` array of strings at the top of the document.

**What must never travel is the whole point of the file, and it is the
no-object-names commitment SECURITY.md makes.** The export is an ALLOWLIST
built field by field in each store's `exportable()`, never a spread of the
persisted shape — a spread would carry whatever the shape grows next, and two
things it has already grown hold object names: `snoozes`, whose keys are a
finding id, a NAMESPACE and an OBJECT NAME, and `namespaceByCluster`. Both are
held back, along with the update check's machine state. No credential,
kubeconfig or cluster address is in either store, so none can reach the file.

**One cluster-shaped thing does travel: the kubeconfig CONTEXT NAME**, as the
key of `pinnedKinds` and of the organisation's placements — "staging is
read-only" cannot be said without naming staging. It is a handle the
recipient's own kubeconfig already gives them and it identifies nothing inside
any cluster, but a colleague reading the file will see which contexts a
teammate has, so the document's own header says so and the Settings pane says
so before the Export button rather than after.

`settingsFile.test.ts` is what keeps this true: it populates both stores with
every forbidden category — a snoozed pod and node, a namespace filter, a
server URL, a token — serialises, and asserts none of them appear; and it
asserts the exported key set against a LITERAL list, so a field carrying
object names cannot join the export without somebody editing that list and
arguing for it. Redaction that is only a comment is redaction that lapses.

Import is a REVIEW, not an overwrite: `previewImport` computes the state an
apply will set and derives the review from it, so the two cannot disagree —
the same rule `domain.PlanBulk` follows. Merge (the default) keeps everything
the file does not mention, key by key within a map; replace makes the exported
surface exactly the file's. **Neither mode touches what the file never
carried** — a replace leaves the snoozes and the namespace filter alone,
because destroying data on the strength of a document's silence would turn the
redaction rules into a way to lose things. A malformed document is refused
whole with the reason and never partly applied; an unknown field is counted
and ignored, so a file from a newer build still imports what this build
understands, and a version from the future is stated rather than refused.

Reading the file needs `SystemAPI.ReadTextFile`, which opens the picker and
returns the CONTENTS — `ChooseFile` returns a path, and the webview cannot
turn one into content. Its dialog is filtered where `ChooseFile`'s is
deliberately not. `SaveTextFile`'s save dialog now derives its filter from the
suggested name's extension: it was pinned to CSV, and macOS enforces a filter
as the extension, so a `.json` document would have been written as
`.json.csv` — which the log download was already quietly suffering.

## The session timeline is in memory, and that is the design

`web/src/stores/timeline.svelte.ts` keeps, per cluster and per object, what
happened while the tab was open: the Kubernetes Events an object produced, the
findings that appeared and cleared underneath it, and the writes PodSteer
itself made. It is shown as a Timeline tab in the detail drawer and as a
fourth pinned pseudo-entry beside the overview, Applications and All clusters
— `podsteer/timeline`, a record rather than a kind, and absent from
`domain/catalog.go` for the reason the other three are.

**It is in the frontend, and it costs nothing on the wire.** Everything it
records had already crossed the bridge for another reason: the assessment is
fetched on every refresh whatever view is open, a pod's findings ride every
row of the pod list (`Pod.findings`), the event lists are the rows of the
views that show them, and a write's outcome is resolved in
`web/src/lib/api/client.ts` before anything is recorded. So there is no
backend state, no extra request, no goroutine, and no Wails event to wait on
— the same trade `usageHistory` makes with the measurements a list response
was already carrying.

**Nothing reaches disk, deliberately.** A timeline is made almost entirely of
object names, and object names are not on the list of things SECURITY.md says
PodSteer writes on an operator's behalf — the same commitment
`ClusterSession.recentObjects` keeps for the navigator's Recent section and
the sampled capacity history keeps by carrying no names at all. Do not add a
preference that would persist it. What that buys is that there is nothing to
leak, nothing to clean up and no retention policy to get wrong; what it costs
is honest and has to be SAID rather than implied, so the panel carries one
line stating that it covers this session only and goes when the tab closes —
the same job `SeriesResult.spanSeconds` does for the sampled charts. The
durable version, backed by storage that outlives the process, is the planned
paid tier, and the seam belongs where `HistoryPort` puts it rather than in a
file this process writes.

Four rules there are load-bearing, each with a test in
`web/src/lib/timeline.test.ts` or `web/src/stores/timeline.test.ts`:

- **A finding appearing or clearing is DERIVED, and "gone" is not "not looked
  at".** `diffFindings` compares one assessment against the last, and a
  refresh that produced nothing passes `null` rather than an empty set: read
  as an assessment it reports every outstanding problem in the cluster
  clearing in the same instant. The same trap has a second shape for pod
  findings, which are scoped to a POD rather than to the cluster — the row
  buffers are mutually exclusive and the namespace filter narrows them, so a
  pod missing from a list has not been looked at, and only pods a list
  actually carried are diffed. A pod finding has no id, so its TITLE is the
  identity; that holds because the per-container rules put the container name
  in their title, and it lets the detail's numbers move without the finding
  reading as cleared and raised again.
- **Repeats are grouped, and grouping is a VIEW decision.** An event carries
  the API server's own `count`, so one entry is recorded per Event object
  however many refreshes re-read it, and `groupTimeline` collapses identical
  entries into one row carrying the summed count and the span. Every entry is
  in exactly one group and the count is on the row — the completeness rule
  `graphFold.ts` already holds the folded dependency map to.
- **It is bounded at both ends.** `MAX_ENTRIES_PER_OBJECT` (200) so one pod in
  CrashLoopBackOff cannot crowd out every other object, and
  `MAX_ENTRIES_PER_CLUSTER` (2000) so a session left open over a weekend
  cannot grow without limit. Oldest first, and an evicted event's observation
  identity is dropped with it or it could never be recorded again.
- **Every write is recorded in one place.** `writing` in
  `web/src/lib/api/client.ts` wraps each `ManagementAPI` call, so a dialog
  added later cannot forget — the same discipline that makes
  `ClusterSession.#recordRecent` the only place an object becomes recently
  opened. A refusal is recorded too, because "I pressed delete and nothing
  happened" is exactly the question this answers. Nothing that leaves the
  cluster unchanged goes through it — `ValidateResource`, `PlanDrain`,
  `PlanBulk` and a dry-run rollback are reads — and **no value is ever
  recorded**: writing one key of a Secret records the key, never what was
  written, exactly as `ManagementService`'s own audit line does.

## Licences are policy, and the build enforces it

The policy lives in two halves that must be edited together:
`docs/LICENCE-POLICY.md` (the reasoning, the tiers, the exception process) and
`build/licence-policy.json` (the machine-readable form). `make notices`
regenerates the inventory AND enforces the policy in one pass, so the two can
never describe different dependency sets. CI runs it inside the `quality` gate.

Three things about the scope are easy to get wrong and are handled in
`build/licences/collect.mjs`:

- **Shipped Go is the UNION across all three release platforms.** Running
  `go list -deps` once on the host is a trap that already caught us:
  `go-webview2` and two others are reached only under `GOOS=windows`, so a
  macOS-generated inventory omitted modules the Windows binary contains.
  `CGO_ENABLED=1` is forced, or cross-GOOS silently prunes cgo dependencies.
- **A package imported by `web/src/` ships, whatever `package.json` says.**
  The import scan cross-checks this and fails the build; mislabelling one as a
  `devDependency` would hide it from the inventory and break
  `npm ci --omit=dev`. This has happened here twice.
- **Build-only tooling is judged separately** (`buildOverrides`), because
  nothing obliges you to credit a compiler you did not distribute. A
  build-scope exception is guarded by a cross-check that fails if its package
  ever enters the shipped tree.

A shipped package that publishes **no licence text** blocks the build too, and
has two outcomes: the notice genuinely does not exist, which is recorded as an
exception in `notices_test.go`, or it exists elsewhere in the same project —
a monorepo where some tarballs carry the file and some do not — in which case
`build/licences/notice-sources.json` names a **sibling already in the
inventory** to copy it from. Never prose typed out from memory, and the
collector fails if the sibling is missing or the package has since started
shipping its own.

`UNKNOWN` is a blocking tier, not an error — an unclassifiable licence must
stop a build rather than be omitted from one. A package whose declared licence
and licence text disagree resolves to `UNKNOWN` too.

`make sbom` emits CycloneDX 1.6 from the same collector; CI attaches it to
every release. `app/adapters/notices/notices_test.go` re-asserts the important
properties from Go, so `go test ./...` catches a hand-edited inventory on a
machine with no Node.

## Commands

```sh
make dev        # wails dev — hot-reloads the frontend, rebuilds Go on change
make build      # packaged application into build/bin
make test       # go test -race ./...
make check      # gofmt + go vet + svelte-check
make bindings   # regenerate TypeScript bindings after changing a bound method
make notices    # regenerate the licence inventory and enforce the policy
make sbom       # emit a CycloneDX SBOM into build/bin/sbom
```

Regenerate bindings whenever a method on `ClusterAPI`/`WorkloadAPI` or a DTO in
`app/adapters/wails/dto.go` changes — `wails dev` and `wails build` do it
automatically, `go build` does not. The generated output in
`web/src/lib/wailsjs/` is **committed**, and the `bindings` CI job fails on any
drift, so a forgotten regeneration is caught in CI rather than at runtime.

## Branching and releases

Follows the ParliTrack standard: `develop` integrates, `main` holds released
code and is only reached by PR. Tags are `v1.2.3-dev-N` / `v1.2.3-rc-N` /
`v1.2.3`, cut with `make tag`. Full detail in `docs/RELEASING.md`.

Unlike a ParliTrack service, a tag here publishes artefacts and a GitHub
Release — there is no environment to deploy into and no `iac-argocd` step.

**macOS ships TWO assets, and the zip is not redundant.** The `.dmg` is what
podsteer.com links from its button, because a zip unpacks to a `.app` in
`~/Downloads` that most people then run from there. The zip is what
`homebrew.yaml` fetches BY EXACT NAME to compute the cask's checksum, so
removing it would break `brew install --cask podsteer` silently — the cask
would point at an asset that is not there. Both are signed, notarised and
stapled: the image is assessed with `spctl --type open`, not `--type execute`,
which reports a disk image as rejected whatever its state.

The asset names are a contract with podsteer.com, which composes them itself —
`Release.AssetName` there, guarded by `TestAssetNameMatchesWhatCIPublishes`.
GitHub resolves a release asset by exact name, so a rename here that is not
made there produces a download page of buttons that 404 against a real
release.

## Where this deviates from the Service Blueprint

Two deviations, both forced by Wails rather than chosen:

- **`go.mod` is at the repository root, not in `app/`.** The Wails CLI compiles
  the root package, so the `main` package must live there — and it must be
  inside the module. CI therefore uses `go-version-file: go.mod`, not
  `app/go.mod`.
- **No `app/internal/` layer.** The blueprint nests `application/`, `domain/`
  and `adapters/` under `app/internal/`; here they sit directly under `app/`,
  with `ports/` as a sibling package rather than interfaces living beside their
  domain models.

## Licensing, and why the seam matters

The application is Apache-2.0 and is intended to stay that way, whole — not an
open core with features held back. Contributions come in under the same licence
with a DCO sign-off (`git commit -s`); there is deliberately no CLA, because a
CLA's usual purpose is to preserve a relicensing option this project does not
want. See `CONTRIBUTING.md` and `DCO.md`.

A future paid tier is planned as **server-side**: storage, alerting and
notifications living in services that are not in this repository, reached by a
client adapter that will be Apache-2.0 like everything else here. Two
consequences for code written now:

- **The community build must never require an account** or contact anything
  PodSteer operates — no telemetry, no sign-in. That is a
  product commitment, and the CSP plus the absence of any HTTP client outside
  `adapters/k8s` is what keeps it honest.
- **Anything remote is an outbound port with a local implementation first.**
  `HistoryPort` is the model and says so in its own doc comment: the store has
  to be swappable because the obvious next implementation records outside the
  application entirely.

## A desktop launch has no shell, and managed clusters need one

`app/adapters/shellpath` runs the operator's login shell once and adopts its
PATH. It exists because a `.app` launched from Finder, the Dock or Homebrew
inherits **launchd's** environment — on a stock machine `launchctl getenv PATH`
is empty, so the process gets `/usr/bin:/bin:/usr/sbin:/sbin` and neither
`/opt/homebrew/bin` nor a Google Cloud SDK in the home directory.

That breaks every managed cluster. EKS authenticates by running
`aws eks get-token`, GKE runs `gke-gcloud-auth-plugin`, AKS runs `kubelogin`,
and client-go resolves all of them through `exec.LookPath`. **The same build
works under `make run`**, because a terminal passes its environment down — which
is precisely why this went unnoticed for so long. Development and an installed
launch are not the same launch.

Three things about it are deliberate:

- **It only fires when PATH looks like the bare system default.** An operator
  who launched from a terminal keeps exactly what they had; overriding it would
  make behaviour depend on a login shell they were not using.
- **It runs alongside the window, not before it.** Asking the shell costs about
  a second. `k8s.Config.EnvReady` is a channel the client factory waits on when
  it builds its first client, so the cost lands on the first connection rather
  than on every launch.
- **Failing is not fatal.** A startup file that hangs is bounded by a timeout
  and the inherited PATH is kept.

When it is not enough, `ports.ErrCredentialPluginMissing` names the binary.
That is its own sentinel and its own `ErrorCode` rather than unreachable,
because the cluster was never contacted: reported as an outage it sends
somebody to check a VPN, and it is deliberately not retryable.

## External systems

The local kubeconfig (`$KUBECONFIG`, else `~/.kube/config`, plus whatever
`PODSTEER_KUBECONFIG_DIR` names — see below) and the API servers it names —
plus, since v0.2.0, `api.github.com` for the update check, and nothing else.
No telemetry, no account, and still no network access from the webview (see
the CSP in `web/index.html`).

**`podsteer mcp` adds nothing to that list**, and the section above says why:
it speaks to its parent process over stdio and to the same API servers this
one does. A change that gave it a listener, or any other transport, would be a
change to this list and needs to be argued for here.

The webview's own policy is tightened at BUILD time, not in `index.html`:
`connect-src 'self' ws: wss:` is what Vite's hot reload needs, and a bare
scheme source permits a WebSocket to any host. A Vite plugin strips it from
the shipped page and `app/adapters/assets/csp_test.go` asserts the result on
the embedded bundle, so dev keeps what it needs and the two cannot drift.

**The update check is the only outbound call that is not a cluster**, and the
constraints on it are not negotiable style preferences. It sends no identifier
of any kind, goes to GitHub rather than anything we
operate (so no dataset exists here to correlate with the planned paid tier),
never runs on the startup path, caches failures, and is off entirely under
`PODSTEER_UPDATE_CHECK=false`. **Off means no request is made**, and that is
asserted in `app/application/updates_test.go` by counting calls to the source
rather than by checking the returned state — the opt-out is precisely what has
silently broken in k9s, Terraform, dotnet, JetBrains and Docker Desktop.

If a future paid tier wants a client-side call, **it does not get to reuse this
one.** That is the creep path this ADR exists to make visible.

The kubeconfig is **read on every call and written in exactly one place**:
`KubeconfigPort.Merge`, behind Add cluster. Everything about that write is
shaped by the fact that the file holds credentials — the paste is parsed and
the plan computed before the file is opened, an existing context name is
refused rather than replaced, symlinks are resolved so a `~/.kube` pointing
into a synced folder is written THROUGH rather than over, the previous
contents are copied to `<path>.podsteer.bak`, and the new contents reach a
temporary file in the same directory which is synced and renamed over the
target, preserving the mode. `app/adapters/k8s/kubeconfig_merge_test.go`
asserts each of those, because every one of them is a way to lose somebody's
access to a cluster quietly.

`current-context` is never touched. Adding a cluster is not a request to
switch to it, and kubectl in another terminal must not change target because
somebody pasted a config here.

**`PODSTEER_KUBECONFIG_DIR` extends the READ side only, never the write
side.** An operator with one file per cluster — `~/.kube/configs/*.yaml`, or a
folder synced from a password manager — points this at that directory instead
of hand-maintaining `$KUBECONFIG` as a path list, the way Radar's
`--kubeconfig-dir` and a synced Lens folder both work. `client.go`'s
`loadingRules` appends every regular file the directory holds (following one
symlink hop; dotfiles, subdirectories and anything that fails to parse as a
kubeconfig are skipped and, for a parse failure, logged at warn by path only)
to `clientcmd.ClientConfigLoadingRules.Precedence` AFTER the explicit or
default file, sorted by filename, and the directory is re-scanned on every
call for the same reason the kubeconfig itself is: a file dropped in appears
without a restart. Because Precedence is what governs the merge, and
client-go's merge keeps the FIRST file's definition of a context name, the
explicit file always wins a collision — a directory file cannot shadow a
context the operator already has. `Merge` still writes to exactly the one
explicit file described above; the directory is never a write target, because
the operator owns those files and may be syncing them from somewhere PodSteer
has no business writing to. Its existence check does look at the merged view,
though, so a name already taken by a directory-only context is refused the
same as one already in the explicit file.

## Domain quirks worth knowing

- **A Job is judged by whether it failed, not by whether it finished.**
  `Workload.IsHealthy` special-cases Jobs, because "0 of 1 completions"
  describes a job that started ten seconds ago exactly as it describes one that
  will never finish. `IsRunning` and `HasFailed` tell those apart.
- **`domain.Event` is a *Kubernetes* Event.** The application's own internal
  notifications are `domain.DomainEvent`. Getting this backwards is easy and
  the compiler will not always catch it.
- **`PodPhaseTerminating` is not a Kubernetes phase.** A deleting pod keeps
  reporting `Running`; the mapper substitutes `Terminating` when
  `DeletionTimestamp` is set, as kubectl does.
- **`Pod.IsHealthy` is not `phase == Running`.** A crash-looping pod reports
  `Running` while serving nothing, so `Running` additionally requires every
  container ready. `Succeeded` counts as healthy — otherwise every completed
  Job would flag.
- **Error classification crosses three layers.** `adapters/k8s/errors.go` maps
  client-go failures onto the `ports.Err*` sentinels; `adapters/wails/errors.go`
  maps those onto an `ErrorCode` and encodes it as a `[code] message` prefix,
  because Wails can only send an error as a string. `web/src/lib/api/errors.ts`
  parses it back. Changing the codes means changing both ends.
- **The navigator's Recent section is in memory only, deliberately.**
  `ClusterSession.recentObjects` (`web/src/stores/session.svelte.ts`) holds the
  last objects opened in the detail drawer and is gone when the tab closes,
  because object names are not on the list of things SECURITY.md says PodSteer
  writes to disk — the same no-object-names commitment the sampled capacity
  history makes. Pinned *kinds* are a different kind of fact (a catalog id,
  never an object name) and persist in `preferences.svelte.ts` for exactly
  that reason.
- **A drain is planned in the domain and executed in the adapter.**
  `domain.PlanDrain` (`app/domain/drain.go`) decides evict/skip/refuse from
  facts `app/adapters/k8s/drain.go` gathers; nothing in the domain touches the
  network, and nothing in the adapter decides who gets evicted. The same
  function backs both the preview `ManagementAPI.PlanDrain` shows before a
  drain runs and the plan `DrainNode` actually executes, so the two can never
  disagree — an operator who read "nothing refused" a moment ago must not then
  watch the drain refuse something. A single refused pod vetoes the whole
  plan, mirroring `kubectl drain`: doing part of a drain and stopping is a
  worse outcome than refusing to start, because a caller cannot tell "capacity
  freed" from "capacity freed except for the pod that mattered" without
  reading the report closely.
- **A bulk action is planned in the domain and executed in the application
  layer, and the two share one function.** `domain.PlanBulk`
  (`app/domain/bulk.go`) decides act/skip per selected object from facts the
  list rows already carry — the controlling `ownerReference` (never a label),
  a workload's desired count, a node's cordoned flag, the cluster's read-only
  flag — so planning a selection costs no read. `ManagementAPI.PlanBulk`
  shows that plan in the review dialog, and `ManagementService.BulkDelete`,
  `BulkRestart`, `BulkScale` and `BulkCordon` run the SAME function again
  before fanning the acting lines out over the single-object
  `ManagementPort` methods (bounded errgroup, no shared context), so what was
  reviewed is what runs. Unlike a drain, **a failure never aborts the rest**:
  one forbidden delete is a per-object result beside forty-nine successes,
  classified exactly as a single write's error would be, because a run that
  stopped halfway would leave the operator unable to tell from the list which
  rows were touched. The read-only guard runs once, up front, for the whole
  selection.
- **Eviction, never deletion, is what a drain and the pod drawer's Evict both
  use.** `ManagementPort.EvictPod` goes through the policy/v1 Eviction
  subresource specifically because it is the one request a PodDisruptionBudget
  can refuse — `DeleteResource` simply removes the pod, budget or no budget.
  A refusal is HTTP 429, mapped to its own sentinel (`ports.ErrDisruptionBudget`)
  rather than folded into `ErrForbidden`: RBAC allowed the request and the
  object's own policy declined it, which calls for waiting and retrying, not
  for different credentials. It is the only error `DrainNode` ever retries —
  every other failure during a drain is recorded as a `DrainFailure` and the
  rest of the node continues draining around it.
- **A revision comes from owned ReplicaSets or ControllerRevisions, never
  the watch store.** `WorkloadPort.RolloutHistory` reads a Deployment's
  ReplicaSets or a StatefulSet/DaemonSet's ControllerRevisions by
  ownerReference — the same rule `ListPodsForWorkload` follows, and for the
  same reason: a selector can be shared by an unrelated object wearing the
  same labels. `domain.Revision.Current` marks whichever carries the
  HIGHEST revision number, never the one with the most replicas or the
  newest timestamp: both controllers REUSE and renumber an existing
  revision object rather than creating a new one when a rollback's target
  template already exists, so the active revision is always the highest-
  numbered one, in steady state and immediately after a rollback alike —
  which is also why a StatefulSet's own `Status.CurrentRevision` field is
  deliberately not read, since the same rule has to hold for a DaemonSet
  too, and a DaemonSet carries no such field. A rollback is a PATCH, not a
  full replace: `ManagementPort.RollbackWorkload` copies a Deployment's
  target ReplicaSet `spec.template` onto the Deployment via a strategic
  merge patch — the same field and patch type `SetImage` uses, which is
  also why a target revision with FEWER containers than the live template
  will not remove the ones the merge does not mention — plus a
  `kubernetes.io/change-cause` annotation naming the rollback, and only
  when the Deployment already carries one today. A StatefulSet's or
  DaemonSet's rollback instead applies the target ControllerRevision's own
  patch data directly as a strategic merge patch, letting the API server do
  the same reconstruction `kubectl rollout undo` relies on rather than this
  process re-implementing strategic-merge-patch semantics by hand.
- **Log timestamps are always requested; the mode is a frontend display
  choice, not a stream option.** `domain.LogOptions.Timestamps` is sent
  `true` by every caller of `StreamLogs` regardless of what
  `LogViewer.svelte`'s timestamp control (off/local/UTC/relative) is set
  to — the mode only decides how `logTimestamps.ts` formats the RFC 3339
  prefix Kubernetes already wrote onto each line, never whether the API
  server is asked to write one. Re-opening the whole stream to add or
  remove a column would cost a fresh tail read for what is purely a
  rendering preference. `SinceSeconds` and `Previous` are the opposite case
  on the same struct: both change what the API server is actually asked
  for, so the frontend re-opens the stream when either changes, the same
  as it already did for `Follow` and `TailLines`.
- **A GitOps object's members are quoted from the controller's own status,
  never inferred from labels.** The Argo CD Application and Flux
  Kustomization/HelmRelease panels (`web/src/lib/gitops/`, rendered by
  `GitOpsDetail.svelte`) list `status.resources` and
  `status.inventory.entries` as followable rows — a label-based
  "applications" view was rejected on 2026-09-02 because a label is weaker
  than a selector (overlaps double-count, unlabelled workloads vanish, every
  line drawn is a relationship Kubernetes does not have), it needs a
  cluster-wide LIST per kind per refresh, and on an unlabelled cluster the
  UI cannot say why it is empty; the controller's status is the membership
  it acts on, costs the one GET the drawer already makes, and reads no
  Secret. The panel is selected by group AND kind ("Application" exists in
  three API groups), and it complements the bottom-up `gitops.ts` badge
  rather than replacing it. A Flux inventory id is
  `<namespace>_<name>_<group>_<kind>` as `sigs.k8s.io/cli-utils`'s
  `ObjMetadata` writes it: a core kind has an EMPTY group segment
  (`shop_web__Service`), a cluster-scoped object an empty namespace
  (`_shop__Namespace`), and a colon in an RBAC name is transcoded as `__` —
  so `parseInventoryId` reads the fields from the outside in and never
  splits on `_`. A HelmRelease carries no inventory at all (the objects are
  in the Helm release Secret, which is not read), and the panel says so
  rather than showing an empty list.

## Typed renderers instead of a plugin API, one per operator

**There is deliberately no extension API here** (decision of 2026-09-04, in
`podsteer/business-docs`): the market is leaving them — Lens killed its own,
FreeLens v2 broke every v1 extension — and a plugin surface is a compatibility
promise this project would then owe forever. The answer is the one Radar
takes: PodSteer ships TYPED, BUILT-IN panels for the controllers people
actually run, and adding another is a new module under `web/src/lib/operators/`
plus a branch in `OperatorDetail.svelte`, not a new API.

Five so far, each rendered by `OperatorDetail.svelte` from the ONE manifest the
drawer already fetched: cert-manager `Certificate`, KEDA `ScaledObject`,
External Secrets `ExternalSecret` (v1beta1 and v1 alike — matched on the group,
never the version), Argo Rollouts `Rollout`, and Trivy Operator
`VulnerabilityReport`. **Selection is by API group AND Kind together**,
extending the mechanism `web/src/lib/gitops/panel.ts` established and for the
same reason: `Certificate` is also a kind in `cert.gardener.cloud` with an
entirely different spec, and a panel chosen on half the coordinate opens on an
object whose fields it does not have. `argoproj.io` is deliberately declared in
both `gitops/argo.ts` and `operators/panel.ts` rather than shared — Argo CD and
Argo Rollouts are separate controllers that happen to share a vendor's group,
and each selector must stay exhaustive over only its own kinds. **A group absent
from the cluster changes nothing anywhere**: these kinds only reach the
navigator when discovery found them.

**All quotation, with exactly one exception, and the exception is named so it
cannot creep.** Ready, Active, Degraded, Paused and CRITICAL are the
controllers' own words in the controllers' own vocabularies; an enum value none
of these files has seen renders as itself. The exception is a Certificate's
expiry: `status.notAfter` and `status.renewalTime` are DATES, comparing them to
the clock and to the Ready condition is a verdict, and it is the one thing an
operator opens a Certificate to ask — so `domain.AssessCertificateRenewal`
(`app/domain/certmanager.go`) makes it, reached through `BrowseAPI` the way
`ClassifyConditions` is, returning at most ONE insight and nothing at all for a
certificate that is Ready or renewing on schedule. Two rules there are
load-bearing: an absent `renewalTime` is NOT an overdue renewal (cert-manager
also clears it while an issuance is in flight, which the `Issuing` condition is
read to tell apart), and a certificate with nothing issued yet produces no
verdict rather than a critical one.

Two things the panels refuse to do, both deliberate. **A KEDA trigger's
metadata value is not rendered when its key could name a credential**
(`isCredentialKey`) — KEDA's own design puts secrets in a
TriggerAuthentication, so this normally redacts nothing, but an inline
connection string does occur and a panel that printed one would put it on
screen and into a screenshot; the KEY is still shown, because knowing a
credential is configured inline is the point. And **an ExternalSecret's panel
maps remote key to local key and reads no value at all**, from the store or
from the Secret it writes — resolving one when a pane opens is exactly the
pattern the Secrets doctrine above exists to prevent.

**The Rollout panel is the only one that writes**, and both controls go through
the ordinary path: `refuseIfReadOnly` and one audit line in
`ManagementService`, the production type-the-name gate in
`RolloutActionDialog`, and a kubectl-transparency strip naming
`kubectl argo rollouts promote|abort NAME`. They are MERGE PATCHES of named
fields rather than an apply, because the fields live in spec AND status, the
controller owns and is concurrently rewriting status, and `UpdateResource`'s
full replace would send a stale copy of it back. `domain.PlanRolloutPromote`
(`app/domain/rollout.go`) decides WHICH patch from facts the adapter reads off
the live object immediately beforehand — the same plan-in-the-domain,
execute-in-the-adapter split as `PlanDrain` — and every body is transcribed
from `kubectl argo rollouts`' own implementation rather than invented, because
the kubectl hint's claim has to stay true. **The status subresource is not
optional**: it is sent there first, and only a 404 for that path makes the
unified body go at the object instead, exactly as the plugin's own fallback
does. A merge patch at the object on a cluster that serves the subresource has
its status half silently discarded — a promote that reports success and changes
nothing, which is the worst outcome available. Only the DEFAULT promote is
offered; `--full` and `--skip-current-step` are different acts with different
blast radii, and a button whose behaviour depends on an invisible flag does not
belong on a production cluster.

**The pod list's severity marks are a discovered add-on read, not part of the
poll.** `Adapter.ListVulnerabilitySummaries` (`app/adapters/k8s/trivy.go`)
reads what the Trivy Operator already wrote — PodSteer scans nothing, fetches
no advisory database and grades nothing — as ONE bounded list per cluster and
namespace, cached ten minutes (`vulnerabilityCacheTTL`, between
`backendCache`'s thirty and `readCache`'s seconds) and forgotten when a cluster
is invalidated. No scanner installed (404), an account that may not read the
reports (403), and a namespace nothing has been scanned in all return an empty
answer, and all three are CACHED, the same discipline `DiscoverMetricsBackend`
follows: an account that may never list something should not have that retried
into its audit log every time a pod list opens. The frontend
(`$stores/vulnerabilities`) asks once per cluster and namespace for the life of
the tab, never on the refresh tick; the list is drawn without it and is
unchanged if it never answers. A row is matched by its CONTROLLING OWNER
("Kind/name", the same string `Pod.ControlledBy` already carries) because the
operator writes one report per container of the workload, not per pod — with
the pod's own name as the fallback for a bare pod, which is the one case the
pod IS the subject. The mark is not a column: a column exists on every cluster
whether or not anything fills it, and on the great majority nothing would.

## A node shell is a pod PodSteer owns, and must be deleted like one

The node shell (`app/adapters/k8s/nodeshell.go`, `TerminalAPI.StartNodeShellSession`)
is a privileged pod that enters a node's host namespaces with `nsenter` — the
same thing `kubectl node-shell` and Lens do, and the most powerful thing this
application can do. Unlike an exec into a container, which leaves nothing
behind, this CREATES a pod, so it is tracked exactly the way a port-forward is
(`nodeShells`, a registry beside `forwards` on the adapter): the record of the
pod and the thing that deletes it are created and torn down together, because
every leak in the clients that offer this comes from those two parting company.
The pod is **deleted when its terminal session ends** — the attach session's
goroutine deletes it on exit — **and on application shutdown** (`StopAllNodeShells`
in `OnShutdown`, beside `StopAllPortForwards`), and it must appear in the
activity list with a stop control (`NodeShellsPanel`, the way `PortForwardsPanel`
does) so an operator can always see and end a root shell still running on a
node. The pod also carries `activeDeadlineSeconds` of one hour — NOT the normal
lifecycle, but a backstop for the one case cleanup cannot cover: PodSteer
crashing, which would otherwise leave a privileged pod running until someone
noticed. Deletion on session end is the rule; the deadline is the safety net.
The ephemeral debug container beside it (`AddEphemeralContainer`) is the
opposite: it is NOT tracked and NOT deleted, because Kubernetes will not remove
an ephemeral container — it stays in the pod's spec until the pod is deleted,
which the dialog states plainly.

## The local terminal runs the operator's own tools, and pins nothing

`app/adapters/localshell` opens a shell on the **operator's own machine** — the
one terminal here that reaches no cluster at all — and can start a coding agent
they already have in it (`TerminalAPI.StartLocalSession` / `StartAgentSession`,
`Terminal.svelte`'s `local` variant, launched by `sessionLauncher`). Three
rules govern it and each is asserted in a test.

**Nothing is bundled, downloaded or installed — ever.** Not kubectl, not helm,
not a coding agent. The shell runs whatever the operator already has on the
PATH `app/adapters/shellpath` adopted; a machine without kubectl gets the
shell's own "command not found", which is the honest answer and their business
to fix. Agents are FOUND (`DetectAgents`, a fixed preference order that does
not depend on the shape of the PATH) and offered; a machine with none has no
agent row and deliberately no link to obtain one. Shipping a binary would mean
shipping its updates, its licence and its CVEs, and would put PodSteer between
an operator and a tool they are perfectly capable of installing themselves.

**`current-context` is never touched, so the context is stated rather than
pinned.** `KUBECONFIG` is set to exactly the files the Kubernetes adapter reads
(`Adapter.KubeconfigFiles`, the one implementation of "which files", quoted in
both places, `PODSTEER_KUBECONFIG_DIR` included), and a one-line notice above
the prompt names the open tab's context and says to pass `--context`. There is
no honest way to do better: kubectl selects a context from `current-context` or
from an explicit flag and nothing else — no environment variable carries one —
which leaves writing the operator's kubeconfig (refused outright by the
kubeconfig section above; kubectl in the terminal beside this one must not
change target), writing a per-session copy of their credentials to disk, or
injecting a shell alias, which cannot be done without REPLACING their own
startup files: bash's `--rcfile` and zsh's `ZDOTDIR` substitute rather than add,
so they would lose their prompt, functions and aliases in exchange for one of
ours. Stating the context costs one line and lies about nothing.

**The read-only guard does not apply, and the pane says so.** Every other Start
method on `TerminalAPI` refuses synchronously on a cluster the operator marked
read-only. `StartLocalSession` does not, and adding the check "for consistency"
is the mistake `terminal_local_test.go` exists to prevent: that guard is about
PodSteer's own writes to a cluster, and a shell somebody opened on their own
machine with their own credentials is not something this application can or
should police. The agent's read-only default is the same shape — an environment
marker plus a sentence in the opening prompt asking for read-only kubectl
unless the operator says otherwise. A request, never a restriction, because
the agent holds the operator's credentials and nothing here can narrow them.

Lifecycle follows the port-forward and node-shell registries exactly: the
record and the process are created and destroyed together, a session ends when
its pane closes, and `StopAllLocalShells` runs in `OnShutdown` beside
`StopAllNodeShells`. Ending one signals its whole process GROUP — a shell's
children go with it — and waits, so "stopped" means gone rather than asked.

**Windows has no local terminal**, and says so instead of half-working. The
pseudo-terminal dependency (`github.com/creack/pty`, MIT) reports unsupported
on Windows for both allocation and resize; ConPTY is a different API and would
be a second, Windows-only implementation of start, resize and teardown. The
dependency sits behind a build tag so it is not linked into the Windows binary
at all, `LocalShellSupported` reports false with one sentence, and the control
is absent rather than present and failing. Nothing about a Windows build is
worse for it: kubectl in the operator's own terminal was always the answer
there, and needs nothing PodSteer provides, since Windows hands a GUI process
the same PATH it hands a console one.

Nothing is sent anywhere by PodSteer. Launching an agent is a local process
start with an argument; whatever the agent then does with its own provider is
between the operator and the tool they installed. There is no PodSteer service
in that path, which is what keeps this consistent with the no-account,
no-telemetry commitment — putting one there would be a different decision
needing its own record.

## The MCP server is a subcommand, it is local, and it only reads

`podsteer mcp` (`app/adapters/mcp`, wired in `app/cmd/mcp.go`) serves PodSteer's
reads to the operator's own coding agent over the Model Context Protocol. It is
the other half of the bridge `app/adapters/localshell` starts: that one hands
an agent a terminal, this one hands it the reads the window makes.

**Local, stdio, no listener.** The agent spawns the process and owns the pipes;
no socket is bound, nothing is served over HTTP, and nothing PodSteer operates
is contacted — the only traffic is the same cluster traffic the window makes,
with the same kubeconfig. A loopback listener would have been easier for some
clients to attach to and is refused anyway: it would make PodSteer reachable by
anything else running on the machine and would need an authentication story
this application has deliberately never had. That is the same reasoning as the
local terminal's, and it is what keeps this consistent with the no-account,
no-telemetry commitment in the licensing section above and with SECURITY.md's
external-systems list, which it does not extend.

**A subcommand rather than a service the window runs.** A background server
started by the desktop app would have to be discovered, would outlive the tab
whose credentials it was using, and would be running whether or not anybody was
asking it anything. A subprocess starts with the agent and ends with it. It
never reaches `wails.Run`, so the single-instance lock is untouched and an
agent can start one while the window is open.

**Read-only, and structurally so.** The server takes narrowed reading
interfaces (`ClusterReader`, `ResourceReader` and the rest in `server.go`)
rather than the inbound ports whole, so it cannot NAME `AddKubeconfig`,
`RevealSecretKey` or any `ManagementService` write, let alone call one.
`tools_test.go` asserts that by reflection as well as by tool name. **There are
no write tools and this is not an oversight**: every write in the UI is guarded
by a confirmation an operator reads — the type-the-name gate on a production
rollout, the drain preview, the bulk review dialog, the Secret-key reveal — and
an agent cannot be shown one. The honest options were a write with no
confirmation or a confirmation nobody sees. If a write is ever added, the
confirmation problem has to be solved first and recorded in
`podsteer/business-docs`, not worked around here.

**RBAC decides and a refusal arrives as a refusal.** Every tool goes through
the same use cases the window calls, so the server grants nothing the account
did not already have, and `errors.go` classifies a failure into the same
vocabulary `adapters/wails/errors.go` uses — kept separate rather than shared,
because one adapter must not import another, and because the readers differ. A
403 comes back as a tool result carrying `isError` and the word "forbidden",
never as an empty list: an agent handed an empty array reports that the cluster
holds no such objects, with complete confidence. The same rule shapes the rest
of the surface — a truncated list states its true total, an absent pod is
reported rather than assessed as healthy, and a bounded log read says it was
bounded.

**Secrets follow the doctrine above unchanged.** `GetManifest` is called with
`revealSecrets` false in exactly one expression, so a Secret's values are
replaced by their decoded size in the adapter before anything is serialised,
and there is no tool that reveals a key or parses a TLS certificate.

**The protocol is implemented directly** — JSON-RPC 2.0 over newline-delimited
stdio, about 150 lines in `jsonrpc.go` and `server.go` — rather than by taking
an SDK. The surface actually needed is five methods (`initialize`,
`tools/list`, `tools/call`, `ping`, and ignoring notifications), an SDK would be
a dependency whose own protocol revisions this repository would then track, and
`docs/LICENCE-POLICY.md` makes every shipped dependency a decision rather than
a convenience. Line-delimited reading rather than a streaming decoder is what
makes a malformed message survivable: a decoder left mid-value cannot
resynchronise, whereas a bad line is answered with a parse error and the next
one is read normally. One request is handled at a time, deliberately.

Two smaller decisions worth not re-deriving: the process runs with
`LiveWatch: false`, because a mirror pays for itself under a UI re-reading the
same lists every few seconds and not under an agent asking a handful of
questions minutes apart; and its user agent is `podsteer-mcp/<version>`, so an
operator reading their API server's audit log can tell a question their agent
asked from a pane they had open.

## Configuration

All optional, all prefixed `PODSTEER_`: `KUBECONFIG`, `KUBECONFIG_DIR`, `QPS`,
`BURST`, `REQUEST_TIMEOUT`, `LOG_LEVEL`, `LOG_SOURCE`, `LIVE_WATCH`,
`UPDATE_CHECK`, `COPY_MAX_BYTES`, `COPY_MAX_ENTRIES`. See
`app/config/config.go`.
