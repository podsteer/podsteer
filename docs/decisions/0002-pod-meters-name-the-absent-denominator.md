# 2. Pod meters name the absent denominator rather than substituting one

Decided 2026-08-29. Status: **accepted**.

## Context

The pod list draws CPU and memory as a value plus a proportion bar, matching
the node list. A node's denominator is its allocatable capacity, which every
node has. A pod's is the sum of its containers' **requests**, which not every
pod has: a BestEffort pod declares nothing, so there is no proportion to draw
and the bar is omitted.

That omission looked like a defect. An empty cell beside populated ones reads
as "the meter failed here", not as "there is nothing to meter", and the
explanation lived only in a tooltip — which nobody hovers over a cell that
already looks broken.

The proposal was to fall back to a denominator we could derive from what we
already know, most plausibly the allocatable capacity of the node the pod is
scheduled on, and to colour that fallback bar **amber** so it read as a
different kind of measurement.

## Why the fallback was rejected

**We do not, in fact, already know it.** `WorkloadService` is constructed with
a workload port, a metrics port and the registry, and nothing else
(`app/application/workload.go`). `domain.Pod` carries `nodeName` but no node
capacity, and the only enrichment on the list path is `WithUsage`. Getting
allocatable onto a pod row means either a nodes LIST on every pod refresh —
every ten seconds, per open tab, per cluster, against the same shared QPS
budget everything else draws on — or building the per-cluster capacity cache
first. The premise that this was free was simply wrong.

**The bars would not be visible.** This is the argument that settles it on its
own. A pod using 50m against a 200m request is at 25%. The same pod against a
sixteen-core node is at 0.3%. A BestEffort pod busy enough to render a bar
somebody could see is one consuming a double-digit percentage of an entire
node, which is rare enough not to be worth designing for. The amber that was
meant to carry the signal would, in the overwhelming majority of cases, not be
drawn at all.

**Amber already means severity in this component.** `MeterBar` colours
`bg-warning` at 75% and `bg-error` at 90% for threshold meters — the node
column, one view across. Amber-as-"different denominator" adjacent to
amber-as-"getting full" means an operator scanning for trouble reads every
BestEffort pod as a warning. The colour would be actively worse than nothing.

**A column whose denominator varies by row cannot be read.** Two bars at 80%
would mean "using 80% of what it reserved" on one row and "using 80% of a
node" on the next. Comparing down the column — which is what a column is for
— stops being possible. This is the same principle that decided ADR 1: a
silent fallback splices two measurement regimes into one figure, and a figure
on screen has to be one claim.

## What every comparable tool does

`kubectl top pod` prints absolute usage and no percentage at all. k9s carries
separate `%CPU/R` and `%CPU/L` columns and prints **`n/a`** where the
denominator was not declared. Lens and Headlamp show absolutes in the list and
move request/limit comparisons into the detail view, omitting the line that
has no data.

No tool we are aware of substitutes node capacity as a pod-row denominator.
That unanimity is evidence rather than convention: everyone who has met this
problem has concluded the substitution misleads.

## Decision

The pod meter **names the absence**. Where there is no request, the cell keeps
the measured value and prints `no request` where the bar would be, in the
muted colour, with no track, no percentage, and no colour used anywhere else
in the column. The tooltip still explains at length.

This makes the cell a statement rather than a gap, which was the real problem,
and it does so with no Go change, no additional API traffic, and no second
meaning attached to a column that has one.

It also happens to surface something worth surfacing. A pod with no
reservation is not merely unmetered: it is a pod the scheduler will place
anywhere and the kubelet will evict first. The QoS column already exists for
that reason, and this agrees with it instead of hiding it behind a blank.

## What this does not close

Node-relative context is a fair thing to want — "this pod is using 50m of its
node's sixteen cores" is occasionally exactly the question. It belongs in the
pod detail view or a tooltip **as text**, because text can name its own
denominator where a bar in a shared column cannot, and it should ride on a
shared per-cluster capacity cache rather than a per-refresh LIST. That is a
separate decision, to be taken when the cache exists.
