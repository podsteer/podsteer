<!--
  The kubectl command that does what this dialog is about to do — a quiet
  strip for the bottom of a write dialog, so the GUI teaches kubectl instead
  of hiding it.

  Read-only but for the copy button: this is not a form field, it is a
  transcript of the request PodSteer is about to make on the operator's
  behalf, in the words `kubectl` itself would need. See $lib/kubectl for how
  it is composed.
-->
<script lang="ts">
  import { flash } from '$lib/flash.svelte'
  import { Check, Copy } from '@lucide/svelte'

  interface Props {
    /** The command, already composed by $lib/kubectl. */
    command: string
  }

  let { command }: Props = $props()

  /**
   * The same affordance RowMenu's "copy" action uses: an icon and a word that
   * swap together and hold for a beat rather than flicker straight back — see
   * $lib/flash.svelte. Copying gives nothing back on its own, so this is what
   * says it happened.
   */
  const copied = flash(1500)

  async function copy(): Promise<void> {
    try {
      await navigator.clipboard.writeText(command)
      copied.show()
    } catch {
      // Silent, like every other copy control here: the command is on screen
      // and selectable either way, and the clipboard is a permissioned API
      // that can simply refuse.
      copied.cancel()
    }
  }

  // Nothing left running behind a component that has gone away.
  $effect(() => () => copied.cancel())
</script>

<!-- No margin of its own: callers sit in a stacked dialog body (which spaces
     with mt-4 between children), a gap-managed footer, or a single-row
     footer beside other flex-1 content — each needs a different wrapper, so
     spacing is the caller's decision. -->
<div class="min-w-0 rounded-sm border border-outline-variant/60 bg-surface-container-lowest p-3">
  <div class="flex items-start justify-between gap-3">
    <div class="min-w-0">
      <p class="text-label-small font-semibold tracking-wider text-on-surface-variant/60 uppercase">
        kubectl equivalent
      </p>
      <!-- break-all rather than break-words: a long context name or image
           reference is one "word" with nothing to wrap at, the same reason
           DetailList breaks its label column this way. -->
      <p
        class="mt-1 font-mono text-body-small break-all whitespace-pre-wrap text-on-surface-variant"
        data-selectable
      >
        {command}
      </p>
    </div>

    <button
      type="button"
      onclick={copy}
      title={copied.on ? 'Copied' : 'Copy command'}
      aria-label={copied.on ? 'Copied' : 'Copy command'}
      class="state-layer flex shrink-0 cursor-pointer items-center gap-1 rounded-sm px-1.5 py-1
             text-label-medium transition-colors duration-75
             {copied.on ? 'text-success' : 'text-on-surface-variant hover:text-on-surface'}"
    >
      {#if copied.on}
        <Check class="size-3.5 shrink-0" strokeWidth={2.5} />
        Copied!
      {:else}
        <Copy class="size-3.5 shrink-0" strokeWidth={1.8} />
        Copy
      {/if}
    </button>
  </div>
</div>
