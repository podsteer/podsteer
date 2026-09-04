<!--
  A TLS Secret's certificate, decoded on request.

  The certificate is public material — anything terminating TLS with it hands
  it to every client that connects — but it lives inside the same Secret
  object as the private key, and a read of that object is a read of that
  object regardless of which half somebody wants. Kubernetes' own
  good-practices page tells cluster operators to alert on Secret reads, so
  this pane never resolves one on its own: the section starts collapsed, and
  nothing is fetched until the operator presses "Inspect certificate" — the
  same deliberate-act discipline secretReveals.svelte.ts documents for a
  Secret's other values.

  RESULTS DIE WITH THE DRAWER. Nothing here is written to a store or to disk;
  `chain` is component-local state, and the caller keys this component on the
  Secret's identity so switching to a different Secret starts over rather than
  showing a stale certificate under a new name.
-->
<script lang="ts">
  import {
    inspectTLSSecret,
    type CertificateChain,
    type CertificateInsight,
  } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { certificateExpiryLabel } from '$lib/certificateExpiry'
  import { flash } from '$lib/flash.svelte'
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import Button from './Button.svelte'
  import {
    AlertOctagon,
    AlertTriangle,
    Info,
    ShieldCheck,
    ShieldQuestionMark,
    ShieldX,
    Copy,
    Check,
    RotateCw,
  } from '@lucide/svelte'

  interface Props {
    clusterId: string
    namespace: string
    name: string
  }

  let { clusterId, namespace, name }: Props = $props()

  let chain = $state<CertificateChain | null>(null)
  let loading = $state(false)
  let error = $state('')

  async function inspect(): Promise<void> {
    loading = true
    error = ''
    try {
      chain = await inspectTLSSecret(clusterId, namespace, name)
    } catch (cause) {
      error = toApiError(cause).message
      chain = null
    } finally {
      loading = false
    }
  }

  const SEVERITY_STYLE = {
    critical: { icon: AlertOctagon, card: 'border-error/40 bg-error-container/20', iconClass: 'text-error' },
    warning: { icon: AlertTriangle, card: 'border-gauge-warn/40 bg-gauge-warn/10', iconClass: 'text-gauge-warn' },
    info: {
      icon: Info,
      card: 'border-outline-variant bg-surface-container-low',
      iconClass: 'text-on-surface-variant/70',
    },
  } as const

  function styleFor(insight: CertificateInsight) {
    return SEVERITY_STYLE[insight.severity as keyof typeof SEVERITY_STYLE] ?? SEVERITY_STYLE.info
  }

  function rowsFor(cert: CertificateChain['leaf']): DetailRow[] {
    return [
      { label: 'Subject', value: cert.subject || '—' },
      { label: 'Issuer', value: cert.issuer || '—' },
      { label: 'Valid from', value: cert.notBefore || '—' },
      {
        label: 'Valid until',
        value: cert.notAfter
          ? `${cert.notAfter} — ${certificateExpiryLabel(cert.expiresInSeconds)}`
          : '—',
        tone: cert.expiresInSeconds < 0 ? ('critical' as const) : undefined,
      },
      { label: 'Serial number', value: cert.serialNumber || '—' },
      { label: 'Signature algorithm', value: cert.signatureAlgorithm || '—' },
      {
        label: 'Public key',
        value: cert.publicKeyAlgorithm
          ? cert.keyBits > 0
            ? `${cert.publicKeyAlgorithm}, ${cert.keyBits} bits`
            : cert.publicKeyAlgorithm
          : '—',
      },
      { label: 'CA certificate', value: cert.isCA ? 'yes' : 'no' },
      { label: 'Self-signed', value: cert.selfSigned ? 'yes' : 'no' },
    ]
  }

  // SAN copy feedback is per-row, but flash() tracks one flag — so the row
  // it belongs to is tracked alongside it, and only that row shows the tick.
  let copiedIndex = $state<number | null>(null)
  const copied = flash(1200)

  function copySAN(value: string, index: number): void {
    void navigator.clipboard?.writeText(value).catch(() => {})
    copiedIndex = index
    copied.show()
  }
</script>

