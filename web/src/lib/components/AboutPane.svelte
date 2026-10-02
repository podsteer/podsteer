<!--
  About: the version, and the two ways of reporting a problem.

  "Copy debug info" shows the block it will copy BEFORE copying it, so the
  operator can read exactly what leaves their machine. It holds versions and
  platform only (see $lib/debugInfo) — no cluster names, hosts, namespaces or
  paths. Nothing here contacts anything: "Report a bug" hands a GitHub URL to
  the operating system's browser, and the operator decides what to paste.
-->
<script lang="ts">
  import { onMount } from 'svelte'
  import { debugInfo, openURL } from '$lib/api/client'
  import { copyText } from '$lib/clipboard'
  import { BUG_REPORT_URL, formatDebugInfo } from '$lib/debugInfo'
  import { workspace } from '$stores/workspace.svelte'
  import Button from './Button.svelte'

  let text = $state('')
  let copied = $state<'idle' | 'ok' | 'failed'>('idle')

  onMount(() => {
    void debugInfo()
      .then((info) => {
        text = formatDebugInfo(
          info,
          workspace.sessions.map((session) => session.cluster.version ?? ''),
          navigator.userAgent,
        )
      })
      .catch(() => {
        text = 'Debug info is unavailable.'
      })
  })

  async function copy(): Promise<void> {
    copied = (await copyText(text)) ? 'ok' : 'failed'
  }

  async function report(): Promise<void> {
    try {
      await openURL(BUG_REPORT_URL)
    } catch {
      // The address is shown below, so a refused hand-off is not a dead end.
    }
  }
</script>

<div class="flex flex-col gap-3">
  <div>
    <h3 class="text-title-medium text-on-surface">Debug info</h3>
    <p class="mt-0.5 text-body-medium text-on-surface-variant">
      This is exactly what “Copy debug info” puts on your clipboard. It lists versions and your
      platform only: no cluster, context or host names, namespaces or file paths.
    </p>
  </div>

  <pre
    class="overflow-x-auto rounded-md bg-surface-container p-3 font-mono text-body-small text-on-surface"
    aria-label="Debug info">{text}</pre>

  <div class="flex flex-wrap items-center gap-2">
    <Button variant="filled" onclick={copy} disabled={text === ''}>Copy debug info</Button>
    <Button variant="outlined" onclick={report}>Report a bug</Button>
    <span class="text-body-small text-on-surface-variant" aria-live="polite">
      {#if copied === 'ok'}Copied.{:else if copied === 'failed'}Could not reach the clipboard; select
        the text above and copy it.{/if}
    </span>
  </div>

  <p class="text-body-small text-on-surface-variant">
    Report a bug opens {BUG_REPORT_URL} in your browser. PodSteer sends nothing itself.
  </p>
</div>
