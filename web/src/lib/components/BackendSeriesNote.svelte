<!--
  What the cluster's monitoring backend said, or why it said nothing.

  A STATUS AND A SENTENCE, NEVER A BLANK. Every outcome here is a fact about
  somebody's cluster rather than a fault in this application — an account that
  may not proxy, a Prometheus that rejected the expression, a Thanos that
  answers for six clusters, and a Prometheus that scrapes application
  endpoints and not kubelets. Each needs different words and each needs a
  different next step, which is why `domain.BackendStatus` exists rather than
  a boolean and why nothing here collapses two of them into "no data".

  THE TWO SERIES MUST NEVER READ AS ONE. The chart above draws the backend's
  line beside PodSteer's own with its own colour and dash; this says in words
  whose it is and how far PodSteer could check that it holds THIS cluster's
  data. A number narrowed by a node filter PodSteer composed is a different
  claim from one that needed no filter, so the two are worded differently.
-->
<script lang="ts">
  import { LineChart, RefreshCw, ShieldAlert, ShieldCheck, ShieldQuestion } from '@lucide/svelte'
  import type { BackendSeriesResult } from '$lib/api/client'
  import type { MetricsQueryMode } from '$stores/backendTrend.svelte'

  interface Props {
    result: BackendSeriesResult
    /** The per-cluster mode, which decides whether a control is offered. */
    mode: MetricsQueryMode
    /** True while a query is in flight. */
    busy?: boolean
    /** Set when the CALL failed, as against the backend answering a refusal. */
    error?: string | null
    onrefresh: () => void
  }

  let { result, mode, busy = false, error = null, onrefresh }: Props = $props()

  /**
   * How far PodSteer could check that this backend holds this cluster's data.
   *
   * SAID OUT LOUD RATHER THAN IMPLIED BY THE PRESENCE OF A LINE. A backend
   * fronting several clusters is the ordinary shape of Thanos, Mimir, Cortex
   * and a VictoriaMetrics cluster, and a chart that did not say so would be
   * showing five other clusters' load under this one's name.
   */
  const verification = $derived.by(() => {
    switch (result.provenance.verification) {
      case 'verified':
        return {
          icon: ShieldCheck,
          text: 'It answers for this cluster and nothing else.',
        }
      case 'fleet':
        return {
          icon: ShieldAlert,
          text: result.provenance.filtered
            ? "It also holds other clusters, so this query was narrowed to this cluster's own nodes."
            : 'It also holds other clusters.',
        }
      case 'mismatch':
        return { icon: ShieldAlert, text: 'It appears to hold a different cluster.' }
      case 'unverifiable':
        return { icon: ShieldQuestion, text: 'PodSteer could not check which cluster it holds.' }
      default:
        return null
    }
  })

  /** Whether the row is worth drawing at all. */
  const shown = $derived(mode !== 'off' && result.status !== 'not-enabled')

  /** Whether the outcome is one an operator should read as a problem. */
  const unhappy = $derived(
    ['forbidden', 'unreachable', 'rejected', 'too-large', 'unverified', 'needs-credential'].includes(
      result.status,
    ),
  )

  const headline = $derived.by(() => {
    switch (result.status) {
      case 'answered':
        return `Longer history read from ${result.provenance.source}.`
      case 'answered-empty':
        return `${result.provenance.source || 'The monitoring backend'} answered with nothing.`
      case 'nothing-discovered':
        return 'No monitoring backend answered for this cluster.'
      case 'forbidden':
        return 'Your account cannot reach the monitoring stack from here.'
      case 'unreachable':
        return 'The monitoring backend could not be reached.'
      case 'rejected':
        return 'The monitoring backend rejected the query.'
      case 'too-large':
        return 'The monitoring backend answered with more than PodSteer will read.'
      case 'needs-credential':
        return 'The monitoring backend wants a credential of its own.'
      case 'unverified':
        return 'No aggregate was drawn from the monitoring backend.'
      default:
        return ''
    }
  })
</script>

{#if shown}
  <div
    class="flex flex-col gap-1.5 rounded-md px-3 py-2 text-body-small
           {unhappy
      ? 'bg-error-container/25 text-on-surface-variant'
      : 'bg-surface-container-low text-on-surface-variant'}"
  >
    <div class="flex items-start gap-2">
      <LineChart class="mt-0.5 size-3.5 shrink-0 opacity-60" strokeWidth={2} />
      <span class="flex-1">
        <strong class="font-medium text-on-surface">{headline}</strong>
        {#if result.message}
          <!--
            THE BACKEND'S OWN WORDS WHERE THERE ARE ANY. For a rejection this
            is the expression it objected to and where — the diagnosis, and
            the only thing anybody can act on. Paraphrasing it would throw
            that away, exactly as it would for a manifest the API server
            rejected.
          -->
          {result.message}
        {/if}
      </span>

      {#if mode !== 'off'}
        <button
          type="button"
          onclick={onrefresh}
          disabled={busy}
          title="Ask the monitoring backend now"
          class="shrink-0 rounded p-1 text-on-surface-variant transition-colors duration-100
                 hover:bg-surface-container-high hover:text-on-surface disabled:opacity-40"
        >
          <RefreshCw class="size-3.5 {busy ? 'animate-spin' : ''}" strokeWidth={2} />
          <span class="sr-only">Ask the monitoring backend now</span>
        </button>
      {/if}
    </div>

    {#if verification && result.provenance.source}
      <p class="flex items-start gap-2 pl-5.5">
        <verification.icon class="mt-0.5 size-3.5 shrink-0 opacity-60" strokeWidth={2} />
        <span>
          {verification.text}
          <!--
            SAID EVERY TIME A BACKEND LINE IS DRAWN, not only when something
            went wrong. The two lines on the chart are two measurements taken
            by two systems at two intervals, and the moment that stops being
            stated somebody reads them as one series.
          -->
          {#if result.status === 'answered'}
            It is drawn beside PodSteer's own samples and is a separate measurement.
          {/if}
        </span>
      </p>
    {/if}

    {#if result.expression}
      <!--
        WHAT WAS SENT, SHOWN AND NEVER TYPED. There is no query box: the
        expression comes from a fixed table in the Go domain. It is shown so
        an operator reading their own backend's query log can match a line
        there to something they pressed here.
      -->
      <details class="pl-5.5">
        <summary class="cursor-pointer text-on-surface-variant/70 hover:text-on-surface">
          What PodSteer asked
        </summary>
        <code
          class="mt-1 block overflow-x-auto rounded-sm bg-surface-container px-2 py-1
                 font-mono text-body-small text-on-surface-variant"
        >
          {result.expression}
        </code>
      </details>
    {/if}

    {#if error}
      <p class="pl-5.5 text-on-surface-variant/70">{error}</p>
    {/if}
  </div>
{/if}
