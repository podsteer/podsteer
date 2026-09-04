<!--
  Open and copy, for one running forward.

  Two controls rather than one clickable address: opening was always here,
  but there was no way to get the address onto the clipboard without
  selecting it by hand, which a monospace address in a proportional-font
  panel makes fiddlier than it should be.

  OPENS 127.0.0.1, NOT `forward.address`. The address field is built for
  DISPLAY — it reads as "localhost", which is what an operator expects to
  see — but SystemAPI.OpenURL hands the string straight to the OS, and
  127.0.0.1 is what skips a DNS resolution `localhost` does not strictly
  need but also does not need to pay for. Copy hands over the same string
  Open uses, so what lands in a terminal is exactly what worked here.
-->
<script lang="ts">
  import { flash } from '$lib/flash.svelte'
  import { openURL, forwardBrowserURL, type PortForward } from '$lib/api/client'
  import { ExternalLink, Copy, Check } from '@lucide/svelte'

  interface Props {
    forward: PortForward
  }

  let { forward }: Props = $props()

  const url = $derived(forwardBrowserURL(forward))
  const copied = flash(900)

  async function copy(): Promise<void> {
    await navigator.clipboard.writeText(url)
    copied.show()
  }
</script>

<span class="flex min-w-0 items-center gap-1">
  <!-- Opened in the real browser, never the webview: this is a link to
       something on the operator's OWN machine, and loading it inside the
       application would replace PodSteer with no way back. -->
  <button
    type="button"
    onclick={() => void openURL(url)}
    class="resource-link flex min-w-0 items-center gap-1.5 text-left"
    title="Open {url}"
  >
    <span class="truncate">{forward.address}</span>
    <ExternalLink class="size-3.5 shrink-0" strokeWidth={1.8} />
  </button>

  <button
    type="button"
    onclick={() => void copy()}
    aria-label="Copy {url}"
    title="Copy address"
    class="state-layer grid size-6 shrink-0 place-items-center rounded-xs
           text-on-surface-variant transition-colors duration-100
           hover:bg-surface-container hover:text-on-surface"
  >
    {#if copied.on}
      <Check class="size-3.5 text-success" strokeWidth={2.5} />
    {:else}
      <Copy class="size-3.5" strokeWidth={1.8} />
    {/if}
  </button>
</span>
