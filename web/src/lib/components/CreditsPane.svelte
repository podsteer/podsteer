<!--
  The Credits pane: every dependency PodSteer ships, and its licence.

  This is an obligation, not a courtesy. MIT, BSD, ISC and Apache-2.0 all
  require the licence and its copyright notice to be distributed with the
  binary, and a desktop application has nowhere to put them except a pane like
  this one. The inventory is generated from what actually ships and embedded at
  build time (see build/generate-notices.mjs), so it cannot drift away from the
  dependencies it describes.

  Licence texts are fetched one at a time: together they are far larger than
  the summary, and nobody reads more than one.
-->
<script lang="ts">
  import { listCredits, licenceText, type Credit } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { ChevronRight, Loader2 } from '@lucide/svelte'

  let credits = $state<Credit[]>([])
  let status = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state<string | null>(null)
  let filter = $state('')

  /** The package whose licence text is open, keyed "ecosystem:name". */
  let openKey = $state<string | null>(null)
  let openText = $state<string | null>(null)
  /** Its NOTICE, when the project ships one. */
  let openNotice = $state<string | null>(null)

  const ECOSYSTEMS = { go: 'Go', npm: 'JavaScript' } as const

  $effect(() => {
    let current = true
    void listCredits()
      .then((result) => {
        if (!current) return
        credits = result
        status = 'ready'
      })
      .catch((cause) => {
        if (!current) return
        error = toApiError(cause).message
        status = 'error'
      })
    return () => {
      current = false
    }
  })

  const matching = $derived.by(() => {
    const term = filter.trim().toLowerCase()
    if (!term) return credits
    return credits.filter(
      (credit) =>
        credit.name.toLowerCase().includes(term) || credit.licence.toLowerCase().includes(term),
    )
  })

  /** Grouped for display, preserving the backend's ordering within a group. */
  const groups = $derived.by(() => {
    const buckets = new Map<string, Credit[]>()
    for (const credit of matching) {
      const bucket = buckets.get(credit.ecosystem)
      if (bucket) bucket.push(credit)
      else buckets.set(credit.ecosystem, [credit])
    }
    return [...buckets.entries()]
  })

  /** Counts by licence, for the one-line summary. */
  const summary = $derived.by(() => {
    const counts = new Map<string, number>()
    for (const credit of credits) counts.set(credit.licence, (counts.get(credit.licence) ?? 0) + 1)
    return [...counts.entries()].sort((left, right) => right[1] - left[1])
  })

  function keyOf(credit: Credit): string {
    return `${credit.ecosystem}:${credit.name}`
  }

  async function toggle(credit: Credit): Promise<void> {
    const key = keyOf(credit)
    if (openKey === key) {
      openKey = null
      openText = null
      openNotice = null
      return
    }

    openKey = key
    openText = null
    openNotice = null
    if (!credit.textId) return

    try {
      openText = await licenceText(credit.textId)
      // Apache-2.0 section 4(d) makes reproducing a NOTICE a duty separate
      // from reproducing the licence, so it is fetched and shown separately.
      if (credit.noticeTextId) openNotice = await licenceText(credit.noticeTextId)
    } catch (cause) {
      openText = toApiError(cause).message
    }
  }
</script>

