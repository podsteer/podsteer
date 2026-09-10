# PodSteer

A desktop Kubernetes client built on Wails v3 (Go backend + the OS's native
webview) rather than Electron, so that it starts fast and stays small in
memory.

Wails v3 is a BETA (`v3.0.0-beta.16`, pinned exactly in `go.mod` and in
`.github/workflows/ci-cd.yaml`). It is pinned rather than floated because a
beta renames things between releases, and `@latest` in CI would break a build
nobody changed.

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
│   ├── wails/      bound SERVICES and DTOs; the frontend API contract
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
point of caching it. 

**THERE ARE TWO WRITE VERBS AND THE DISTINCTION IS LOAD-BEARING.**

`UpdateResource` is the EDITOR's. The manifest is a draft of a LIVE object and
must carry the `resourceVersion` it was read at; it is a PUT under that
optimistic lock, and a stale version comes back as `ports.ErrConflict` — reload
the object and re-apply the edit, never retry the same request. Replacing the
object whole is the point: deleting a line in a full-object editor has to
delete the field, which is the only thing an editor can honestly promise. **A
manifest with no `resourceVersion` is REFUSED.**

`ApplyResource` is for DECLARED INTENT — the Create dialog, Duplicate, a pasted
or dropped file. Server-side apply, `fieldManager` the explicit constant
`podsteer`. A manifest names the fields it cares about and says nothing about
the rest, and the rest is LEFT ALONE.

**What this replaced could destroy data.** `UpdateResource` used to fall back
to Create when there was no `resourceVersion`, and on `AlreadyExists` it fetched
the live object solely to steal that version and then REPLACED it. Pasting a
Deployment manifest without `spec.replicas` over one an HPA had scaled to ten
reset the replica count, and deleted every label, annotation and field the
paste did not mention. A test pinned the behaviour, so it was chosen rather
than overlooked. It is not what `kubectl apply` does — it three-way merges
precisely to avoid this.

**A CONFLICT IS AN OUTCOME, NOT AN ERROR.** Where another field manager owns
something the manifest would change, the server refuses, nothing is written,
and `ApplyOutcome.Conflicts` names the fields and their owners.
`ApplyOutcome.Refused()` reports it; an error return means the request failed,
not that it was declined. `fieldConflictsFrom` reads `Details.Causes` BEFORE
`classify`, which would fold a 409 into `ErrConflict` and lose the list — and
a stale-resourceVersion 409 carries no such causes, which is what keeps the two
apart. **Nothing forces ownership** yet: taking a field is a decision an
operator makes with the owner's name in front of them.

`domain.ClassifyManager` decides what KIND of owner it is, because the sentence
differs — overriding Argo CD is not durable (it reverts on the next sync),
overriding kubectl takes a field from a person. The table is hand-compiled and
stale by construction, like the deprecation and release tables; an unrecognised
manager is `ManagerUnknown` and gets the server's own words, never a guess.

`fieldManager` is EXPLICIT rather than derived. client-go falls back to the
user agent, and `Config.UserAgent` is operator-settable — so an override
silently renamed the manager on every object PodSteer had written. The name is
a durable mark on somebody's cluster that outlives the uninstall, and it is not
per-user: it lands in `metadata.managedFields`, readable by anyone with `get`. Validation is a SERVER-SIDE dry run
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

## A `Limit` and the watch cache cannot be asked for together

`cachedResourceVersion` (`"0"`, `app/adapters/k8s/adapter.go`) asks the API
server to answer a LIST from its watch cache instead of a quorum read from
etcd. Every poll here uses it, and should.

**A list that also names a `Limit` must not.** The server DROPS the limit:
`ShouldDelegateList` excludes `"0"` from the branch that would send a limited
list to etcd, so the watch cache answers it, and `computeListLimit` returns 0
whenever the resource version is `"0"` — upstream's own comment reads "as of
today, the limit is ignored for requests that set RV == 0". The request is
accepted, no error comes back, and the response simply contains everything,
with no `Continue` token — so a paging loop reads one page, sees no token, and
reports itself complete having read the whole collection.

**It is not even consistent.** When that resource's watch cache is not ready, a
limited list with no selectors IS delegated to etcd
(`shouldDelegateListOnNotReadyCache`) and the limit binds. So the same cluster
truncates at the cap shortly after an API-server restart and reads everything
once the cache warms: two opposite bugs from one line, decided by something no
operator can see.

Three reads here were written that way, and the caps in their names had no
effect: the Helm listing (where the cap governed how many Secrets the server
was asked to decrypt), the vulnerability read, and one object's events.
`TestNoLimitedListAsksForTheWatchCache` walks this package's AST and fails on
the pairing, so it cannot come back. A read with no `Limit` uses the watch
cache freely; that is what it is for.

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

## The two edge columns stay put, and that makes the row backgrounds opaque

Every list pins its selection tick box to the left edge and its row menu to
the right, so both stay reachable when a wide table — a cluster with custom
columns, a narrow window — is scrolled sideways. `web/src/lib/fixedColumns.ts`
holds the arithmetic (which columns, which edge, what offset, and when to draw
the boundary hairline), `DataTable.svelte` publishes the switches and offsets
onto the `<table>`, and its own `<style>` block acts on them through `:global`
— the cells belong to each view, not to DataTable.

Three things there are load-bearing and are not obvious from any one file:

- **A ROW'S BACKGROUND MUST BE OPAQUE.** A sticky cell paints only what it was
  given, so the pinned cells take `background-color: inherit` and the row is
  what supplies the colour. That only works because the state tints are now
  pre-composited tokens in `app.css` (`--row-open`, `--row-ticked`,
  `--row-open-secondary`) rather than the translucent `bg-primary/8` they used
  to be — each is that alpha over `--surface`, which is the section behind
  every table. **Ticked and open moved together, to 9% and 16%**, and the
  ORDER is the load-bearing part rather than either number: a view draws the
  open ground for a row that is open AND ticked, so a ticked tint louder than
  the open one makes opening a ticked row look like unticking it. Changing one back to a translucent
  utility does not fail anywhere: it looks right on a still page and lets the
  scrolling columns show through the pinned ones the moment somebody drags the
  scrollbar. Header cells cannot inherit anything — a `<tr>` has no background
  and the band belongs to the `<thead>`, which scrolls out from under a cell
  that does not — so they name `--table-header` instead.
- **The elastic column sits BETWEEN the last real column and the menu.** The
  fixed layout needs one width-less column to absorb the surplus, and the menu
  has to be at the right-hand end whether or not there is any. So
  `RowMenuCell.svelte` draws both cells, in that order, and no view writes
  either by hand. The row menu is a real column (`ROW_MENU_COLUMN`, appended
  last after the operator's own) rather than something falling into the slack
  by arithmetic, which is what it was until 2026-09-06 — and which is why the
  header used to hold one cell fewer than every row.
- **The tick box's DRAWN state is DOM structure, never the input's
  `checked`.** `Checkbox.svelte` — the one checkbox in the application, native
  input underneath, mark inserted and removed by an `{#if}` — exists because
  RowSelect cancels the browser's own toggle (a shift-click on a ticked row
  ADDS a range and must leave it ticked) and the browser puts a cancelled
  toggle back after every listener has run. Svelte's `set_checked` caches the
  value IT last wrote, so the cache said ticked while the DOM said not and
  every later write short-circuited: the box drew no tick again for the life
  of the page. Structure cannot be reverted by a browser that did not create
  it. Do not re-express the mark as an `input:checked ~ .box` rule — that is
  the same bug in CSS.
- **`Column.pinned` and "fixed" are different things.** `pinned` means the
  column cannot be hidden; fixed means it does not scroll. The row menu is
  both, the status column is neither, and the two are decided in different
  places — `pinned` per column by the view, fixed per EDGE and
  application-wide in `preferences.fixedEdges`, because keeping the tick box
  in view is a reading habit like `wrapLines` rather than a fact about pods.
- **EVERY CELL OF A LIST ROW CENTRES ITS CONTENT, AND NOTHING IN A ROW IS
  ALIGNED ON A BASELINE.** One rule, stated once in `DataTable.svelte`'s own
  `<style>` and selected through the unconditional `data-list-table` marker on
  the `<table>`: `vertical-align: middle` on every `th` and `td`, and whatever
  a cell holds laid out as a BLOCK-LEVEL flex row with `items-center` rather
  than as an inline box. The tick box, the status icon and the name used to be
  positioned by three different mechanisms and drifted against each other
  accordingly — a `<td>` resolves to `vertical-align: baseline` by default, and
  a baseline is only a shared reference for boxes that HAVE one: text has a
  real one a font-descent above its line box's bottom, an SVG has a synthesised
  one at its bottom margin edge, and `RowSelect` carried an explicit
  `align-middle` of its own. Four cells, four quantities — a font's descent, a
  cell's padding, a box's height, an override — which can only agree by
  coincidence. Centring depends on ONE quantity for all of them, the row's
  height. The markup half cannot be done from the stylesheet, which is why
  `StatusIndicator` is `flex` and not `inline-flex` (a block-level flex box has
  no strut and no baseline to answer to) and why `RowSelect` and `RowMenuCell`
  wrap their controls in one. Do not put a per-cell `align-middle` back: a rule
  half the cells state for themselves is a rule the other half can be written
  without, which is how this drifted in the first place.

## A row menu asks the drawer; it never confirms or writes anything itself

Every list row's menu (`RowMenu.svelte`, filled by `web/src/lib/rowActions.ts`)
offers what that kind can be put through — every kind opens with Overview; a
pod then gets Logs, Terminal, Evict, Delete and the kubectl copy; a controller
gets Restart, Scale, Delete; a node gets Cordon or Uncordon, Drain and a node
shell. **Not one of those items performs a write or shows a confirmation.** Each sets a `DetailIntent` on the
session and opens the object, and `DetailDrawer` engages its OWN control for
it — so there is still exactly one Delete dialog, one Scale dialog and one
drain preview in the application, each with the guards it already had: the
production type-the-name gate (`nameConfirmed`, `web/src/lib/confirm.ts`), the
drain's per-node plan, and the eviction's sentence about a budget refusing it.
A row menu that opened its own confirmation would be a second implementation
free to drift; one that called the API directly would be a write with no
confirmation at all.

Eight rules there, each with a test in `web/src/lib/rowActions.test.ts` or
`web/src/lib/components/RowMenu.test.ts`:

- **The intent is consumed exactly once.** `takeDetailIntent` clears it as it
  hands it over, or the next object opened by an ordinary click inherits the
  last row menu's request — a delete dialog waiting on a pod somebody merely
  looked at. `closeDetail` drops a pending one for the same reason.
- **The drawer's intent effect is declared AFTER its reset effect**, because
  both are dirty in the same flush and effects run in creation order: the
  reset puts the tab back to Overview, so a "Logs" from a row menu declared
  first would land on the wrong tab.
- **An item exists only where the drawer renders a control for it.** A
  DaemonSet has no Scale (no replica count — `PlanBulk` says so in those
  words), a ReplicaSet has none either because `isScalable` covers Deployments
  and StatefulSets only, a Job has no Run now (that creates a Job from a
  CronJob's template), and a node has no Delete. The bulk bar's sets are
  looser on purpose: `bulkActionsFor` mirrors `domain.PlanBulk`'s kind rules,
  which are kubectl's.
- **A pair is one item.** Cordon or Uncordon, Suspend or Resume — chosen from
  the row's own flag, since only one of them could change anything. Resume and
  Uncordon run without a dialog, which is the drawer's rule for them rather
  than a shortcut taken here.
- **A destructive item is marked, and the mark is NOT a colour.** Delete,
  Evict and Drain were drawn in the error token; they are not any more. Red on
  the two or three most dangerous rows made the whole menu read as a warning
  rather than as a list of what a row offers, and it separated nothing for
  anybody who cannot see it — a colour is not in the accessibility tree,
  carries no ARIA and is announced by nothing. What separates them is the
  item's own word and icon ("Delete" with a bin, "Evict" with a door, "Drain…"
  with an ellipsis promising a dialog), and that is what a screen reader was
  getting all along. `RowAction.destructive` survives as a FACT rather than a
  styling hook, published as `data-destructive` so a test can name those three
  without matching on their labels.
- **The read-only guard disables rather than hides.** `toRowActions` disables
  every item marked `write` on a read-only cluster and carries the backend's
  own sentence as the tooltip, while Overview, Logs and the kubectl copy stay
  usable.
