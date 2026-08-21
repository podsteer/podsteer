<!--
  An inline error surface in the MD3 error-container role.

  Shows the backend's message and, when the failure is one a second attempt
  could clear, a retry action.
-->
<script lang="ts">
  import type { ApiError } from '$lib/api/errors'
  import Button from './Button.svelte'
  import { AlertCircle } from '@lucide/svelte'

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
</script>

{#if error}
  <div
    role="alert"
    class="flex items-start gap-3 rounded-sm border border-error/20 bg-error-container/80 px-4 py-3
           text-on-error-container {className}"
  >
    <AlertCircle class="mt-0.5 size-5 shrink-0 text-error" strokeWidth={2} />

    <div class="min-w-0 flex-1">
      <p class="text-body-medium font-medium" data-selectable>{error.message}</p>
      <p class="mt-0.5 text-body-small opacity-60">{error.code}</p>
    </div>

    <div class="flex shrink-0 items-center gap-1">
      {#if showRetry}
        <Button variant="text" class="text-on-error-container" onclick={onretry}>Retry</Button>
      {/if}
      {#if ondismiss}
        <Button variant="text" class="text-on-error-container" onclick={ondismiss} label="Dismiss">
          Dismiss
        </Button>
      {/if}
    </div>
  </div>
{/if}