<div class="flex h-full min-h-0 flex-col gap-3">
  <div>
    <h3 class="text-title-medium text-on-surface">Open source components</h3>
    <p class="mt-0.5 text-body-small text-on-surface-variant">
      PodSteer is built on the work below and distributes it under the licences shown. Every one
      is permissive; none restricts what you may use PodSteer for.
    </p>
  </div>

  {#if status === 'loading'}
    <div class="flex flex-1 items-center justify-center gap-2 text-on-surface-variant/60">
      <Loader2 class="size-5 animate-spin" strokeWidth={1.8} />
      <span class="text-body-medium">Loading…</span>
    </div>
  {:else if status === 'error'}
    <p class="text-body-medium text-error">{error}</p>
  {:else}
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
      {#each summary as [licence, count] (licence)}
        <span class="text-body-small text-on-surface-variant">
          <span class="tabular-nums text-on-surface">{count}</span>
          {licence}
        </span>
      {/each}
    </div>

    <input
      type="search"
      bind:value={filter}
      placeholder="Filter by name or licence…"
      aria-label="Filter components"
      class="h-9 w-full rounded-md border border-outline-variant bg-surface-container px-3
             text-body-medium text-on-surface outline-none
             placeholder:text-on-surface-variant/50 focus:border-primary"
    />

    <div class="min-h-0 flex-1 overflow-y-auto rounded-lg border border-outline-variant/50">
      {#each groups as [ecosystem, entries] (ecosystem)}
        <h4
          class="sticky top-0 z-10 border-b border-outline-variant/50 bg-surface-container-high
                 px-3 py-1.5 text-label-medium uppercase tracking-wider text-on-surface-variant"
        >
          {ECOSYSTEMS[ecosystem as keyof typeof ECOSYSTEMS] ?? ecosystem}
          <span class="tabular-nums opacity-60">({entries.length})</span>
        </h4>

        <ul class="divide-y divide-outline-variant/30">
          {#each entries as credit (keyOf(credit))}
            {@const expanded = openKey === keyOf(credit)}
            <li>
              <button
                type="button"
                onclick={() => toggle(credit)}
                aria-expanded={expanded}
                class="flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors
                       duration-100 hover:bg-surface-container"
              >
                <ChevronRight
                  class="size-3.5 shrink-0 text-on-surface-variant/50 transition-transform
                         duration-150 ease-standard {expanded ? 'rotate-90' : ''}"
                  strokeWidth={2.5}
                />
                <span class="min-w-0 flex-1 truncate text-body-small text-on-surface" title={credit.name}>
                  {credit.name}
                </span>
                <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant/60">
                  {credit.version}
                </span>
                <span
                  class="w-24 shrink-0 text-right text-body-small text-on-surface-variant"
                  title={credit.expression ? `Offered as ${credit.expression}` : undefined}
                >
                  {credit.licence}{credit.expression ? '*' : ''}
                </span>
              </button>

              {#if expanded}
                <div class="bg-surface-container/60 px-3 pb-3 pl-8">
                  {#if credit.copyright}
                    <p class="pb-2 text-body-small text-on-surface-variant">{credit.copyright}</p>
                  {/if}

                  {#if !credit.textId}
                    <!-- A few projects declare a licence but publish no licence
                         file. Saying so is more honest than inventing one. -->
                    <p class="text-body-small text-on-surface-variant/70">
                      Declared {credit.licence}; the project publishes no licence file.
                    </p>
                  {:else if openText === null}
                    <p class="text-body-small text-on-surface-variant/60">Loading licence…</p>
                  {:else}
                    <pre
                      class="max-h-64 overflow-auto rounded border border-outline-variant/40
                             bg-surface p-2 text-[11px] leading-relaxed whitespace-pre-wrap
                             text-on-surface-variant"
                      data-selectable>{openText}</pre>
                  {/if}

                  {#if openNotice}
                    <p class="pt-3 pb-1 text-label-medium uppercase tracking-wider text-on-surface-variant">
                      Notice
                    </p>
                    <pre
                      class="max-h-48 overflow-auto rounded border border-outline-variant/40
                             bg-surface p-2 text-[11px] leading-relaxed whitespace-pre-wrap
                             text-on-surface-variant"
                      data-selectable>{openNotice}</pre>
                  {/if}
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      {/each}

      {#if matching.length === 0}
        <p class="px-3 py-6 text-center text-body-small text-on-surface-variant/60">
          Nothing matches "{filter}".
        </p>
      {/if}
    </div>
  {/if}
</div>