- **OVERVIEW IS FIRST, ON EVERY KIND.** It opens the drawer on its Overview tab
  exactly as Logs opens it on Logs, and unlike Logs or Scale there is no kind
  where it leads nowhere — the drawer declares that tab `show: () => true`. It
  also gives the menu a harmless first item, so what sits under the pointer the
  instant a menu opens is the reading rather than a write. `DetailIntent.tab`
  accepts `'overview'` even though the drawer's reset effect already lands
  there: the reset clears the PREVIOUS object's tab, which is a different
  question from where this request wants to go, and a drawer that one day
  remembered the last tab per kind would silently break every Overview item
  that had said nothing.
- **THE SEPARATOR IS DERIVED, NEVER PLACED.** `RowActionCopy.local` says the
  item does not touch the cluster — true of "Copy as kubectl" alone, which
  composes a string on this machine and stops, and false of Overview, Logs and
  Terminal, which open the object and read it. `RowMenu` draws a rule wherever
  two consecutive items disagree about it. So no view holds a divider, a menu
  whose items vary (a node's, a suspended CronJob's) cannot grow the line in
  the wrong place or grow two, and a menu that sets the flag on nothing — the
  detail pane's, which is all reads — gets none. It is deliberately NOT the
  same question as `write`: Logs is not a write and still reaches the cluster,
  so sharing one flag would either disable Logs or put the line above it.
- **A CONFIRMATION IS EVIDENCE, NOT DECORATION.** See the next section.

## Every copy goes through one helper, and it reports whether it worked

`web/src/lib/clipboard.ts` is the only thing in the frontend that puts text on
the clipboard, and it returns a BOOLEAN. Sixteen call sites used to do it
themselves — `DetailList`, `LogViewer`, `Terminal` (twice), `ForwardAddress`,
`CertificateInspector`, `ShareMenu`, `DiffView`, `KubectlHint`, the manifest
copy in `DetailDrawer`, and a `copyKubectl` of its own in each of the six list
views — and most of them were written as
`navigator.clipboard?.writeText(text).catch(() => {})`, deliberately silent on
the argument that a permissioned API may refuse and the text is on screen
anyway.

**That argument is right about a refusal and wrong about this case.**
`navigator.clipboard` is only defined in a SECURE CONTEXT, and the webview
serves the page over the framework's own scheme rather than https or
localhost — so in the shipped application the property is ABSENT, the optional
chain makes the whole expression `undefined`, and there is no promise, no
rejection and nothing for `.catch` to see. The copy never happened. `RowMenu`
then flashed "Copied!" beside it because `copied.show()` ran unconditionally
after the handler returned, so an operator who trusted it pressed paste and
got whatever had been on their clipboard before — on a shared machine, someone
else's text.

Four rules, each with a test in `web/src/lib/clipboard.test.ts` or
`web/src/lib/components/RowMenu.test.ts`:

- **The Go process is tried FIRST**, through the runtime's own clipboard, and
  `navigator.clipboard` is the fallback. Its availability matches the
  application's own: if PodSteer is running, that path exists. The DOM one does
  not, and even where it is present it additionally wants the document focused
  and, on some engines, a live user activation — so a copy fired as a menu
  closes can be refused for reasons unrelated to what was asked. Ordering it
  the other way puts the conditionally-present mechanism ahead of the
  always-present one and makes every copy in the shipped build pay a failure
  first. The test asserts the DOM path was NOT reached, because a test that
  only checked the boolean would pass with the two swapped.
- **The DOM path is kept anyway.** `vite dev` serves over http://localhost,
  which IS a secure context and where no Go process is attached — so it is what
  makes a copy work in a plain browser tab, and it is the path the unit tests
  take for real rather than through a mock.
- **No caller may claim a copy it did not make.** Every confirmation in the
  application is now downstream of that boolean: `RowMenu` says "Copied!" only
  for `true` and "Copy failed" for `false`, and a handler that returns nothing
  is read as a failure — silence is the safe direction, and it stops an
  unconverted handler acquiring a confirmation by saying nothing. Being quiet
  about a failure is still allowed; claiming a success is not.
- **The test stub refuses, and is not taught to lie.**
  `web/src/test/wailsRuntimeStub.ts` rejects `Clipboard.SetText` like every
  other entry point there, which is exactly the shape of a browser tab with no
  Go process behind it — so a component test that copies something still takes
  a real success through the real fallback, which happy-dom provides. A test
  wanting the both-refused case removes `navigator.clipboard` for its own
  duration instead.

One consequence worth knowing: `Terminal`'s copy-on-select chip now waits on
that boolean and carries a generation token, because a selection changes as
fast as somebody can drag and a slow answer about an abandoned one would
otherwise raise a chip over the selection that replaced it.

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
- **A monitoring stack already in the cluster is discovered, and is queried
  only when somebody asks** — `app/adapters/k8s/prometheus.go` lists Services by
  two label selectors and produces a RANKED CANDIDATE LIST rather than one
  guess, because a kube-prometheus-stack install returns several and only one
  answers PromQL. `DiscoverMetricsBackend` is the head of that list — which is
  what every caller wanted — and `ListMetricsBackends` hands over the whole of
  it from the same cached answer, so offering an operator the candidates
  PodSteer did not pick costs no extra request. Finding nothing is the ordinary
  answer and never reaches `Unavailable`: a cluster with no monitoring stack is
  not a degraded cluster.
  **Prometheus AND VictoriaMetrics**, ranked in that order — Prometheus first
  because it is what this build always pointed at, so a cluster running both
  keeps the answer it had. VictoriaMetrics is matched on
  `app.kubernetes.io/name` and never on the Service NAME, which is the part it
  does not hold still: its operator's `useLegacyNaming` strips the prefix
  entirely and both Helm charts prepend a release name, while the labels are
  identical either way. **The write path is never a candidate** — vmagent,
  vminsert and vmstorage ingest and none of them serves a query API — and the
  `victoria-metrics-cluster` chart makes that a live hazard rather than a
  theoretical one: it puts the same `app.kubernetes.io/name` on vmselect,
  vminsert AND vmstorage, and vmstorage publishes a port literally named
  `vmselect`. The component label is what tells them apart, and
  `writeOnlyNameLabels` refuses the operator's three by name as well.
  `MetricsBackend.Prefix` carries where the query API is mounted: empty for
  Prometheus and for a single-node VictoriaMetrics, `/select/0/prometheus` for
  a VictoriaMetrics cluster's select component, which serves it per tenant.
  **Tenant zero only** — nothing in a Service records which account ids hold
  data, and a wrong tenant answers 200 with no series, which reads as an idle
  cluster. `Describe` names the PRODUCT it found; telling somebody they run
  Prometheus when they run VictoriaMetrics sends them looking for something
  that is not there.
  **PodSteer NOW QUERIES one of them, and the rule that replaced "it does not"
  is narrower rather than gone: PodSteer queries nothing it was not asked to
  query, and draws no series it cannot attribute** (ADR 7). Six things make
  that true and each has a test.

  **The gate is `clusters.<context>.metricsQuery` in `settings.json`** — mode
  (off, manual, auto), preferred backend, fleet policy — read by
  `application.MetricsQueryService` BEFORE discovery runs, before the node
  list, and before anything reaches the network. Off is the default and off
  means no request is made, which `metricsquery_test.go` asserts by counting
  calls rather than by reading the returned status, the discipline
  `updates_test.go` established.

  **`auto` sends a query when a chart opens or its range changes, and NEVER on
  the refresh tick.** That exclusion is the whole of the difference between
  this and querying on a poll, which the record refused. The rule lives in
  `web/src/stores/backendTrend.svelte.ts`: every trigger — opening, a range
  change, the chart's own control, and the session's tick — goes through one
  `consider(reason)` so no call site can forget, and `backendTrend.test.ts`
  drives a dozen refreshes and counts adapter calls. `OverviewView.svelte`
  keeps that honest with a SEPARATE effect from the sampled chart's, which does
  not read `session.lastRefreshedAt`.

  **ONE GESTURE, ONE QUERY**, and two things enforce it. The in-flight key is
  claimed BEFORE the await, not after the answer resolves — claimed after, a
  range change raced its own effect and both queries reached the backend and
  the audit log. And that effect reads the window through `untrack`, because
  the control that changes the range is already a trigger: reading it
  reactively makes the effect a second one for the same gesture.

  **A result carries no metric of its own**, so `#load` clears the previous one
  before asking. Left in place, a CPU answer is drawn on the memory chart —
  scaled by a thousand, under a legend naming the service that answered — and
  stays for ever if the new call throws. `TrendChart` refuses a backend result
  whose `unit` does not match the metric it is drawing, as the second lock on
  the same door.

  **The transport is the service proxy `reachability.go` already uses, and it
  is a GET.** `app/adapters/k8s/promquery.go` proxies through the API server on
  the tab's own credential, so no outbound host is added. **A POST is refused
  rather than merely unused**: posting to services/proxy is the RBAC verb
  `create`, a different permission from the `get` every proxying account
  already holds, so a form body would fail for exactly the tightly-permissioned
  accounts this project is built for. That is why a node filter travels in the
  URL, and why an expression past `domain.MaxQueryURLBytes` is refused with a
  sentence rather than sent. The answer is read through an `io.LimitReader` and
  refused UNDECODED past `maxQueryResponseBytes`. **The rate limiter does not
  reach these requests** — it lives on client-go's `RESTClient`, not on the
  transport, and the round trip is made on `clients.queryHTTP` because a
  bounded read of a FAILED response needs the body client-go's helpers discard
  — so `PODSTEER_QPS` and `PODSTEER_BURST` bound everything around this file
  and nothing in it. What makes that acceptable is one request per user action
  and none on a tick.

  **A NODE NAME IS ESCAPED TWICE, AND ONCE IS A BROKEN FEATURE.**
  `regexp.QuoteMeta` handles the regex grammar (a cloud node name carries dots
  and an unescaped dot matches any character), and every backslash it produces
  is then DOUBLED for the PromQL string literal it sits inside — whose escape
  rules are Go's, where `\.` is not one and the lexer answers
  `unknown escape sequence`. Stopping at the first escape makes every
  `FleetFilter` query a 400 on precisely the clusters the narrowing exists for,
  and the only reason a fleet sum was never wrong is that no query ever
  succeeded. `domain.quoteForPromQL` is the one place both are applied. The
  hand parser in `promql_test.go` is STRICT about escape sequences for this
  reason — its first version skipped whatever followed a backslash, accepted
  what Prometheus refuses, and the escaping test then pinned the broken output
  as correct; its `promEscapes` table carries the date it was checked against
  the real parser, which is deliberately not taken as a test dependency.

  **A refusal to proxy is cached per cluster for `queryRefusalTTL` and dropped
  on `Invalidate`** — an account that may not proxy is refused every time, and
  each retry is a denied request in somebody's audit log. It has a TTL because
  a permission is a thing somebody grants: without one, an administrator adding
  a RoleBinding while the tab is open left a sentence that had become false.
  **Only a 403**: a 401 is a credential fact, client-go re-runs an exec plugin
  on one, and caching it is the one way to guarantee there is no next request.
  A transport failure is not cached at all.

  **Everything cached here is written under a GENERATION captured before the
  request**, in `k8s.generations` and in `application.verificationCache`. A
  query holding the old client set passes `Invalidate` and then writes; what it
  writes is about the cluster this tab has left, and for a node set that would
  license a narrowed sum over nodes the new connection has never seen. Ordering
  provably cannot close that window — the generation makes the late write
  inert. The HTTP client needed no such guard once it moved onto the client set
  itself (`clients.queryHTTP`), where it cannot outlive its own config.

  **The expressions are a fixed table in `app/domain/promql.go`, keyed by
  metric and scope, and there is no query box.** Every one is pre-aggregated at
  cluster or node level, and `promql_test.go` parses each rendered expression
  and fails if its outermost operator is not an aggregation closing at the end
  of the string — a prefix test would pass `sum(a) + rate(b[5m])`, whose result
  set is the pod count. The step scales with the range to a few hundred points
  and the rate window is `max(5m, 2*step)`.

  **A backend may front several clusters, so a subset check decides whether an
  aggregate may be drawn at all.** `domain.VerifyBackendNodes` compares the node
  set the backend answers with against this cluster's and produces four
  outcomes: **verified** (a subset), **fleet** (strangers in it — Thanos, Mimir,
  Cortex, a VictoriaMetrics cluster), **mismatch** (disjoint) and
  **unverifiable** (nothing to compare). OVERLAP WOULD BE THE WRONG TEST and
  that is the point of the function: a fleet backend overlaps with every cluster
  it fronts, and an unfiltered `sum` against one then draws five other clusters'
  load under this cluster's name — worse than no chart, because nothing about it
  looks wrong. The probe is `count by (node) (container_memory_working_set_bytes)`
  as an instant query, chosen because it is the metric and the label the charts
  themselves depend on, so the check cannot pass while the feature fails;
  `/api/v1/label/node/values` is deliberately not used, since it reports the
  label's values for ANY metric and can only widen the set. It is cached per
  cluster AND per backend for half an hour and dropped on `Invalidate`, because
  on a fleet backend that probe touches every container series the querier
  fronts — and it is SINGLEFLIGHTED, so two charts opening together send one
  probe rather than two. A probe that FAILED is remembered for
  `probeFailureTTL` instead, so a backend slow enough to time out is not
  re-asked and re-waited on every range change. On **fleet** the per-cluster
  policy decides — narrow every expression to this cluster's node names, or
  refuse the aggregate and say why; on **mismatch** and **unverifiable**
  nothing is drawn.

  **A cluster whose NODES could not be listed is unverifiable, not forbidden.**
  That is the namespace-scoped account this project is built for: it may proxy
  perfectly well and simply may not list nodes cluster-wide, and reporting that
  as a refusal sends somebody to ask for a permission they already hold about a
  Service nobody asked about. The backend is not probed either — with nothing
  of ours to compare against, the answer is unverifiable whatever it says — and
  the outcome is not cached, because a node list can fail transiently.

  **`domain.BackendStatus` keeps `answered-empty` separate from `answered`**,
  and that is not pedantry: a Prometheus that scrapes application endpoints and
  not kubelets returns HTTP 200 with no series for every expression here, and
  collapsed into "answered" that is a blank chart under a green label. A backend
  can also fail INSIDE a 200 (`"status":"error"`), which `decodeQueryResponse`
  catches. A rejected expression comes back with the backend's own message
  verbatim, the way `ErrManifestRejected` carries the API server's — which is
  also why the HTTP is done with an `http.Client` built from the same
  `rest.Config` rather than through client-go's `Stream`, whose error message
  reads "unknown" for any body that is not `text/*`.

  **A failure is routed by the BODY and not by the code.** A body that decodes
  as `kind: Status` came from the API server, so the proxy never reached the
  backend — and no such body may reach the rejected branch whatever its code,
  or a throttled API server's raw Status JSON is shown to the operator as their
  Prometheus explaining itself about a query it never saw. A 401 or 403 whose
  body is NOT a Status is the backend's own authentication — kube-rbac-proxy,
  an oauth proxy — and gets `ports.ErrMetricsBackendAuth` and
  `domain.BackendNeedsCredential`, because the service proxy strips
  `Authorization` and so neither a Kubernetes permission nor a change to the
  expression can ever make that route work.

  **Every series carries its provenance and the two are never merged.**
  `domain.SeriesProvenance` travels with each answer — origin, the service that
  answered, the verification, whether it was narrowed — and `TrendChart.svelte`
  draws the backend's line as its own series with its own colour and dash,
  naming it in the legend and renaming PodSteer's own to "PodSteer samples" the
  moment a second measurement is on the chart. Splicing them, or using one to
  fill a gap in the other, is the recorded mistake ADR 1 refused for kubelet
  readings.

  Per-object usage is still not written to disk — the recorded cluster history
  deliberately carries no object names, and a file of per-pod series would
  reverse that — and no value a backend returned is recorded anywhere.
