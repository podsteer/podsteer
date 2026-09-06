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
  import { copyText } from '$lib/clipboard'
  import { flash } from '$lib/flash.svelte'
  import { Check, Copy } from '@lucide/svelte'

  interface Props {
    /** The command, already composed by $lib/kubectl. */
    command: string
    /**
     * The heading above the command.
     *
     * Defaults to "kubectl equivalent" because that is what every caller
     * this strip was written for shows. It is a prop rather than a constant
     * because the strip is now also used for a command PodSteer does NOT
     * make on the operator's behalf — `helm rollback` and `helm uninstall`,
     * which ADR 6 deliberately leaves for the operator's own shell — and
     * heading that "kubectl equivalent" would claim both the wrong binary
     * and the wrong relationship.
     */
    label?: string
  }

  let { command, label = 'kubectl equivalent' }: Props = $props()

  /**
   * The same affordance RowMenu's "copy" action uses: an icon and a word that
   * swap together and hold for a beat rather than flicker straight back — see
   * $lib/flash.svelte. Copying gives nothing back on its own, so this is what
   * says it happened.
   */
  const copied = flash(1500)

  async function copy(): Promise<void> {
    // Quiet about failing and never wrong about succeeding: the command is on
    // screen and selectable either way, so there is nothing useful to raise —
    // but the tick has to mean the command is on the clipboard, because this
    // strip exists so an operator can take that command elsewhere and run it.
    // See $lib/clipboard.
    if (await copyText(command)) copied.show()
    else copied.cancel()
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
        {label}
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
