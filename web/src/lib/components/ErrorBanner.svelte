<!--
  An inline error surface in the MD3 error-container role.

  Shows the backend's message and, when the failure is one a second attempt
  could clear, a retry action. Errors that cannot be retried — an RBAC denial,
  a deleted resource — get no button, because offering one only invites the
  operator to click it twice before concluding the same thing.
-->
<script lang="ts">
  import type { ApiError } from '$lib/api/errors'
  import Button from './Button.svelte'

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
    class="flex items-start gap-3 rounded-md bg-error-container px-4 py-3
           text-on-error-container {className}"
  >
    <svg
      class="mt-0.5 size-5 shrink-0"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M12 8v5M12 16.5v.01" />
    </svg>

    <div class="min-w-0 flex-1">
      <p class="text-body-medium" data-selectable>{error.message}</p>
      <!-- The code is what an engineer needs when the operator forwards a
           screenshot; it is deliberately quiet rather than hidden. -->
      <p class="mt-0.5 text-body-small opacity-70">{error.code}</p>
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