- **kube-state-metrics is discovered the same way, and is a SEPARATE
  question** — `app/adapters/k8s/kubestate.go`, beside `prometheus.go` and
  following it in every particular: two label selectors
  (`app.kubernetes.io/name` and the older `k8s-app` variant), a ranked pick
  because a cluster genuinely holds two often enough to matter, a miss and a
  refusal are both ordinary answers rather than errors, a refusal is cached
  and a transport failure is not, and the answer is dropped when a cluster is
  invalidated. **PodSteer does not query it, and there is deliberately
  no `ProxyTarget` on `domain.KubeStateMetrics`** — `MetricsBackend` has one
  because a proxied PromQL query is a thing this build now does, while
  kube-state-metrics is a scrape endpoint and reading it would be
  PodSteer collecting metrics rather than pointing at the system that already
  does. That distinction is what keeps the ADR 7 read narrow: it asks a system
  that already keeps series for series it already has, and does not turn
  PodSteer into a scraper. It is carried separately from `Backend` because they answer different
  questions — one keeps series, the other produces the object-state series a
  great many of them ARE — and what the note buys an operator is knowing why
  the replica counts and Job gauges in their Grafana exist while PodSteer's
  own figures come from the metrics API and its own samples, which
  `KubeStateNote.svelte` says in as many words. Two deliberate divergences
  from `pickPrometheus`, both because nothing connects to it: an unrecognised
  port does not disqualify a service (Prometheus needs the right port or it
  proxies PromQL at a gRPC listener; this needs none, so the port is reported
  when recognised and left empty otherwise), and an exact name outranks a
  Helm-prefixed one, because `nameRank` scores those the same and there is
  only one candidate name here — a list of one ranks nothing, and the
  two-install case would be decided by list order.

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

## A reachability probe says WHERE it was answered from, or it says nothing

`app/domain/reachability.go` plans a probe of a Service, a pod or an Ingress
and shapes what came back; `app/adapters/k8s/reachability.go` opens the
sockets; `application.InspectService` guards and audits it. **Two vantages, and
the difference between them is the entire feature**, so `ProbeResult` carries
its vantage and its ROUTE and no surface renders an outcome without both. From
this machine, a Service goes through the API server's own service proxy — a
success means the endpoints answer *the API server*, which is subject to no
NetworkPolicy — and a pod goes through an **ephemeral port-forward**, reusing
`StartPortForward`/`StopPortForward` rather than a second SPDY dialer and torn
down in a defer so a cancelled probe cannot leak a bound socket. From inside
the cluster, `domain.ProbeCommand` is one `sh -c` in a container the operator
names, so cluster DNS, the CNI and every policy in the path are exercised as a
workload experiences them. **An Ingress has no local vantage at all**, and that
is a product rule rather than a gap: reaching a public host means connecting to
something that is not an API server, which is the one thing "External systems"
below forbids — the panel says so and points at a browser.

Five rules, each with a test:

- **DNS and connect are separate steps and always will be.** A name that does
  not resolve and an address that refuses need opposite next steps, so
  `OutcomeNameNotResolved` is its own outcome and a failed resolution
  short-circuits rather than reporting a connect that never happened. `skipped`
  is a third status because an address literal has nothing to resolve, and
  rendering that as a failure sends somebody to look at cluster DNS.
- **A refusal is an ordinary answer, never an error dialog.** Only a probe that
  could not be PERFORMED rejects — an ExternalName or headless Service, a UDP
  port, an Ingress from here — and each of those is a fact about the object that
  `CodeProbeUnavailable` carries verbatim into the panel where the result would
  have gone. A container with no `nc`, `curl` or `wget` is
  `ports.ErrProbeToolMissing` / `probe_tool_missing`, the direct sibling of
  `ErrTarMissing` and for the same reason: it says nothing about the target.
- **Every probe is explicit, one-shot and bounded.** `domain.ProbeTimeout` (5s)
  is a hard ceiling carried on the plan so no adapter invents its own, and
  nothing on this path is ever called by the refresh tick — that rule is written
  on `ports.InspectPort` and `ports.InspectService` and is why both live behind
  one interface.
- **The in-cluster probe is write-shaped and treated as one.** It runs something
  in somebody's container, so it goes through the read-only guard and leaves one
  audit line naming cluster, namespace, pod, container and target — and **never
  the output**, exactly as a file transfer's line never carries a file's
  contents. The local probe is not guarded, on the same line `StreamLogs` and
  `DownloadFromPod` sit on.
- **The command and the parser are one protocol**, so `ProbeCommand` and
  `ParseProbeOutput` live together in the domain with a test asserting the
  script emits what the parser reads. Host and port are vetted by `PlanProbe`
  and single-quoted regardless.

## An image is described from what Kubernetes reports, and says what it did not read

`app/domain/imagereport.go` describes a container's image using the resolved
reference and digest the kubelet recorded, the size and names of the node that
pulled it (`status.images`), the pull policy, and the NAMES of any
`imagePullSecrets`. Two GETs — the pod and its node — on request, never on a
tick.

**No registry is contacted, and that is a decision rather than an omission.**
The layers, their creation commands, the entrypoint, the exposed ports and the
labels live in the image's manifest and config blob in a registry; reading them
is a new outbound destination for every image an operator opens, and for a
private one an authenticated read whose credential is a pull Secret the Secrets
doctrine says is read on explicit request and never on render. "External
systems" below is the commitment on the other side of that, and the update-check
ADR's rule applies unchanged: a new client-side call does not get to reuse an
existing one, and needs its own record in `podsteer/business-docs`. Until then
`domain.ImageDetailBounded` is on **every** report and the panel always shows
it — a `Bounded` line, not an `Unreadable` one, because no read was attempted
and no permission changes that, and empty space where layers would be is a
claim nothing checked. A pod that pulls with credentials gets
`domain.ImageCredentialNote`, which names the Secret and states that it was not
read.

Three rules there are load-bearing:

- **The node's digest and the container status's digest routinely differ**, and
  that is what a multi-platform image looks like: one names the index and the
  other the manifest for that node's architecture. So `matchNodeImage` tries the
  digest FIRST and falls back to repository-and-tag, and the disagreement is
  stated in `DigestNote` rather than hidden — matching on the digest alone
  reports "the node does not list this image" for an image the node is plainly
  running.
- **A size that was not measured is a dash with a reason, never a nought.**
  `ImageSizeStatus` separates measured from not-reported (the kubelet garbage-
  collects images) from unreadable (an account without `get nodes`, or an
  unscheduled pod), modelled on `MetricsStatus` and for the same reason; a
  refused node is carried INSIDE the facts rather than failing the call, so the
  digest and references still render.
- **The resolved reference is the subject, not the declared one.** What the
  kubelet says it is running is a fact; what the manifest asks for is an
  intention, and `Drift` is the comparison — made in the domain, like every
  other comparison here.

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

## Listing a directory emits PodSteer's own format, never `ls` output

The browser half of file copy (`FileCopyAPI.ListDirectory`) runs a POSIX `sh`
script the domain composes and reads a format the domain defines —
`domain.ListCommand` beside `domain.ParseListOutput`, the arrangement
`ProbeCommand`/`ParseProbeOutput` already uses because the command and the
parser are one protocol and drift the moment they are separated.

**Three obvious mechanisms were considered and rejected, and the reasons are
worth keeping** because each is what a reviewer will ask for. `ls -l` has no
pinnable format: busybox and GNU disagree on columns and on the date field,
GNU escapes a space in a name only behind a flag busybox lacks, a name
containing a newline splits into two rows that both look valid, and nothing
tells you which `ls` you got until after you have parsed it. `find -printf`
would be pinnable and is a GNU extension busybox does not build, so the shape
of the result would depend on the image. Tar headers are correct on names —
a binary format with length-delimited byte strings — and too expensive: `tar
cf -` recurses and streams file bodies, and skipping a body over a network
stream still reads it, so listing a directory of large files would pull
gigabytes to learn a dozen names.

So the script uses only what is guaranteed. Types come from the shell's own
`test`, with **`-L` asked before `-d`** — `test -d` follows a symlink, so a
link to a directory is reported as a directory unless you ask the other way
first, and the interface would then navigate into it as one. Names are printed
with `printf` and **never `echo`**, because dash's `echo` interprets backslash
escapes and would rename a file called `a\tb` by the act of listing it. Sizes
come from `stat -c '%s %Y'` and only after the exact invocation has been
probed, because a `stat` on PATH is not a `stat` that takes `-c`; where it is
absent the listing reports **no size at all** rather than a nought, which
would read as an empty file.

A container with no shell is `ports.ErrShellMissing` — the third sibling of
`ErrTarMissing`, and the same kind of fact: a distroless image has no `sh`,
nothing is wrong, and copying still works because it needs only `tar`.

**The listing is never persisted and its entries are never logged.** Names
inside somebody's container are object-adjacent data: `auditListing` records
the cluster, namespace, pod, container, path and entry COUNT, and no name;
nothing reaches `settings.json`, history or the timeline. The browser also
runs nothing on open and nothing on a tick — every listing is one press, the
rule the Helm page and the reachability probe already follow.

