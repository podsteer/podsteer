<!--
  What this cluster's own objects say about its security posture, and what a
  scanner somebody else installed has already written down.

  THE NAME IS A PROMISE THIS PAGE CANNOT KEEP ON ITS OWN, so the first thing
  it does is say what it covers. A page called "Security" that renders empty
  on a cluster with no scanner is making a claim nobody made — "nothing found"
  where the truth is "nothing looked". Every section here states which of the
  two it is showing, and the closing section names what is deliberately not
  here and where it lives instead.

  TWO HALVES WITH DIFFERENT WARRANTIES, and they are kept visibly apart:

  - POSTURE is read from the specs operators wrote. Privileged, host
    namespaces, escalation, dangerous capabilities, UID 0. No feed, no
    database, no judgement that could go stale — a privileged container will
    mean the same thing in five years. PodSteer owns these rules
    (domain/security_findings.go).
  - VULNERABILITIES are QUOTED. PodSteer scans nothing and never will: seven
    mature scanners show zero general agreement, NIST abandoned universal CVE
    enrichment in April 2026, and a number we computed that disagreed with the
    operator's own scanner would be worse than no number. Every count here was
    written by the scanner the operator chose, before this read it. See
    domain/vulnerability.go.

  NOTHING ON THIS PAGE POLLS. The posture findings ride the assessment that
  runs under every view anyway; the scanner read is one bounded cluster-wide
  call, cached in $stores/vulnerabilities and in Go behind it. A cluster-wide
  LIST of VulnerabilityReports on a ten-second timer would be the Helm page's
  audit problem over a larger collection.
-->
<script lang="ts">
  import { ShieldCheck, ShieldOff, ArrowUpRight } from '@lucide/svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import { RBAC_KIND_ID } from '$stores/session.svelte'
  import { ALL_NAMESPACES } from '$lib/api/client'
  import { preferences } from '$stores/preferences.svelte'
  import {
    ensureVulnerabilities,
    vulnerabilityReadFor,
    summariesFor,
  } from '$stores/vulnerabilities.svelte'
  import FindingCard from '$lib/components/FindingCard.svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /**
   * One cluster-wide scanner read, when the page opens.
   *
   * ALL_NAMESPACES rather than the session's, because posture is a property
   * of the cluster and an operator who has narrowed the navigator to one
   * namespace has not said anything about that. The store reads each
   * cluster-and-namespace pair at most once, so re-entering the page costs
   * nothing.
   */
  $effect(() => {
    ensureVulnerabilities(session.cluster.id, ALL_NAMESPACES)
  })

  /** What the scanner read produced, or undefined before it answers. */
  const read = $derived(vulnerabilityReadFor(session.cluster.id, ALL_NAMESPACES))
  const summaries = $derived(summariesFor(session.cluster.id, ALL_NAMESPACES))

  /**
   * The posture findings, from the assessment this tab already has.
   *
   * NOT RE-FETCHED. The overview's assessment runs under every view, and its
   * security category is exactly this. Asking again would be a second request
   * for something on screen.
   */
  const posture = $derived(
    (session.overview?.findings ?? []).filter((finding) => finding.category === 'Security'),
  )

  /**
   * Every scanned image, with the workloads running it and the counts the
   * scanner recorded.
   *
   * KEYED BY IMAGE, NOT BY WORKLOAD, because the image is what an operator
   * actually fixes: the scanner writes one report per container, so an image
   * running in twelve Deployments produces twelve identical rows — twelve
   * problems where there is one. One tag bump closes all twelve.
   *
   * The counts are the MAXIMUM across the workloads running the image rather
   * than the sum, because summing would count the same CVE once per workload
   * and report a number no scanner ever wrote.
   */
  const images = $derived.by(() => {
    const byImage = new Map<
      string,
      { image: string; critical: number; high: number; medium: number; low: number; unknown: number; workloads: number }
    >()

    for (const summary of summaries) {
      for (const image of summary.images ?? []) {
        const row = byImage.get(image) ?? {
          image,
          critical: 0,
          high: 0,
          medium: 0,
          low: 0,
          unknown: 0,
          workloads: 0,
        }
        row.critical = Math.max(row.critical, summary.critical)
        row.high = Math.max(row.high, summary.high)
        row.medium = Math.max(row.medium, summary.medium)
        row.low = Math.max(row.low, summary.low)
        row.unknown = Math.max(row.unknown, summary.unknown)
        row.workloads += 1
        byImage.set(image, row)
      }
    }

    return [...byImage.values()].sort(
      (a, b) =>
        b.critical - a.critical ||
        b.high - a.high ||
        b.medium - a.medium ||
        b.workloads - a.workloads ||
        a.image.localeCompare(b.image),
    )
  })

  /** Images carrying at least one critical or high, which is what gets acted on. */
  const pressing = $derived(images.filter((row) => row.critical > 0 || row.high > 0))

  function snoozedUntil(findingId: string, namespace: string, name: string): number {
    return preferences.snoozedUntil(session.cluster.id, findingId, namespace, name)
  }

  function openList(kindId: string): void {
    void session.selectKind(kindId)
  }

  async function openObject(kindId: string, name: string, namespace: string): Promise<void> {
    const kind = session.kinds.find((entry) => entry.id === kindId)
    await session.openObject(kindId, name, namespace, kind?.namespaced ?? true)
  }
