<!--
  One deliberately revealed Secret value.

  Everything here is a reaction to how the incumbents get this wrong.

  Freelens renders the raw base64 in a plain text input and calls it masked.
  Base64 is an encoding, not a cipher; a screenshot of that pane leaks the
  credential to anyone who can type `base64 -d`. So nothing is ever rendered
  encoded here: a value is either hidden or it is plaintext the operator
  asked for.

  Freelens's reveal is also one-way — once shown, the control is replaced by
  the value and there is no way back short of remounting the pane — and its
  reveal state is global, so a value unmasked in private is still unmasked
  when an unrelated pod is opened on a shared screen. Reveal here is scoped to
  this one key, re-hideable, and CLEARS ITSELF: on a timer, and whenever the
  window loses focus, which is the moment a screen share usually starts.

  Headlamp has an open bug where the secret flashes on screen before the
  authorisation error arrives. Nothing is rendered here until the call has
  resolved, so a denial shows a denial and never a glimpse.
-->
<script lang="ts">
  import { revealSecretKey } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { Eye, EyeOff, Loader } from '@lucide/svelte'

  interface Props {
    clusterId: string
    namespace: string
    /** The Secret holding it, and the key within it. */
    secret: string
    secretKey: string
  }

  let { clusterId, namespace, secret, secretKey }: Props = $props()

  /**
   * The plaintext, while it is on screen.
   *
   * Component-local and never in a store: a store outlives the pane, is
   * inspectable from anywhere in the app, and would be serialised by anything
   * that ever snapshots application state. This dies with the component.
   */
  let value = $state<string | null>(null)
  let loading = $state(false)
  let error = $state('')

  /** How long a revealed value stays on screen before hiding itself. */
  const HIDE_AFTER_MS = 30_000
  let timer: ReturnType<typeof setTimeout> | null = null

  function hide(): void {
    value = null
    error = ''
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
  }

  async function reveal(): Promise<void> {
    loading = true
    error = ''
    try {
      // Assigned only after the call resolves. Rendering optimistically is
      // how a client shows a secret to somebody who was not allowed to read
      // it, for the moment before the error lands.
      value = await revealSecretKey(clusterId, namespace, secret, secretKey)
      timer = setTimeout(hide, HIDE_AFTER_MS)
    } catch (cause) {
      error = toApiError(cause).message
    } finally {
      loading = false
    }
  }

  /**
   * Hides when the window loses focus.
   *
   * Which is, in practice, the moment somebody alt-tabs to start a screen
   * share, or accepts a call. It costs a click to get back and removes the
   * failure mode where a credential is sitting revealed behind a window
   * nobody remembers is open.
   */
  $effect(() => () => hide())
</script>

<svelte:window onblur={hide} />

<span class="inline-flex min-w-0 items-baseline gap-2">
  {#if error}
    <span class="text-error">{error}</span>
  {:else if value !== null}
    <span class="min-w-0 font-mono break-all text-on-surface" data-selectable>{value}</span>
  {:else}
    <span class="text-on-surface-variant">
      &lt;set to the key '{secretKey}' in secret '{secret}'&gt;
    </span>
  {/if}

  <button
    type="button"
    onclick={() => (value === null && !error ? reveal() : hide())}
    disabled={loading}
    class="state-layer inline-grid size-6 shrink-0 place-items-center rounded-full
           text-on-surface-variant transition-colors duration-100
           hover:bg-surface-container hover:text-on-surface disabled:opacity-50"
    aria-label={value === null ? `Reveal ${secretKey}` : `Hide ${secretKey}`}
    title={value === null
      ? 'Read this value from the Secret. This is an audited read, and it hides itself again shortly.'
      : 'Hide'}
  >
    {#if loading}
      <Loader class="size-3.5 animate-spin" strokeWidth={2} />
    {:else if value === null}
      <Eye class="size-3.5" strokeWidth={1.8} />
    {:else}
      <EyeOff class="size-3.5" strokeWidth={1.8} />
    {/if}
  </button>
</span>