The one write it offers is the upload that already existed, aimed at the
directory on screen rather than at a path somebody typed — which is the whole
reason to offer it there, since not knowing the path is why anybody opened the
browser. It is the same call with the same limits and the same refusals; the
browser adds a destination and nothing else. A transfer begun from the dialog
is handed back to the pane behind it, direction and both paths included, so
ONE state machine renders every copy: a second renderer in the dialog would be
free to disagree with the first about whether a copy had finished, and a
dialog that started a transfer and only closed left one running with nothing
on screen and swallowed its failure.

**It is refused on a read-only cluster while the download beside it is not**,
and that asymmetry is the deliberate reading of what the mark means: the guard
tracks whether an arbitrary shell RUNS, not whether bytes move. A download is
a fixed `tar` argv; a listing is `sh -c`, which is the subresource a terminal
uses and appears in the audit log as one.

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

## The Helm page is built from labels, and reads a payload only when asked

`podsteer/helm` is the sixth pinned pseudo-entry, beside the overview,
Applications, All clusters, the RBAC explorer and the timeline, and it is one
for the reason they are: a Helm release is not an object anything can GET by
that name — it is a set of Secrets Helm labelled — so it is deliberately
absent from `domain/catalog.go`. The Secrets themselves are ordinary
catalogue entries and stay where they are. Decided in
`podsteer/business-docs` decision 6, which is the Secrets doctrine (decision
3) APPLIED rather than an exception to it.

**A release list does not need a release payload.** A release lives in a
Secret of type `helm.sh/release.v1` holding a base64'd gzip'd JSON blob of
roughly a megabyte, and reading forty of those to render a list of forty names
is the bulk Secret read operators alert on. Helm labels every Secret it writes
(`newSecretsObject`, `pkg/storage/driver/secrets.go`) with `owner=helm`, the
release `name`, the `version`, the `status` and `createdAt`, adding
`modifiedAt` on an update — so `Adapter.ListHelmReleases`
(`app/adapters/k8s/helm.go`) lists them through the **metadata client**,
exactly as `Adapter.APIWriters` does, and **not one byte of a Secret's data
crosses the wire**. The label selector `owner=helm` is primary; the
`type=helm.sh/release.v1` field selector rides alongside and is worth
understanding for what it buys: it cuts wire bytes and cuts the API server
NOTHING, since the server reads and decrypts every Secret in scope before
filtering.

**The metadata client narrows the response, not the verb.** To RBAC this is
`list secrets` like any other, so the page is unreadable for a real part of
the audience this project is built for — that is a limitation of the design,
not an edge case, and it carries a wording requirement: **the entry must never
read as "no Helm here" when it means "not permitted here"**. Hence
`domain.HelmListStatus` has `listed`, `forbidden` and `failed` and
**deliberately no "absent"**: a cluster with no releases is LISTED with zero
rows, which is what keeps the two distinguishable. `helmCache` is the one
per-cluster cache here that stores a **refusal WITH its error** rather than as
an empty answer — the collapse `backendCache` makes deliberately is exactly
what this must not, and `vulnerabilityCache` no longer makes it at all: a
security signal cannot let "not read" and "nothing found" share one answer,
so it carries a status instead — and `showsEmptyCopy`
(`web/src/lib/helm.ts`) is the guard that keeps a call site from testing
`releases.length === 0` and rendering the zero-row copy to somebody who was
simply not allowed to look.

**The list is off the refresh tick entirely.** `ClusterSession.#fetch` has a
case for this view that fetches nothing: a metadata LIST of Secrets every ten
seconds is six `list secrets` lines a minute in an operator's audit log for as
long as the page is open — the doctrine's own signature with the bytes removed
and the pattern intact. It is cached five minutes (`helmCacheTTL`, modelled on
`upgradeCache` and NOT on `readcache.go`, whose two-second window exists for
the tick), refreshed on exactly three events — opening the page, the page's own
Refresh (which passes `refresh: true` and bypasses the cache), and a write
PodSteer made, since `helmCache.forget` is wired into `forgetReads` as well as
`Invalidate` — and the page shows an "as of" time beside that control, because
a cache that cannot say its age is one that lies.

**`HelmPort` is a new outbound port rather than a widening of `ResourcePort`,**
and the reason is `podsteer mcp`: it narrows by INTERFACE, so a list tool might
one day be offered while a payload read never is, and the two have to be
separable at the type level. It now carries both methods, which is what makes
that separability something to keep rather than something to assume — see the
payload section below.

**The LIST ships without a CHART and an APP VERSION column**, which is the
record's explicit and stated cost rather than an oversight: neither is a label,
both live only inside the payload, and filling them on a page load would be the
bulk read this exists to refuse. The page says so where the columns would have
been, and points at the release pane, which is where they do appear.

### Reading one revision is the second act, and it is `RevealSecretKey`'s

`HelmPort.ReadHelmRelease` / `HelmAPI.ReadRelease` decodes ONE revision of ONE
release, from a click handler and from nowhere else — never on render, never
when the drawer opens, never on the tick. It reads a Secret's contents, so it
inherits ADR 3's controls verbatim rather than a summary of them, and one
audit line in `HelmService.ReadRelease` names cluster, namespace, release and
revision and never a value. **All the Helm-format knowledge stays in the
adapter** (`app/adapters/k8s/helm_payload.go`, beside `helm.go`): the derived
object name, Helm's own base64 layer, the gzip sniff and the release document.

Five things there are load-bearing:

- **The Secret is SHAPE-CHECKED before it is decoded**, and the wording there
  matters because it is easy to overclaim. Its type must be
  `helm.sh/release.v1` and its `owner`, `name` and `version` labels must match
  the request. This is **not a boundary against an attacker** — whoever can
  create a Secret at the derived name can set its type and labels too — and
  what makes a hostile document merely a document is the bounded
  decompression, the narrow unmarshal target and the masked manifest. What the
  check does buy is that an object merely SITTING at Helm's naming shape (a
  backup, a hand-made copy, a restore under the wrong name) is refused rather
  than rendered as a release it is not. `owner=helm` is checked because the
  LISTING selects on it, and without it an object the list cannot see would
  still be readable here — the two acts must agree about what a release is. A
  failure is `ErrHelmPayloadUnreadable` and never `ErrNotFound`: the object is
  there.
- **The limit is 32 MiB and the reader is given limit+1**, so reaching it
  exactly succeeds and exceeding it is observable. Exceeding it REFUSES
  (`ErrHelmPayloadTooLarge`) rather than truncating — etcd bounds the
  compressed release only, gzip expands by three orders of magnitude, and a
  manifest rendered as whole when it is short is worse than no manifest. The
  boundary is a table test on both sides.
- **The unmarshal target is a NARROW struct.** A release payload carries the
  whole chart — `chart.templates` and `chart.files` — and nothing displays
  them, so no field names them and `encoding/json` walks past.
- **A RENDERED MANIFEST IS SECRET MATERIAL, and the decision record missed
  it.** A chart that renders `kind: Secret` puts base64 `data:` into the
  manifest string, and base64 is an encoding rather than a cipher — the
  doctrine's own words — so `maskSecretDocuments` splits the manifest on
  document boundaries and masks each v1 Secret through the same
  `maskSecretData` the YAML tab uses, in the adapter, before the string
  crosses any boundary. **A v1 `List` or `SecretList` is walked and each
  Secret in `items[]` masked and counted**, because a chart templating several
  Secrets from one `range` emits exactly that and both kubectl and Helm's kube
  client expand it — matching only a bare Secret let one straight through with
  its base64 intact while the count said nothing was hidden. Nesting is
  bounded at `helmListNestingLimit`, and hitting the bound **withholds the
  document** rather than passing it through: "I did not finish looking" and
  "there was nothing to hide" must not produce the same output. **A document
  that fails to parse is passed through unchanged** (it is text, not a
  Secret), and so is anything that is not one of those shapes — only a masked
  document is re-serialised, so an untouched manifest is byte-identical,
  comments included. Because it arrives masked it needs no reveal timer.
- **`maskSecretData` masks a non-string scalar too**, which it did not before
  this change — it used to `continue` past anything that was not a Go string.
  That was a hole in the Secrets YAML tab as much as here, and the Helm pane
  is what surfaced it: the API server rejects a Secret whose value is not a
  string, but Helm writes its release Secret BEFORE applying what it rendered,
  so a `failed` revision's manifest routinely carries the object the server
  refused — and an unquoted `stringData: {pin: 483920}` parses as a number. A
  map or a list is still left alone, deliberately: it is not a value shape at
  all, and a byte-count placeholder over it would claim something nothing
  measured.
- **Values and notes DO need one**, and notes are not the milder half: a NOTES
  template is rendered from the same `.Values` and printing an admin password
  is one of the commonest things it does.

**The reveal discipline has ONE implementation.**
`web/src/stores/revealHolder.svelte.ts` owns the thirty-second timer, the
re-hide and the window-blur clear, and both `secretReveals` and
`helmPayloads` use it. Two copies would drift in the way nobody can see — a
pane whose timer has become "never" looks identical to one that has not, until
a value is still on screen in a recording. **Hiding forgets rather than stops
rendering**, so showing a value again costs another audited read;
`helmPayloads` splits the payload accordingly, holding the masked manifest and
the chart identity for the drawer and only the values and notes under the
timer, and dropping everything when the drawer closes.

**Every reveal takes a GENERATION TOKEN before its await and checks it after**
(`RevealHolder.claim`/`isCurrent`), and this is not defensive decoration — it
closes a live hole in the blur rule. Reveal a value, alt-tab, and the blur
handler empties the holder while the read is still in the air; the promise
then resolves and writes the material straight back, revealed and under a
fresh thirty seconds, in a window nobody is looking at. Emptying is not enough
on its own. The token is per KEY as well as global, which is what stops the
Helm pane resurrecting a revision it moved away from mid-read — two revisions
held at once, the abandoned one's values under a timer no control could reach.
`HelmView.load` already guarded the LISTING this way; the stores now do the
same for the material.

**Leaving the page drops the payload too, not only closing the drawer.**
`HelmView` sits inside an `{#if}` on the view mode, so switching to Pods with
the drawer open destroys the component without `close()` running — a teardown
`$effect` calls `forgetAll` so the SECURITY.md sentence about dropping the
payload when the drawer closes is true rather than nearly true.

**The payload reaches nothing that persists.** Not the timeline recorder (which
records writes, and this is a read), not a CSV export (which renders the
listing, built from labels), not disk. `helmPayloads.test.ts` asserts the first
two with a sentinel, and `dto_helm_test.go` asserts the payload DTO's field set
against a LITERAL list — the guard `notification_api_test.go` and
`settingsFile.test.ts` already use, here because this is the one DTO carrying
Secret material.

**The MCP guard names the payload read.** `tools_test.go` already asserted no
reachable interface declares `RevealSecretKey` or `InspectTLSSecret`;
`ReadHelmRelease` joins them, and the check is now made by walking every
interface-typed field of `Deps` rather than a hand-listed set — so a future
`HelmReader` offering the LIST (which reads no Secret contents at all) cannot
quietly acquire the payload read by being handed `ports.HelmPort` whole. That
separability is why `HelmPort` exists as its own type.

**Rollback and uninstall are NOT performed** (I2). A Helm rollback re-renders a
revision, diffs it, applies the difference, prunes and writes a fresh release
Secret; re-implementing that means re-implementing Helm. Shelling out was
refused too — starting a program on somebody's machine as a side effect of
opening a page is a commitment made only through the terminal pane they opened
themselves. So `helmRollback`, `helmUninstall` and `helmHistory`
(`web/src/lib/kubectl.ts`) compose the commands and `KubectlHint` shows them
under a `label` prop, since heading a `helm` command "kubectl equivalent"
would claim both the wrong binary and the wrong relationship.

**Flux's releases are not always in the HelmRelease's own namespace.**
helm-controller writes to `spec.storageNamespace` when set, and
`effectiveReleaseName` (`web/src/lib/gitops/flux.ts`) composes Flux's own
documented `[targetNamespace-]name` default, because looking a release up
under a name nothing wrote reports no release for a healthy HelmRelease.

**The Argo CD sentence comes from discovered KINDS, never from annotations.**
`gitops.ts` reads `argocd.argoproj.io/tracking-id`, an annotation, off one
object's manifest — and annotations do not ride list rows, so that signal is
unavailable here. `servesArgoApplications` reads `argoproj.io/Application`
out of the kinds the session already holds, costs nothing, and claims only what
it can: Argo CD is installed here, not that any workload is managed by it.

