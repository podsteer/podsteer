<!--
  An inline error surface in the MD3 error-container role.

  Shows the backend's message and, when the failure is one a second attempt
  could clear, a retry action.
-->
<script lang="ts">
  import type { ApiError } from '$lib/api/errors'
  import Button from './Button.svelte'
  import { AlertCircle, Info } from '@lucide/svelte'

  interface Props {
    error: ApiError | null
    /** Invoked by the retry action. Omit to hide it entirely. */
    onretry?: () => void
    /** Invoked by the dismiss action. Omit to hide it. */
    ondismiss?: () => void
    class?: string
  }

  let { error, onretry, ondismiss, class: className = '' }: Props = $props()

  const showRetry = $derived(Boolean(onretry && error?.isRetryable))

  /**
   * Whether to show the raw code beneath the message.
   *
   * Only when the message cannot stand on its own. "The cluster refused the
   * connection" followed by "unreachable" tells the reader nothing they did
   * not just read, in vocabulary they did not ask for — but when all we can
   * say is "An unexpected error occurred", the code is the only thing anybody
   * can act on or quote in a bug report.
   */
  const showCode = $derived(error?.code === 'internal' || error?.code === 'unknown')

  /**
   * Not every failure is an alarm, and colouring them all alike wears the
   * alarm out.
   *
   * A cluster that is no longer in the kubeconfig is not broken — it is a
   * state change the operator very likely caused, by deleting a context or
   * rebuilding a cluster. Announcing that in the same full red as a crashed
   * request teaches people to dismiss red banners, which is exactly the habit
   * you do not want when a real one arrives.
   *
   * The distinction is severity, not importance: the informational tone is
   * still a banner, still says the same words, still cannot be missed.
   */
  const INFORMATIONAL = new Set(['cluster_not_found', 'no_active_cluster'])
  const isAlarm = $derived(!error || !INFORMATIONAL.has(error.code))
</script>

{#if error}
  <div
    role={isAlarm ? 'alert' : 'status'}
    class="flex items-start gap-3 rounded-sm border px-4 py-3 {isAlarm
      ? 'border-error/20 bg-error-container/80 text-on-error-container'
      : 'border-outline-variant/40 bg-surface-container-high text-on-surface'} {className}"
  >
    {#if isAlarm}
      <AlertCircle class="mt-0.5 size-5 shrink-0 text-error" strokeWidth={2} />
    {:else}
      <Info class="mt-0.5 size-5 shrink-0 text-on-surface-variant" strokeWidth={2} />
    {/if}

    <div class="min-w-0 flex-1">
      <p class="text-body-medium font-medium" data-selectable>{error.message}</p>
      {#if showCode}
        <p class="mt-0.5 text-body-small opacity-60" data-selectable>{error.code}</p>
      {/if}
    </div>

    <div class="flex shrink-0 items-center gap-1">
      {#if showRetry}
        <Button variant="text" class={isAlarm ? 'text-on-error-container' : ''} onclick={onretry}>Retry</Button>
      {/if}
      {#if ondismiss}
        <Button variant="text" class={isAlarm ? 'text-on-error-container' : ''} onclick={ondismiss} label="Dismiss">
          Dismiss
        </Button>
      {/if}
    </div>
  </div>
{/if}