<DetailSection level="h3" id="certificate" title="Certificate" defaultOpen={false}>
  {#if !chain}
    <div class="flex flex-col gap-3">
      <p class="text-body-small leading-relaxed text-on-surface-variant/70">
        Reads tls.crt (and tls.key, to check they match) from this Secret. Reading a
        Secret is an audited action, so nothing here happens until asked.
      </p>
      <Button variant="outlined" {loading} onclick={inspect} class="self-start">
        Inspect certificate
      </Button>
      {#if error}
        <p class="text-body-small text-error" role="alert">{error}</p>
      {/if}
    </div>
  {:else}
    <div class="flex flex-col gap-4">
      <div class="flex items-center justify-between gap-2">
        <!-- Key match, said up front: it is the one fact that decides
             whether this Secret works at all, and it should not wait behind
             a scroll to be seen. -->
        {#if chain.keyMatches === true}
          <span class="flex items-center gap-1.5 text-body-small text-on-surface">
            <ShieldCheck class="size-4 text-success" strokeWidth={2} />
            Key matches certificate
          </span>
        {:else if chain.keyMatches === false}
          <span class="flex items-center gap-1.5 text-body-small text-error">
            <ShieldX class="size-4" strokeWidth={2} />
            Key does not match certificate
          </span>
        {:else}
          <span class="flex items-center gap-1.5 text-body-small text-on-surface-variant/70">
            <ShieldQuestionMark class="size-4" strokeWidth={2} />
            No tls.key in this Secret to check
          </span>
        {/if}

        <Button
          variant="text"
          loading={loading}
          onclick={inspect}
          class="shrink-0"
          label="Re-inspect certificate"
        >
          <RotateCw class="size-3.5" strokeWidth={2} />
          Re-inspect
        </Button>
      </div>

      {#if error}
        <p class="text-body-small text-error" role="alert">{error}</p>
      {/if}

      {#if chain.insights.length > 0}
        <div class="flex flex-col gap-2">
          {#each chain.insights as insight, index (index)}
            {@const style = styleFor(insight)}
            {@const Icon = style.icon}
            <div class="flex items-start gap-2 rounded-sm border p-3 {style.card}">
              <Icon class="mt-0.5 size-4 shrink-0 {style.iconClass}" strokeWidth={2} />
              <div class="min-w-0 flex-1">
                <p class="text-body-medium font-medium text-on-surface">{insight.title}</p>
                <p class="mt-1 text-body-medium leading-relaxed text-on-surface-variant" data-selectable>
                  {insight.detail}
                </p>
                {#if insight.advice}
                  <p class="mt-1.5 text-body-medium leading-relaxed text-on-surface">{insight.advice}</p>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}

      <DetailList rows={rowsFor(chain.leaf)} />

      <!-- One per line, and each copyable on its own — a SAN is what gets
           pasted into a test command or an /etc/hosts entry, not the whole
           row. -->
      <div class="flex flex-col gap-1">
        <p class="text-label-medium text-on-surface-variant/70">
          Subject Alternative Names {chain.leaf.sans.length ? `(${chain.leaf.sans.length})` : ''}
        </p>
        {#if chain.leaf.sans.length === 0}
          <p class="text-body-small text-on-surface-variant/60">None</p>
        {:else}
          <ul class="flex flex-col divide-y divide-outline-variant/20 rounded-sm border border-outline-variant/40">
            {#each chain.leaf.sans as san, index (index)}
              <li class="flex items-center gap-2 px-2.5 py-1.5">
                <span class="min-w-0 flex-1 truncate font-mono text-body-small text-on-surface" data-selectable>
                  {san}
                </span>
                <button
                  type="button"
                  onclick={() => copySAN(san, index)}
                  aria-label="Copy {san}"
                  class="state-layer grid size-6 shrink-0 place-items-center rounded-full
                         text-on-surface-variant/60 transition-colors duration-100 hover:text-on-surface"
                >
                  {#if copied.on && copiedIndex === index}
                    <Check class="size-3.5 text-success" strokeWidth={2} />
                  {:else}
                    <Copy class="size-3.5" strokeWidth={1.8} />
                  {/if}
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </div>

      {#if chain.intermediates.length > 0}
        <div class="flex flex-col gap-1">
          <p class="text-label-medium text-on-surface-variant/70">
            Intermediates ({chain.intermediates.length})
          </p>
          <ul class="flex flex-col divide-y divide-outline-variant/20 rounded-sm border border-outline-variant/40">
            {#each chain.intermediates as intermediate, index (index)}
              <li class="px-2.5 py-1.5">
                <p class="truncate text-body-small text-on-surface" data-selectable title={intermediate.subject}>
                  {intermediate.subject || '—'}
                </p>
                <p class="truncate text-body-small text-on-surface-variant/60" title={intermediate.issuer}>
                  issued by {intermediate.issuer || '—'}
                </p>
              </li>
            {/each}
          </ul>
        </div>
      {/if}
    </div>
  {/if}
</DetailSection>