## Two structural facts that look like mistakes

**`main.go` sits at the repository root.** It is a three-line shim; the real
entry point is `app/cmd/main.go`, which is `package cmd`.

**That was forced under Wails v2 and is now a convention.** The v2 CLI ran
`go build` with its working directory set to the project root and no package
argument, and exposed no setting to point it elsewhere. v3 does not build the
application at all — `make build` is `go build` and `wails3 generate bindings`
takes an explicit package pattern — so nothing now requires the `main` package
to be at the root. Moving it would mean renaming `app/cmd` to `package main`
and repointing the Makefile and CI, which is a separate change with its own
diff; until somebody makes it, the shim's comment says why it is there.

`cmd.Main` still reads its arguments the way it does, but the reason has
narrowed. Under v2 it was mechanical: binding generation compiled and RAN this
binary argument-free, so anything else on that path took the build down. v3
generates bindings by reading the SOURCE, so that consequence is gone and what
remains is the product rule — **a bare launch must always be the window**,
because a double-click, a Dock icon and `brew install --cask podsteer` all run
this binary with nothing after its name. A subcommand is additive and is
reached only when the first argument names it. `route` is split out of
`dispatch` precisely so `main_test.go` can assert that without starting a
window, which is the one thing a test of this path cannot do.

**Vite builds into `app/adapters/assets/dist`.** Go's `//go:embed` cannot
reference a parent directory, so the bundle has to land beside the package that
embeds it. Frontend *source* stays in `web/`; only build output crosses over.
See `web/vite.config.ts`.

That directory's contents are git-ignored except a tracked `.gitkeep`, because
`//go:embed all:dist` will not compile if the directory is missing from a fresh
clone. `emptyOutDir` deletes the placeholder on every build, so a small Vite
plugin (`podsteer:keep-embed-directory`) rewrites it — do not remove it.

**The frontend is still built before the Go build, for ONE reason rather than
two.** `//go:embed all:dist` will not compile against an empty directory, so
`build` depends on `web-build` in the Makefile and CI builds the frontend
first. The second reason is gone: `wails3 generate bindings` reads the source
and executes nothing, so `bindings` needs only `embed-stub` — a placeholder
index.html — where under v2 it needed a real bundle because generation started
the application and `assets.FS()` refuses to let one start without one.

Do not "fix" the check in `app/adapters/assets/assets.go` by softening it —
it is what turns "compiled without a frontend" into a startup error instead of
a blank window nobody can diagnose, and that is as true when the asset server
is v3's `application.AssetFileServerFS` as it was under v2's.

**There is one window, and it is NAMED.** v3 is a multi-window framework:
`app.Window.Current()` answers with the FOCUSED window, which is none at all
when the application is hidden or minimised — exactly the state a notification
click or a second launch is trying to leave. So the window carries
`wails.MainWindowName` and `App.RaiseWindow` looks it up by name. Genuine
multi-window support is now possible for the first time and is deliberately
not built here.

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

- **The sampler has one owner and one way to stop** (`Close`), and it waits for
  the write in flight before returning. It is started from the
  `events.Common.ApplicationStarted` hook — v3's shape of what v2 called
  `OnStartup` — and stopped from `Options.OnShutdown`. It is emphatically
  **not** the only long-lived goroutine — this file claimed that for a long
  time and it was never true — see the rule below.
- **Retention lives in Go, and it is an instance of the general rule.** A
  setting is BACKEND-OWNED when the Go process must act on it before or without
  a window, or when it decides what reaches disk or the network; everything
  else stays in the webview's own storage. The test for a new one is: *if this
  process had no webview — `podsteer mcp`, a sampler tick before any pane has
  loaded, the cluster picker on launch — would the setting still have to
  exist?* Retention answers yes twice over, which is why it is here. A column
  width answers no to both, which is why it is not. **Object names stay out,
  with exactly one named exception**: the snoozed findings and the per-cluster
  namespace filter keep their home in localStorage, because moving them into a
  file this process writes would break the claim SECURITY.md makes about that
  file. The exception is `domain.PreferredBackend` — the namespace and Service
  name of the monitoring backend an operator explicitly picked (ADR 7), which
  is disclosed in SECURITY.md, in the readme the store writes into the file and
  in the domain comment, written only when the pick differs from the ranked
  default, and asserted absent otherwise by a test in the settings store. It is
  ONE field; anything else wanting to hold an object name is a new argument to
  be had in the open. Zero retention means record nothing *and* erase what
  exists — an operator choosing it means both.
- **A sample is derived from the overview**, not from a second read of the
  cluster, so the chart and the numbers above it can never disagree.
- Samples hold capacity figures only: no object names, no logs, no manifests.

## There are TWO settings files, and they are unrelated

`app/adapters/settings` writes **`os.UserConfigDir()/PodSteer/settings.json`**,
the settings the GO PROCESS acts on. `web/src/lib/settingsFile.ts` writes the
**exported** document an operator saves and sends to a colleague. They share a
`_readme` header and nothing else: the kinds differ deliberately
(`PodSteerBackendSettings` against `PodSteerSettings`) so one dropped in as the
other is refused rather than misread, the backend file is not part of the
export, and the export never reads it.

The backend file is not new so much as **absorbed**: `history.json` has been
written beside the history directory since retention became configurable — two
integers, a plain `os.WriteFile`, no version, no kind — and SECURITY.md already
disclosed it. `Store.adopt` reads it once on a first run, writes the new
document, and only then removes it, because v0.2.0 shipped it and an operator
who set retention to zero must not find recording back on after an upgrade.

Five things about it are load-bearing:

- **Its path comes from `os.UserConfigDir` directly, never from the history
  directory.** That dependency ran the wrong way in `main.go` and is reversed:
  history is state, settings are configuration, and on Linux those are meant to
  part company. Only `app/cmd/settings.go` knows both paths, which is why the
  legacy file is passed in as `Options.AdoptFrom` rather than derived inside
  the store.
- **One value under one mutex, and there is no `Save`.** Every mutation is
  `Update(ctx, func(*domain.Settings) error)` doing the read, the change, the
  validation and the whole-document atomic write inside the lock. A `Save`
  taking a whole value would let two callers each read, each change a different
  field, and the second silently discard the first — which is exactly what the
  old two-integer file did, invisibly, because nothing else was in it yet.
- **`Normalise` never fails; `Validate` refuses — and `Update` runs Validate
  FIRST.** Normalise is the read path: a hand-edited value falls back to its
  default, is counted, and PodSteer starts. Validate is the write path: a bad
  value from the interface is a bug worth not persisting. Running Normalise
  first would repair the value out from under Validate and make every refusal
  unreachable.
- **A malformed file is set aside, never repaired and never overwritten**
  (`settings.json.invalid-<unix>`, stamped so a second bad edit does not
  clobber the first), and an unknown top-level SECTION round-trips verbatim
  through `document.Unknown` so an older build cannot erase a newer one's. That
  protects against an added section and NOT against a field that moved between
  sections, which is why a file declaring a **higher version is read and never
  written back** (`ports.ErrSettingsFromFuture`, one line in Settings).
- **`podsteer mcp` opens it read-only**, and `app/cmd/main_test.go` asserts
  that the whole MCP composition leaves the configuration directory
  byte-identical. That is what keeps SECURITY.md's "nothing is written
  anywhere" literally true rather than a thing everybody has to remember.

Consumers take narrow interfaces at the consumer: `HistoryService` takes a
two-method `HistorySettingsStore`, not `ports.SettingsPort`, because the
sampler has no business being able to name the kubeconfig sources or the proxy.
**Order matters in the composition root**: the store is opened before the
Kubernetes adapter and before the history service, or the first sampler tick
runs under the defaults on a machine where recording was turned off.
## Every goroutine has an owner and one way to stop

There are many long-lived goroutines here, not one: the history sampler, the
watch sweeper, every reflector and its supervisor, every port-forward
supervisor, every exec and attach session, every local-shell pump, every log
stream and every file transfer. The rule is not that there is only one — it is
that **each has exactly one owner, one way to stop, and a stop that WAITS
rather than signals**, so a record and the goroutine behind it can never part
company. That is the sentence `portForwards`, `nodeShells`, the local-shell
`Manager` and `watchManager` each restate in their own words, and it is the
one to hold new code to.

**Shutdown teardown cannot rely on context cancellation, because nothing
cancels anything.** The framework's runtime context is never cancelled —
`App.OnShutdown` only clears the pointer to it — so a goroutine parked on
`ctx.Done()` at exit would park forever. Teardown is therefore explicit and
enumerated in `OnShutdown`: `StopAllPortForwards`, `StopAllNodeShells`,
`StopAllClusterShells`, `StopAllLocalShells`, `StopAllWatches`,
`historyService.Close()`. There is no
ambient cancellation to fall back on; a new owner that needs stopping needs a
line there.

**A sweep closes its registry, and a start racing one is refused.** Copying a
map and stopping what was in it leaves the window between the copy and a start
that finishes after it — and a node shell is the sharp case, because starting
one waits up to a minute for a privileged pod to schedule and pull, and
nothing cancels that wait. So `nodeShells`, `clusterShells`, the local-shell
`Manager` and `watchManager` each carry a `closed` flag their sweep sets: a
start finding it set deletes its pod (or kills its process) and returns an
error rather than registering into a map nobody will read again.

**A port-forward goes with its connection, not just with the process.**
`Adapter.Invalidate` stops that cluster's forwards and waits for them, FIRST,
before it drops the client or forgets the watch — so disconnecting a tab ends
its forwards everywhere, the same rule `Registry.Close` already follows. That
ordering is load-bearing rather than tidy: a supervisor whose pod has died
lists pods every three seconds for two minutes looking for a replacement, on a
transport it built itself and that invalidating the client does not touch, and
that list rebuilds the client, re-executes the credential plugin and ensures a
fresh watch set. It is the resurrection the client-first ordering exists to
prevent, arriving through a door that ordering does not cover.

The search itself takes the **unexported** `listPods` as defence in depth, so
it can never ensure a watch and a cancelled search actually stops. That second
half matters because `readcache.go` detaches its shared fetch from whoever
started it: through the exported `ListPods`, a search already in flight when
the stop landed would outlive its own cancellation and could rebuild the
client behind `Invalidate`'s back. The coalescing given up is worth nothing
here — the search runs once every three seconds, longer than `readTTL`.

**Three things are deliberately NOT swept at shutdown, and die with the
process**: terminal sessions (exec, attach, debug), log streams, and file
transfers. They hold a stream to the API server and nothing in a cluster, so
the process exiting is a complete teardown — with two exceptions that are
already handled elsewhere: a node-shell attach session deletes its pod on
exit and `StopAllNodeShells` covers the same pod from the other side, and an
in-cluster shell's session does the same against `StopAllClusterShells`. Do not
read `OnShutdown`'s list as "everything with a goroutine is stopped"; read it
as "everything that would otherwise leave something behind is stopped".

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

**It is in the frontend, and ALL THREE of its sources cost nothing on the
wire.** The assessment is fetched on every refresh whatever view is open and
carries the findings AND the events (`Overview.events`), a pod's findings ride
every row of the pod list (`Pod.findings`), and a write's outcome is resolved
in `web/src/lib/api/client.ts` before anything is recorded. So there is no
backend state, no goroutine, and no Wails event to wait on — the same trade
`usageHistory` makes with the measurements a list response was already
carrying. The timeline records nothing that had not already crossed the bridge
for another reason, and that is a property to keep rather than a description
of how it happens to work today.

**Events were the exception, and treating them as free while recording them
from a PAGE was a bug rather than a saving.** They were recorded only from a
view that had fetched them — for a while the Events page alone, then the
Timeline page as well — so the record a cluster produced was a function of
which pages somebody had visited. Two things were wrong and only one of them
was visible: the Timeline page opened empty, and, worse, THE NAVIGATOR'S COUNT
STAYED AT NOTHING on every other view, so a tab recording nothing looked
identical to one with plenty to show. Giving the Timeline page its own event
fetch fixed the page and left the count exactly as wrong, because nothing
recorded events unless one of those two pages was open.

**So the events ride the assessment.** `domain.OverviewInput.Events` was
already gathered on every tick whatever view is on screen — the event findings
are derived from them — and simply never crossed the bridge. `domain.Overview`
now carries them, `Overview.events` carries them to the frontend, and
`ClusterSession.#adopt` records them beside the findings, from the same
assessment, on every tick. `case 'timeline'` fetches nothing again, and the
Timeline page is once more a view over a record rather than a reader with a
request of its own.