</script>

<div class="min-h-0 flex-1 overflow-y-auto">
  <div class="mx-auto flex max-w-[1100px] flex-col gap-6 p-5">
    <!--
      WHAT THIS PAGE IS, BEFORE ANYTHING ON IT. Read first because everything
      below is qualified by it, and because a page named "Security" that
      starts with numbers invites an operator to read those numbers as a
      complete account.
    -->
    <header class="flex flex-col gap-2">
      <h1 class="text-headline-small font-semibold text-on-surface">Security posture</h1>
      <p class="max-w-[70ch] text-body-medium text-on-surface-variant">
        Two things, with different warranties. The privileges these workloads take are read
        from the specs somebody wrote here — PodSteer owns those rules and they do not go
        stale. The vulnerability counts are quoted from a scanner running in this cluster;
        <span class="font-medium text-on-surface">PodSteer scans nothing itself</span> and
        never sends anything anywhere.
      </p>
    </header>

    <!-- ── Posture ─────────────────────────────────────────────────────── -->
    <section class="flex flex-col gap-3">
      <div class="flex items-baseline gap-2">
        <h2 class="text-title-medium font-semibold text-on-surface">Privileges these workloads take</h2>
        <span class="text-body-small text-on-surface-variant/70">
          from the pod specs, not from a scanner
        </span>
      </div>

      {#if !session.overview}
        <p class="text-body-medium text-on-surface-variant">Assessing the cluster…</p>
      {:else if posture.length === 0}
        <!--
          A REAL ANSWER, NOT AN EMPTY STATE. These rules fire on a deliberate
          act with no benign default, so nothing found means nothing here asks
          for privileges it should not have — which is worth saying plainly.
        -->
        <div class="flex items-start gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <ShieldCheck class="mt-0.5 size-5 shrink-0 text-success" strokeWidth={1.8} />
          <p class="text-body-medium text-on-surface-variant">
            No workload here runs privileged, shares a host namespace, allows privilege
            escalation, holds a dangerous capability, or pins itself to UID&nbsp;0.
          </p>
        </div>
      {:else}
        <!--
          RENDERED AS FINDINGS, WITH THE SAME CARD THE OVERVIEW USES, so a
          snooze set in one place is honoured in the other — they are the same
          findings, and a second presentation with its own quietening would be
          two switches for one alarm.
        -->
        <p class="max-w-[70ch] text-body-medium text-on-surface-variant">
          Each of these is a field somebody wrote deliberately. They are reported as notes
          rather than as failures: every real cluster runs a privileged network or storage
          agent, because that is what those agents are for.
        </p>
        <div class="flex flex-col gap-3">
          {#each posture as finding (finding.id)}
            <FindingCard
              {finding}
              snoozedUntil={(namespace, name) => snoozedUntil(finding.id, namespace, name)}
              onopen={openList}
              onselect={openObject}
              onsnooze={(namespace, name, durationMs) =>
                preferences.snooze(session.cluster.id, finding.id, namespace, name, durationMs)}
              onunsnooze={(namespace, name) =>
                preferences.unsnooze(session.cluster.id, finding.id, namespace, name)}
            />
          {/each}
        </div>
      {/if}
    </section>

    <!-- ── Scanner ─────────────────────────────────────────────────────── -->
    <section class="flex flex-col gap-3">
      <div class="flex items-baseline gap-2">
        <h2 class="text-title-medium font-semibold text-on-surface">Known vulnerabilities in these images</h2>
        <span class="text-body-small text-on-surface-variant/70">quoted from your scanner</span>
      </div>

      {#if !read}
        <p class="text-body-medium text-on-surface-variant">Reading what the scanner recorded…</p>
      {:else if read.status === 'not-installed'}
        <!--
          THE SENTENCE THIS PAGE EXISTS TO GET RIGHT. Most clusters have no
          scanner, and on those an empty section would read as "clean". It
          names what to install and says plainly that PodSteer will not do it.
        -->
        <div class="flex items-start gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <ShieldOff class="mt-0.5 size-5 shrink-0 text-on-surface-variant" strokeWidth={1.8} />
          <div class="flex flex-col gap-1">
            <p class="text-body-medium text-on-surface">
              No vulnerability scanner is installed in this cluster.
            </p>
            <p class="max-w-[70ch] text-body-medium text-on-surface-variant">
              This section is blank because nothing has looked — not because the images are
              clean. PodSteer reads the reports a scanner writes; it does not scan, and
              installing one is a decision about your cluster rather than about this
              application. Anything writing <code class="font-mono text-body-small">VulnerabilityReport</code>
              objects fills this in.
            </p>
          </div>
        </div>
      {:else if read.status === 'forbidden'}
        <div class="flex items-start gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <ShieldOff class="mt-0.5 size-5 shrink-0 text-on-surface-variant" strokeWidth={1.8} />
          <p class="max-w-[70ch] text-body-medium text-on-surface-variant">
            A scanner is installed, but this kubeconfig may not read its reports. Nothing
            below is missing because the images are clean — the read was refused.
          </p>
        </div>
      {:else if !read.complete && !read.truncated}
        <div class="flex items-start gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <ShieldOff class="mt-0.5 size-5 shrink-0 text-on-surface-variant" strokeWidth={1.8} />
          <p class="max-w-[70ch] text-body-medium text-on-surface-variant">
            The scanner read did not complete, so nothing here can be read as an account of
            this cluster.
          </p>
        </div>
      {:else}
        {#if read.truncated}
          <!--
            A CEILING IS A FACT ABOUT THE ANSWER, not a detail. Below it, the
            list is what was read rather than what exists, and an operator
            comparing two clusters has to know which they are looking at.
          -->
          <p class="rounded-sm border border-gauge-warn/30 bg-gauge-warn/10 p-3 text-body-medium text-on-surface">
            Stopped at {read.cap.toLocaleString()} reports. {read.read.toLocaleString()} were read{read.remaining
              ? `, and the server said ${read.remaining.toLocaleString()} more were withheld`
              : ''}. What follows is that much of the picture, not all of it.
          </p>
        {/if}

        {#if images.length === 0}
          <div class="flex items-start gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
            <ShieldCheck class="mt-0.5 size-5 shrink-0 text-success" strokeWidth={1.8} />
            <p class="text-body-medium text-on-surface-variant">
              The scanner has written no reports for this cluster's workloads yet.
            </p>
          </div>
        {:else}
          <p class="max-w-[70ch] text-body-medium text-on-surface-variant">
            Grouped by image, because that is what gets fixed: one tag bump closes every
            workload running it. {pressing.length === 0
              ? 'Nothing carries a critical or high.'
              : `${pressing.length} of ${images.length} ${images.length === 1 ? 'image' : 'images'} ${pressing.length === 1 ? 'carries' : 'carry'} a critical or high.`}
          </p>

          <div class="overflow-x-auto rounded-sm border border-outline-variant/40">
            <table class="w-full min-w-[640px] border-collapse text-body-medium">
              <thead>
                <tr class="border-b border-outline-variant/40 bg-surface-container-low text-left">
                  <th class="px-3 py-2 font-medium text-on-surface-variant">Image</th>
                  <th class="px-3 py-2 text-right font-medium text-on-surface-variant">Workloads</th>
                  <th class="px-3 py-2 text-right font-medium text-on-surface-variant">Critical</th>
                  <th class="px-3 py-2 text-right font-medium text-on-surface-variant">High</th>
                  <th class="px-3 py-2 text-right font-medium text-on-surface-variant">Medium</th>
                  <th class="px-3 py-2 text-right font-medium text-on-surface-variant">Low</th>
                  <!--
                    UNKNOWN IS ITS OWN COLUMN, not folded into Low. The
                    scanner's own bucket for a finding whose sources do not
                    state a severity — and trivy-operator ≥ v0.32.0 files
                    genuine highs and criticals there when SeveritySource is
                    empty, so folding it would quietly under-report.
                  -->
                  <th class="px-3 py-2 text-right font-medium text-on-surface-variant">Unknown</th>
                </tr>
              </thead>
              <tbody>
                {#each images as row (row.image)}
                  <tr class="border-b border-outline-variant/20 last:border-0">
                    <td class="px-3 py-2 font-mono text-body-small text-on-surface" data-selectable>
                      {row.image}
                    </td>
                    <td class="px-3 py-2 text-right tabular-nums text-on-surface-variant">{row.workloads}</td>
                    <td class="px-3 py-2 text-right tabular-nums {row.critical > 0 ? 'font-semibold text-gauge-critical' : 'text-on-surface-variant/50'}">
                      {row.critical}
                    </td>
                    <td class="px-3 py-2 text-right tabular-nums {row.high > 0 ? 'font-semibold text-gauge-warn' : 'text-on-surface-variant/50'}">
                      {row.high}
                    </td>
                    <td class="px-3 py-2 text-right tabular-nums text-on-surface-variant">{row.medium}</td>
                    <td class="px-3 py-2 text-right tabular-nums text-on-surface-variant">{row.low}</td>
                    <td class="px-3 py-2 text-right tabular-nums text-on-surface-variant">{row.unknown}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      {/if}
    </section>

    <!-- ── What is not here ────────────────────────────────────────────── -->
    <!--
      THE SECTION THAT MAKES THE NAME HONEST. Without it, "Security" reads as
      a complete account of the cluster's security, which reading one optional
      operator and one set of spec rules cannot be. Naming the gaps is cheaper
      than any of them and is the part an operator is entitled to.
    -->
    <section class="flex flex-col gap-3 border-t border-outline-variant/40 pt-5">
      <h2 class="text-title-medium font-semibold text-on-surface">What this page does not cover</h2>
      <ul class="flex max-w-[70ch] flex-col gap-2 text-body-medium text-on-surface-variant">
        <li>
          <span class="font-medium text-on-surface">Who can do what.</span> Permissions and the
          blast radius of a role are a different question, asked of the authorization APIs.
          <button
            type="button"
            class="inline-flex items-center gap-0.5 text-primary hover:underline"
            onclick={() => openList(RBAC_KIND_ID)}
          >
            Permissions<ArrowUpRight class="size-3.5" strokeWidth={2} />
          </button>
        </li>
        <li>
          <span class="font-medium text-on-surface">Volumes.</span> A mounted docker.sock or a
          sensitive hostPath belongs beside the privileges above and is deliberately absent:
          the watch store strips volumes, so the rule would be right on some clusters and
          silently blank on others. A blank rule is worse than no rule.
        </li>
        <li>
          <span class="font-medium text-on-surface">Anything through time.</span> When a
          critical first appeared, how long it has gone unfixed, whether posture is improving
          — none of it is answerable here. Trivy Operator's reports default to a 24-hour TTL,
          so the cluster itself does not hold the history.
        </li>
        <li>
          <span class="font-medium text-on-surface">A compliance score.</span> There is no
          percentage on this page and there will not be one. A score derived from one optional
          operator's CRDs is a number that cannot survive being asked how it was calculated.
        </li>
        <li>
          <span class="font-medium text-on-surface">Certificate expiry and deprecated APIs</span>
          are computed, but per object and on the overview respectively, rather than being
          restated here.
        </li>
      </ul>
    </section>
  </div>
</div>