Three things about that crossing are load-bearing:

- **The DTO is a NARROWING, not the Events page's row.** `TimelineEvent`
  (`app/adapters/wails/dto_overview.go`) carries the eight fields the recorder
  reads — the event object's namespace and name for its identity, the API
  server's own `count`, `isWarning`, the reason, message, involved kind and
  involved name — and drops labels, annotations, `involvedObject`, `source`,
  `type` and the three time fields. All of that is dropped because nothing
  reads it, and it is worth dropping because `Event` crosses only while
  somebody is on the Events page whereas this crosses on EVERY tick on EVERY
  view. Order of 280–400 KB per tick at the adapter's 1000-event cap, roughly
  40% less than the full row would be, against the 6–13 MB a tick already
  costs on a 5,000-pod cluster. That is the price of the fix, and it is paid
  on every view rather than on one.
- **Nothing is capped on the bridge, deliberately.** The only bound is
  `eventListLimit` in `app/adapters/k8s/workload.go`, which the Events page and
  the event findings are already subject to. A tighter cap here would be an
  entry the timeline never saw and could therefore never show, and the panel's
  line about what it covers would become false rather than merely brief.
- **The Events page still files the rows it fetched**
  (`ClusterSession.#recordTimeline`). Its read is namespace-scoped where the
  assessment's is cluster-wide against that same per-query cap, so on a cluster
  busy enough to hit it the page open on one namespace sees events the
  cluster-wide read truncated away. Recording both is a superset of either, and
  an event already recorded is upserted rather than duplicated.

**An empty event list is not evidence that nothing happened.** A cluster whose
events this account may not list produces exactly the same empty list as a
quiet one, and `Overview.unavailable` naming `events` is the only thing that
tells them apart — the same distinction the overview's "assessed without …"
line already draws. `ClusterSession.#adopt` passes it to
`timeline.noteEventSource` on every assessment, and `TimelinePanel` says so
next to the line that already states what the timeline covers. Recording it on
every assessment rather than only on a refusal is what makes an account that
regains the permission stop being warned on the next tick instead of for the
rest of the session.

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

## A new critical finding can reach the desktop, and one diff decides it

A new warning or critical finding plays a motif (`$stores/alerts`); a new
CRITICAL one can also post an OS notification, through Wails' own notification
support (`NotificationAPI`, `App.StartNotifications`). Both are raised from
`ClusterSession.#adopt`, from **the same diff**, and that is the load-bearing
part: `#adopt` used to hand-roll a second comparison over a `Set` of ids
beside the one the session timeline already ran, and two differs mean two
baselines. They drift, and then the timeline records a finding appearing on
one refresh while the notification announces it on another — the same event,
two instants, and no way to tell which is right. So `#adopt` now calls
`diffFindings` (`web/src/lib/timeline.ts`) and hands `diff.appeared` to the
sound, to the notification and — as the whole assessment, because it also
records what CLEARED — to the timeline. Adding a third consumer means reading
that diff, never computing another.

Everything about **whether** one is posted is in `web/src/lib/notify.ts`, pure
and with a test per rule, because each of them is the kind that is invisible
when it goes wrong: critical only (a sound is over in half a second, a
notification sits in a tray until it is dismissed), snoozed findings never,
one notification per batch naming the count, and at most one per cluster per
`NOTIFY_COOLDOWN_MS`. **Off by default**, the same choice `alertSoundsEnabled`
makes and for one more reason it does not have — on macOS the first
notification triggers a system permission prompt, so `NotificationAPI.Request`
runs when the operator turns the preference on and never at startup.

Two rules there are worth not re-deriving. **A failed refresh is `null`, not an
empty assessment** — the trap `diffFindings`' own comment describes, and the
reason `#refreshAssessment` tells the timeline explicitly rather than by
silence. **A PARTIAL refresh is the same trap pointed the other way**, and it
is what `sourcesAreComparable` exists for: `Overview.unavailable` names the
sources this assessment could not read, and a source that was missing last
refresh and answered this one hands over every finding it produces in the same
instant. None of them is new. So the two assessments must have read the same
source SET to be compared at all — deliberately not "the current one is
clean", which would permanently mute every cluster without metrics-server.

**A notification is a write, and is fenced like one.** macOS keeps delivered
notifications in Notification Centre, which is on disk, and a Linux
notification daemon may log what it showed — so the no-object-names commitment
SECURITY.md makes about files holds here in full. What travels is a COUNT, a
finding's TITLE (a rule's own name, written in this repository) and the
kubeconfig CONTEXT NAME, which travels on exactly the terms the settings file
lets one travel. `Finding.summary` and `Finding.subjects` are deliberately
never read. `NotificationRequest` has no field for a namespace or an object
name and `notification_api_test.go` asserts its field set against a LITERAL
list, the way `settingsFile.test.ts` does for the export, so one cannot join
it without somebody arguing for it. Do-not-disturb is not checked and cannot
be: macOS Focus, Windows Focus Assist and a Linux daemon's quiet mode all
apply after delivery and none is readable through any API Wails exposes, so
PodSteer posts and the platform decides whether to present — guessing would
mean guessing wrong in the direction that silences an alarm somebody asked
for.

**The platform service is started by hand and never registered**, and that is
the v3 form of a rule that predates it. Wails v3 ships notifications as an
ordinary SERVICE (`v3/pkg/services/notifications`), and a registered service
has every exported method bound — which for this one would put
`RemoveAllDeliveredNotifications` and the rest of its management surface
within reach of the page. So `App.StartNotifications` constructs it, calls its
`ServiceStartup` directly, keeps the handle, and leaves it out of
`Options.Services`; `NotificationAPI` exposes three methods and decides
nothing. Under v2 the same rule kept `InitializeNotifications` and
`CleanupNotifications` off the bound struct, for the identical reason: Wails
binds every exported method of what it is given.

**"Supported" now means the service started.** v2 had `IsNotificationAvailable`
to ask; v3's platform backend reports the same thing by FAILING its own
startup — no notification centre, or, on macOS, no bundle identifier because
the binary is running outside a `.app`. That last case is why `make dev`
builds `PodSteer.dev.app` rather than running a bare binary: a development
build that reported notifications as unsupported could not exercise this
feature at all.

Clicking one raises the window in Go — before the event is emitted, so a tab
never switches behind a hidden window — through `App.RaiseWindow`, which the
single-instance callback shares. `App.svelte` then focuses that cluster and
opens its overview.

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
  The Linux entry carries no build tag any more: v2 defaulted to webkit2gtk
  4.0 and the tag chose 4.1, which changed which files — and therefore which
  imports — were in scope. v3 targets 4.1 directly.
- **Build scope is MEMBERSHIP, not cache presence.** A Go module is build-only
  when `go list -deps -test` reaches it on some release platform and no
  binary links it (`build/licences/scope.mjs`, pure and unit-tested by
  `node --test build/licences/`). It used to mean "in the local module cache",
  which made the gate's verdict depend on what a machine had downloaded and
  flipped in CI with the shared `setup-go` cache. Modules the graph mentions
  that nothing reaches are counted, never classified.
- **A licence the classifier does not recognise blocks the build**, which
  makes a false negative in `build/licences/classify.mjs` as expensive as a
  false positive. ISC has two published wordings, and matching only the newer
  one sent `coder/websocket` — perfectly ordinary ISC, an ALLOWED tier — to
  human review. The signature list is ordered and each entry is load-bearing;
  read the comments there before adding one.
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
make dev        # wails3 dev — Vite for the frontend, Go rebuilt on change
make build      # packaged application into build/bin
make package    # the same, minus the frontend build and the licence gate
make test       # go test -race ./...
make check      # gofmt + go vet + svelte-check
make bindings   # regenerate TypeScript bindings after changing a bound method
make icons      # re-render the packaging icons from build/appicon.png
make notices    # regenerate the licence inventory and enforce the policy
make sbom       # emit a CycloneDX SBOM into build/bin/sbom
```

`make dev` runs `wails3 dev` against `build/config.yml`, whose `executes` are
this project's own targets rather than the v3 template's tasks: `dev-build`
compiles the Go side (and, on macOS, the `.dev.app` around it), Vite serves the
frontend, and `dev-run` launches the binary. `wails3 dev` exports
`FRONTEND_DEVSERVER_URL`, which is what points the webview at Vite instead of
at the embedded bundle.

**NOTHING ELSE REGENERATES THE BINDINGS.** Under v2, `wails dev` and
`wails build` did it as a side effect; v3's build is a plain `go build`, so
`make bindings` is the only thing that writes them and a forgotten
regeneration is a stale frontend contract until CI says so. Run it whenever a
method on a bound service or a DTO in `app/adapters/wails/` changes. The
generated output in `web/src/lib/bindings/` is **committed**, the frontend
reaches it through the `$bindings` alias, and the `bindings` CI job fails on
any drift.

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

Two deviations, one of them now inherited rather than forced:

- **`go.mod` is at the repository root, not in `app/`.** Wails v2's CLI
  compiled the root package, so the `main` package had to live there — and it
  had to be inside the module. v3 imposes neither, so this is now a layout
  nothing enforces and nothing has moved; see "Two structural facts that look
  like mistakes". CI uses `go-version-file: go.mod`, not `app/go.mod`.
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

**A CLOUD CLI PODSTEER DRIVES IS A FOURTH KIND OF DESTINATION, and the honest
statement is not the literal one.** Since decision 12, the Add cluster dialog
can run a cloud CLI the operator already has — to list the clusters it can see
and to have it write a kubeconfig entry. PodSteer's own process still contacts
nothing new: it starts a program, reads its stdout, and never speaks to a
provider. But that program contacts `eks.<region>.amazonaws.com`, or
`management.azure.com`, BECAUSE A BUTTON IN PODSTEER SAID SO, and defending
"PodSteer contacts nothing new" on that technicality is how a list like this
stops being trusted. It is stated here rather than argued.

What keeps it bounded is in `app/adapters/vendorcli`: nothing runs without a
press, the binary comes from PATH and from no setting, PodSteer passes no
region, subscription or project of its own, and the CLI writes to a temporary
kubeconfig PodSteer owns and deletes rather than to the operator's. The
provider table is `app/domain/vendorclis.json`, embedded at build time and
deliberately NOT operator-editable: a table naming programs to run must not be
a file anything can rewrite.

**`podsteer mcp` adds nothing to that list**, and the section above says why:
it speaks to its parent process over stdio and to the same API servers this
one does. A change that gave it a listener, or any other transport, would be a
change to this list and needs to be argued for here.

The webview's own policy is tightened at BUILD time, not in `index.html`:
`connect-src 'self' ws: wss:` is what Vite's hot reload needs, and a bare
scheme source permits a WebSocket to any host. A Vite plugin strips it from
the shipped page and `app/adapters/assets/csp_test.go` asserts the result on
the embedded bundle, so dev keeps what it needs and the two cannot drift.

**`connect-src 'self'` is load-bearing under v3 in a way it was not under v2**,
and it still says what it said. v2's bridge was JavaScript injected into the
page; v3's is an HTTP request to `/wails/runtime` on the asset server, which is
the page's OWN origin. So the shipped policy permits exactly one destination —
this process — and nothing else, which is the same commitment expressed against
a different mechanism. Anything that moved the IPC to a different origin, or to
a WebSocket transport (v3 offers one), would need this line reopened.

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

**A container REGISTRY is the destination most recently declined**, and it is
worth recording so the question is not re-derived. The image pane
(`app/domain/imagereport.go`) would show layers, creation commands, the
entrypoint and the labels if it read an image's manifest and config blob, and
those are only in a registry. That is a new outbound host per image an operator
opens — plural, arbitrary, and third-party — and for a private image an
authenticated read whose credential is a pull Secret in the cluster, which the
Secrets doctrine says is read on an explicit per-key request and never as a side
effect of opening a pane. Even a strictly anonymous, per-image, off-by-default
read is a destination this file's first sentence does not list, so it needs a
decision recorded in `podsteer/business-docs` and an amendment here and in
SECURITY.md — the same bar the update check cleared. What shipped instead is
everything Kubernetes already reports, with the pane stating what it did not
look at.

**The monitoring-backend read adds no destination either** (ADR 7). A
discovered Prometheus or VictoriaMetrics is reached through the API server's
own service proxy on the tab's credential, so it is one more request to an API
server this list already names — which is exactly why a typed URL is refused
outright and why the operator picks from what discovery FOUND. What it does add
is a third party that logs the expressions PodSteer composed, and an audit line
per call; both are disclosed in SECURITY.md, and both are why it is off until
switched on per cluster.

**The reachability probes add no destination at all**, deliberately. The local
vantage reaches a cluster only through the API server named in the kubeconfig —
its service proxy, or a port-forward tunnelled through it — and the in-cluster
vantage opens no socket from this machine whatsoever. An Ingress's public host
is refused for exactly this reason; see the reachability section above.

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

**The in-app source list is the same read-side extension, made editable.**
`settings.json` holds an ordered list of `{path, kind: file|directory}`,
absolute paths only, and `client.go`'s `loadingRules` appends what they
contribute AFTER the environment's entries:

```text
Precedence = explicit-or-default chain  ++  PODSTEER_KUBECONFIG_DIR  ++  settings sources
```

**Environment first, always**, and the three reasons are worth keeping. (1)
client-go keeps the FIRST file's definition of a context name, so an in-app
source can never shadow a context the machine's own configuration provided.
(2) `Merge` writes `Precedence[0]`, so a source is *structurally* incapable of
being the write target — which is why there is no "write here" flag to offer
and no way to ask for one, and why
`TestMergeStillWritesTheExplicitFileWhenSourcesArePresent` is the load-bearing
test of this feature rather than any of the ones about clusters appearing.
(3) A packager's or an enterprise's variable beats the UI, the same precedence
`PODSTEER_UPDATE_CHECK=false` has over the toggle beside it.

A **folder** source is scanned by `kubeconfigFilesIn` — `kubeconfigDirFiles`
generalised to take a path — so the skip rules cannot drift between it and
`PODSTEER_KUBECONFIG_DIR`. A listed path that does not exist is **kept and
reported missing**, never dropped: a synced folder is routinely absent for the
first minute after a login. `current-context` is still never touched.

`Cluster.Source` carries clientcmd's `LocationOfOrigin`, so the picker can say
WHICH file contributed a context, and `SettingsAPI` derives from the composed
report which contexts an entry LOST and to whom. That computation lives in Go
because it is a statement about the merge rule, not about a component. The
local terminal picks the sources up for free through `KubeconfigFiles()`, so
its exported `KUBECONFIG` names the same files.

The pane deliberately offers no write target, no context editing or deletion,
no current-context control and no paste — the last is Add cluster, which parses
the paste, refuses a collision and backs the file up first.

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
  because Wails carries an error across as its `Error()` STRING and nothing
  else — v2 rejected with the bare string, v3 rejects with a `RuntimeError`
  whose message is that same string, and neither carries structure.
  `web/src/lib/api/errors.ts` parses it back. Changing the codes means
  changing both ends.
- **A nil Go slice or map arrives as `null`, and the v3 bindings say so.**
  v2's generator declared every list `T[]` and simply did not model it, which
  is how `Overview.unavailable` reached a component that dereferenced it. The
  v3 types are `T[] | null`, and `callList` in `web/src/lib/api/client.ts`
  normalises a list to `[]` at the seam so no view has to remember. A nested
  field on a DTO does not pass through it and is guarded at the reader.
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
  `BulkEvict`, `BulkRestart`, `BulkScale` and `BulkCordon` run the SAME
  function again before fanning the acting lines out over the single-object
  `ManagementPort` methods (bounded errgroup, no shared context), so what was
  reviewed is what runs. Unlike a drain, **a failure never aborts the rest**:
  one forbidden delete is a per-object result beside forty-nine successes,
  classified exactly as a single write's error would be, because a run that
  stopped halfway would leave the operator unable to tell from the list which
  rows were touched. The read-only guard runs once, up front, for the whole
  selection.
  **A pod list offers Evict BEFORE Delete, and there is deliberately no bulk
  Drain.** Eviction is the more correct default of the two for a page of pods
  for the reason the next bullet gives — a budget can refuse it — so
  `bulkActionsFor` (`web/src/lib/bulk.ts`) returns `['evict', 'delete']` for a
  Pod and the order carries the argument. A budget refusing ONE pod is that
  pod's own failed result, keeping `ports.ErrDisruptionBudget` all the way to
  `CodeDisruptionBudget` and the sentence naming the budget, beside the pods
  that did leave; the run itself succeeds, which is what lets an operator see
  WHICH pods a budget protected. `bulkCommand` returns null for an eviction
  and the dialog shows no kubectl line, because kubectl has no eviction verb
  and printing a delete in its place would name the one command that ignores
  the budget. A bulk drain was refused: draining several nodes at once can
  take a cluster down, and the per-node preview `PlanDrain` produces is the
  safety that would be lost — `parseBulkAction` refuses the verb outright,
  with a test saying so.
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
no advisory database and grades nothing — as a PAGED read per cluster and
namespace, cached ten minutes (`vulnerabilityCacheTTL`, between
`backendCache`'s thirty and `readCache`'s seconds) and forgotten when a cluster
is invalidated.

**It reads SERVER-RENDERED TABLE ROWS, not objects, and that is a 50× saving
rather than a preference.** Every number shown is already an
`additionalPrinterColumn` on Trivy's CRD (`Critical`/`High`/`Medium`/`Low`/
`Unknown` off `.report.summary.*`), so `readVulnerabilityRows` asks for the
same Table transform `ListTable` uses, with `includeObject=Metadata` for the
labels that name the workload. A row is 2–3 KB; a `VulnerabilityReport` with
its CVE list attached is 50–150 KB, and the previous read transferred those,
per namespace, per cache window. Columns are matched by NAME, case-insensitively
— a Table carries no jsonPath — and a report set with no `Critical`/`High`
column is an ERROR rather than zeros, because zeros would be a clean bill of
health for every workload in the namespace.

**Four ordinary outcomes leave rows undecorated and only one means "clean".**
`domain.VulnerabilityListing` carries which: `complete`, `truncated`,
`not-installed`, `forbidden`. This used to be one empty slice for all of them,
which was survivable only while the read had no ceiling it could hit; now that
it does, an absent mark had to stop meaning four things at once. `Complete()`
is the question every reader asks before treating an absence as an answer, and
`$stores/vulnerabilities.vulnerabilityReadFor` is its frontend half — a
truncated read puts a notice above the pod list, because that is the one case
where a row without a mark is a claim nobody earned. No scanner and no
permission are still CACHED, the same discipline `DiscoverMetricsBackend`
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

**The overview reports the privileges a spec takes, and every one is INFO.**
`securityFindings` (`app/domain/security_findings.go`) reads five things an
operator WROTE — privileged mode, a shared host namespace, a written
`allowPrivilegeEscalation`, a dangerous added capability, a UID pinned to 0 —
and reports nothing about an unstated field, because "the operator did not say"
is not "the operator chose the unsafe thing". `SeverityWarning` would mark the
cluster DEGRADED (see `grade`), and every real cluster runs privileged CNI, CSI
and monitoring agents that share host namespaces; a warning would therefore
paint every cluster permanently yellow for having a network plugin.
`TestSecurityFindingsNeverDegradeTheClusterVerdict` is what holds that.

Pods carrying `podsteer.io/purpose` are skipped: a node shell IS a privileged
pod sharing the host's namespaces, and reporting it would fire every time
somebody opened one. The exclusion is that label and nothing else — kube-system
is not an exemption.

**NOTHING IN THIS CATEGORY READS `pod.Spec.Volumes`.** hostPath and a mounted
docker.sock belong here and cannot be computed correctly today: `stripPod`
nils volumes, so the rule would be right on some clusters and silently blank on
whichever ones the watch happened to be serving.
`TestStrippingAPodChangesNothingThisApplicationReads` now carries a
securityContext and the three host namespaces in its fixture, so it fails if
anyone strips what these rules read — and it fails too if anyone adds a volume
read to `mapPod`.

**The image is what gets fixed, so each summary carries it.** `Repository` and
`Tag` are printer columns like the counts, so the read already had them; a
summary now names the artefacts it summed (`Images`, deduplicated, sorted,
"repository:tag" — NOT a digest, which is not printed). That is what
`workloadsRunningImage` (`$stores/vulnerabilities`) answers from, and the
report panel says "N other workloads in this namespace" when it is more than
one. Without it the same image in twelve Deployments is twelve rows with
identical counts, presented as twelve problems when it is one bump.

**And the panel says how much of a report can be acted on.** `fixableCount`
(`web/src/lib/operators/trivy.ts`) counts findings whose `fixedVersion` is
non-empty — a REQUIRED CRD field, so empty is Trivy's own statement that no fix
exists rather than a field the operator skipped — and a checkbox filters to
them. Counted from the vulnerability LIST rather than `report.summary`, which
is a deliberate departure from the rest of that file: there is no fixable count
in the summary to read, so the list is the only source, and counting it against
the same list the panel renders is what keeps the sentence and the rows in
agreement on a report the operator's config has capped.

## Kubernetes' own newer APIs get typed panels too, and three things bite

`web/src/lib/standardapis/` is a third family beside `gitops/` and `operators/`,
selected the same way — by API group AND Kind, through `standardPanelFor` —
and rendered by `StandardApiDetail.svelte` from the one manifest the drawer
already fetched. It covers Gateway API (`GatewayClass`, `Gateway`, and
`HTTPRoute`/`GRPCRoute` sharing one panel), Dynamic Resource Allocation
(`ResourceClaim`, `ResourceClaimTemplate`, `DeviceClass`) and the admission
policies (`ValidatingAdmissionPolicy`, `MutatingAdmissionPolicy` and both
bindings). It is PURE QUOTATION with no exception — not even the one the
operator panels name for a certificate's expiry.

**A route has no `status.conditions`, and that is the whole point of its
panel.** An HTTPRoute's status is `status.parents[]`, one entry per Gateway
that was asked to serve it, each carrying the controller that answered and
that controller's own conditions. "Accepted by this Gateway, refused by that
one" is a sentence only that shape can say, and the drawer's generic
Conditions section — which reads `status.conditions` — renders nothing at all
on a route. A Gateway's listeners nest theirs the same way, under
`status.listeners[]`, joined to the spec BY NAME and never by position: the
two arrays are not required to agree in order or in length, and a positional
join hands one listener another's attached-route count.

**`resource.k8s.io` is read by SHAPE and never by `apiVersion`.** The group has
been re-cut in nearly every release: the earliest versions named one
`resourceClassName` on the claim and recorded `resourceHandles`, later ones a
list of device requests, later still the request's own fields moved under
`exactly` with a prioritised `firstAvailable` beside it. Every field is read
from wherever its shape puts it, so a version nobody coded for renders as much
of itself as it carries; what an older version records and newer ones do not
gets a field of its own rather than being folded into a modern one it does not
mean. A consumer in `status.reservedFor` names a plural RESOURCE, not a Kind,
so only a core `pods` is resolved (to `Pod`) and offered as a link — every
other plural is quoted and left unfollowable, because a link on a guessed Kind
fails when it is followed.

**Two of the three groups had to be adopted to be reachable at all.**
`resource.k8s.io` and `admissionregistration.k8s.io` end in `.k8s.io`, so
`isKubernetesGroup` (`app/adapters/k8s/cluster.go`) hid them from
`DiscoverCustomKinds`, and nothing in `domain/catalog.go` covers either — the
kinds could not be opened, and a panel for them would have been dead code.
They join `adoptedGroups` beside Gateway API, which stretches that list's
original wording (Kubernetes-owned but installed by an operator) in the
direction it exists for: the suffix rule hides a group on the grounds that
every cluster has it, and both of these are behind feature gates most clusters
do not turn on. A catalog entry was the wrong mechanism precisely because it
pins ONE version, which is the thing `resource.k8s.io` will not hold still on.

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
in the shutdown hook, beside `StopAllPortForwards`), and it must appear in the
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

**The default images differ by one suffix, and the split is the point.**
All three are `docker.io/cloudresty/dockydeb`, pinned (`DEFAULT_DEBUG_IMAGE`,
`DEFAULT_NODE_SHELL_IMAGE` and `DEFAULT_CLUSTER_SHELL_IMAGE` in
`web/src/stores/preferences.svelte.ts`), and the debug and in-cluster ones are
the `-nonroot` variant while the node shell is not — the in-cluster shell takes
the debug default for the debug default's reason, since its pod lands in an
ordinary namespace that Pod Security judges. A debug container is injected into
somebody else's pod, in their namespace, so Pod Security admission judges it —
and under `restricted` a root container is rejected outright, before anything
starts, so a root default would fail on exactly the clusters most likely to
have someone debugging in them. A node shell is a privileged pod entering the
host namespaces with `nsenter`: root by definition, and a nonroot image could
not do the one thing it exists for. Do not "tidy" these into one reference.
They are pinned rather than floating for the same reason everything PodSteer
creates in a cluster is: what this application puts into somebody's cluster
must not change because an upstream tag was republished. And the registry is
Docker Hub deliberately — the same repository on ghcr.io answers 403 to an
anonymous pull, so a ghcr reference would be `ImagePullBackOff` on every
cluster with no credential for it, which is all of them out of the box. All
four (the three images and the node-shell namespace) are editable in
Settings → Terminal images (`TerminalImagesPane.svelte`), which is where an
air-gapped operator points them at their own mirror; they were persisted
preferences reachable only from the dialogs that used them until that pane
existed, so the operator who most needed them met them as a stuck pod.

## The in-cluster shell is an ORDINARY pod, and its admissibility is the feature

`app/adapters/k8s/clustershell.go` creates a throwaway pod in a namespace and
attaches to a shell in it — the equivalent of `kubectl run --rm -it` — so an
operator can run `kubectl`, `dig` and `curl` FROM INSIDE the cluster's network.
`nodeshell.go` is the template and the lifecycle is copied wholesale: a
registry beside `nodeShells` on the adapter, the record and the pod created and
destroyed together, the pod deleted when the attach session ends
(`TerminalAPI.StartClusterShellSession`'s exit hook) and on shutdown
(`StopAllClusterShells` in `OnShutdown`, beside `StopAllNodeShells`), a `closed`
flag so a start racing the sweep deletes its own pod, an activeDeadlineSeconds
of one hour as the crash backstop, and a stop control in the activity list
(`ClusterShellsPanel`, beside `NodeShellsPanel`).

**It is neither of the two things it will be mistaken for.** Not the ephemeral
debug container, which is injected into somebody else's pod and which
Kubernetes will not remove; not the node shell, which is privileged, pins
itself to a node with a blanket toleration, and enters the host namespaces with
`nsenter`. This is a vantage point, not a privilege — and every one of those
node-shell fields is absent here, with a test naming each absence.

**WHAT IS PRESENT IS ADMISSIBILITY, and that is the point of the whole
feature.** A namespace enforcing Pod Security's `restricted` profile rejects a
root container outright, before anything starts — so the pod runs as non-root
(the image default is the `-nonroot` DockyDEB build, the DEBUG image's variant
and for the debug image's reason), forbids privilege escalation, drops every
capability and asks for the `RuntimeDefault` seccomp profile. Those four are
`restricted`'s container requirements and a pod missing any one is refused in
exactly the namespaces this exists to work in. **If admission refuses anyway,
the API server's own words travel verbatim.** That needed a new sentinel:
a Pod Security refusal is HTTP **403**, so `classify` would have reported it as
`ErrForbidden` — "your account is not allowed to perform this operation", which
is false (the account was allowed and the OBJECT was declined) and which throws
away the one sentence naming the field to change. `classifyPodCreate` tells the
two apart by the API server's own wording, which is the only evidence a 403
carries, and wraps `ports.ErrPodRejectedByAdmission` /
`CodePodRejected` — the direct sibling of `ErrManifestRejected`, verbatim for
the identical reason. Anything matching neither wording keeps `classify`'s
answer, so a change upstream costs the sharper message and never the diagnosis.

**`automountServiceAccountToken` is the one field this sets the OPPOSITE way
from the node shell**, which sets it false. A node shell reaches a node through
nsenter and needs no API access at all; this shell exists so somebody can run
kubectl from inside, and the token it gets is the namespace's own default
ServiceAccount — what `kubectl run` hands any pod there, and not a credential
of the operator's that PodSteer copied anywhere. Set explicitly rather than
left to Kubernetes' default, so it is a decision on the record.

**The namespace follows the TAB, and "all namespaces" is asked about rather
than guessed.** `clusterShellNamespaceFor` (`web/src/lib/clusterShell.ts`)
returns the tab's namespace, and the EMPTY STRING when the tab is on every
one — at which point the dialog asks and the confirm button is disabled. It
deliberately does not fall back to a system namespace: that is where the node
shell's own setting points (kube-system, permissive by necessity), and it is
the wrong answer here — an operator looking at their application's namespace
would get a pod in kube-system without being told, in the one namespace they
are least likely to be permitted to create one in. The refusal is made three
times over, because `domain.NewNamespaceName("")` is `NamespaceAll` rather than
an error and every list in this application reads it as "every namespace":
`domain.ErrShellNamespaceRequired` exists for exactly that, and
`TerminalAPI`, `ManagementService` and the adapter each check it.

**Reuse is offered only for a RUNNING pod.** The pods are labelled
(`app.kubernetes.io/managed-by=podsteer`, `podsteer.io/purpose=cluster-shell` —
the purpose label is what keeps a node shell out of this list), and
`FindClusterShells` lists ours in the namespace before another is created.
`domain.PlanClusterShellReuse` splits them: Running is an offer, everything
else is `Other` — reported, because a namespace accumulating exited shell pods
is otherwise a question the operator has to ask the cluster, and never offered,
because an attach to an exited pod fails for a reason the offer gave nobody a
way to see. **Attaching ADOPTS the pod** (`AdoptClusterShell`), which puts it on
the same delete hook a created one is on; a pod nobody owns is a pod nobody
deletes, and reuse would otherwise be how a namespace fills up. Adoption reads
the pod back from the cluster rather than trusting the offer — both facts can
have changed in the moment since — and refuses one that is not ours, which is
the check that keeps "attach to this pod" from becoming "delete any pod in this
namespace when the pane closes".

**The writes go through `ManagementService`, unlike the node shell's.** That is
a deliberate divergence and `app/application/clustershell.go` says so at the
top: the node shell holds `ports.NodeShellPort` on `TerminalAPI` and
re-implements the guard (`ReadOnly()`) and the audit line there, so a method
added beside it inherits neither. Creating a pod is a write like any other, so
it goes where every other write goes — `refuseIfReadOnly` first, before
anything else, and one audit line naming cluster, namespace and pod. That line
is written AFTER the create rather than before, unlike every other write here,
for one reason: the pod's name is generated, so there is no pod to name until
the API server has accepted it; a refused create is on the record as the Error
line, which names what could be known. `TerminalAPI` keeps a synchronous
`ReadOnly()` pre-check as well, which is only what stops a doomed session being
built. `StopClusterShell` and `StopAllClusterShells` are deliberately
UNGUARDED, the same exception the node shell's stop makes: they remove
something PodSteer put in the cluster, and refusing one because the cluster was
marked read-only after the pod was created would strand that pod until its
deadline.

**The toolbar control is a MENU because it grew a second entry.**
`TerminalMenu.svelte` replaces the local terminal's `ToolbarButton`, with a
chevron so it reads as a menu rather than as a button, and it disables the
in-cluster entry on a read-only cluster while leaving the local one alone —
which is the guard's own doctrine rather than an inconsistency, and is the
distinction `terminal_local_test.go` already exists to protect.

## The local terminal runs the operator's own tools, and pins the context with a file that holds nothing else

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

**`current-context` in the operator's kubeconfig is never touched, and the
tab's context is still selected — through a file that names it and nothing
else.** `KUBECONFIG` is set to exactly the files the Kubernetes adapter reads
(`Adapter.KubeconfigFiles`, the one implementation of "which files", quoted in
both places, `PODSTEER_KUBECONFIG_DIR` included), with ONE PodSteer-owned file
in front of them:

```yaml
apiVersion: v1
kind: Config
current-context: <the tab's context>
```

No clusters, no users, no credentials — see
`app/adapters/localshell/kubecontext.go`. client-go keeps the FIRST definition
of anything it merges, so that `current-context` wins while every cluster, user
and context still comes from the operator's own files behind it: the context
resolves in full, namespace included, and their kubeconfig is opened for
reading only.

**This paragraph used to say there was no honest way to do better, and the
enumeration behind that claim was incomplete.** kubectl does select a context
from `current-context` or an explicit flag and nothing else — no environment
variable carries one — and all three options that were enumerated are still
refused: writing the operator's kubeconfig (refused outright by the kubeconfig
section above; kubectl in the terminal beside this one must not change target),
writing a per-session copy of their credentials to disk, and injecting a shell
alias, which cannot be done without REPLACING their own startup files, since
bash's `--rcfile` and zsh's `ZDOTDIR` substitute rather than add and would cost
them their prompt, functions and aliases in exchange for one of ours. What was
missed is that the MERGE is a fourth way in, and it costs none of what those
three cost: their file is untouched, nothing in the overlay is a secret so
there is no credential at rest, and a list is added to rather than substituted
for.

**Two consequences that must be said rather than discovered.** First,
`kubectl config use-context` inside that shell writes to the FIRST file in
`KUBECONFIG`, which is the overlay — so it appears to work and dies with the
session. That is the outcome we want, far better than it editing their real
file, but it is surprising enough to read as a bug, so the notice says it.
Second, the notice above the prompt no longer tells anybody to pass `--context`
— that instruction stopped being true — and it says their own kubeconfig is
untouched. `ContextNotice` in `env.go` writes it and
`web/src/lib/localShell.ts` writes the pane's own line; both were changed
together, because a stale half of a two-part statement is worse than either
half alone.

**No overlay when nothing resolved, and no overlay is not a failure.**
`BuildEnv` deliberately leaves `KUBECONFIG` alone when the precedence list is
empty, so a shell keeps seeing whatever clusters the operator's environment
gave it; prepending an overlay there would set `KUBECONFIG` to one file naming
a context nothing defines and the shell would see NO clusters instead of
theirs. And if writing the overlay fails at all — a temp directory that will
not take a file — the session opens anyway with exactly the behaviour it had
before, and the notice falls back to saying `--context`. A terminal is worth
more than a pin; a notice claiming a context that is not selected is not.

**The overlay is created and destroyed with its session**, in a 0700 temp
directory at mode 0600, removed in `pump` where the record is dropped — before
`done` closes, so a caller that stopped a session knows the file is gone and
not merely the process — and therefore on every path `StopLocalShell` and
`StopAllLocalShells` reach. The refused-during-shutdown branch and the
`startPTY` failure path remove it themselves, because neither registers a
session that would ever come back for it. There is deliberately no second sweep
over the temp directory: a second list of things to clean up is how the two
lists come to disagree.

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
record, the process AND the context overlay are created and destroyed together,
a session ends when its pane closes, and `StopAllLocalShells` runs in the
shutdown hook beside `StopAllNodeShells`. A file left in the temp directory
after its shell has gone is the same class of leak as a goroutine nobody stops
— nothing breaks today, and by the hundredth session there are a hundred of
them. Ending one signals its whole process GROUP — a shell's
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

## The terminal's font stack leads with Nerd Fonts, and measures with Unicode 11

`web/src/lib/terminalFont.ts` holds the stack, out of the component so its
ORDER can be argued with in a test. A prompt built with Powerlevel10k, Starship
or oh-my-posh draws its separators and icons from the Private Use Area, and no
plain monospace family has a glyph there — so the stack this pane shipped with
(`JetBrains Mono`, `Fira Code`, `Cascadia Code`, Monaco, Menlo, `Ubuntu Mono`)
rendered such a prompt as a row of boxes. The Nerd Font names now come first,
`MesloLGS NF` leading because that is what Powerlevel10k's own wizard installs;
every plain family is kept behind them, unchanged, because this must not become
a stack that only works for people who installed something. `Symbols Nerd Font
Mono` is LAST rather than first on purpose: it has no letters or digits, so
leading with it would hand xterm.js a first family it cannot measure a cell
from, while per-glyph fallback still reaches it where it is. Nothing is
bundled — PodSteer ships no font and downloads none, the same rule the local
terminal follows for kubectl.

A font alone only fixes half of it. xterm.js decides how many CELLS a character
occupies from a built-in table that stops at Unicode 6, where most emoji, the
CJK ranges added since and the Private Use Area are all one cell wide; a
two-cell glyph drawn in a one-cell slot shifts the rest of the line and smears
on every redraw. `@xterm/addon-unicode11` (MIT, in `notices.json`) replaces
that table, and `terminal.unicode.activeVersion = '11'` is the switch — loading
the addon only makes the version selectable. It needs `allowProposedApi`, which
this terminal already sets.

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
never reaches `application.New` or `app.Run`, so the single-instance lock is
untouched and an agent can start one while the window is open.

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
