<!--
  Where PodSteer's own outbound calls go.

  THREE MODES, NOT A SWITCH, and the middle one is the reason. "Leave the
  environment alone" and "never use a proxy" look like the same off until you
  are the operator whose HTTPS_PROXY reaches the internet but not their private
  API server — for whom the first is the problem and the second is the fix. A
  tick box could only have offered one of them, and it would have been the
  wrong one for everybody it silently changed.

  The default changes nothing: with no dialer installed, client-go reads
  HTTPS_PROXY, HTTP_PROXY and NO_PROXY exactly as it always has.
-->
<script lang="ts">
  import { onMount } from 'svelte'
  import { proxy, PROXY_MODES, type ProxyMode } from '$stores/proxy.svelte'
  import Button from './Button.svelte'
  import Radio from './Radio.svelte'
  import { Check, Loader } from '@lucide/svelte'

  interface Props {
    /** Whether the settings file can be written at all. */
    canWrite: boolean
    /** Why not, when it cannot. */
    readOnlyReason?: string
  }

  let { canWrite, readOnlyReason = '' }: Props = $props()

  let mode = $state<ProxyMode>('environment')
  let url = $state('')
  let noProxy = $state('')

  onMount(() => {
    void proxy.load().then(() => {
      mode = (PROXY_MODES as readonly string[]).includes(proxy.current.mode)
        ? (proxy.current.mode as ProxyMode)
        : 'environment'
      url = proxy.current.url
      noProxy = proxy.current.noProxy
    })
  })

  const dirty = $derived(
    mode !== proxy.current.mode ||
      url.trim() !== proxy.current.url ||
      noProxy.trim() !== proxy.current.noProxy,
  )
</script>

<div class="flex flex-col gap-4">
  <fieldset>
    <legend class="text-body-medium text-on-surface-variant">
      How PodSteer reaches your clusters
    </legend>
    <div class="mt-2 flex flex-col gap-1">
      <Radio name="proxy-mode" bind:group={mode} value="environment" dense>
        Use the environment ({'HTTPS_PROXY'}, {'NO_PROXY'})
      </Radio>
      <Radio name="proxy-mode" bind:group={mode} value="none" dense>
        No proxy, even if the environment names one
      </Radio>
      <Radio name="proxy-mode" bind:group={mode} value="manual" dense>Use this proxy</Radio>
    </div>
  </fieldset>

  <!-- Present but disabled rather than hidden: a field that appears when a
       radio is chosen makes the choice feel like it did something before it
       has, and the values are worth keeping visible while somebody switches
       modes to compare. -->
  <label class="block">
    <span class="text-body-medium text-on-surface-variant">Proxy URL</span>
    <input
      type="text"
      bind:value={url}
      disabled={mode !== 'manual' || !canWrite}
      placeholder="http://proxy.example:3128"
      autocomplete="off"
      spellcheck="false"
      class="field mt-1 w-full px-3 py-2 text-body-medium"
    />
  </label>

  <label class="block">
    <span class="text-body-medium text-on-surface-variant">Exceptions</span>
    <input
      type="text"
      bind:value={noProxy}
      disabled={mode !== 'manual' || !canWrite}
      placeholder="10.0.0.0/8,.internal,localhost"
      autocomplete="off"
      spellcheck="false"
      class="field mt-1 w-full px-3 py-2 text-body-medium"
    />
    <span class="mt-1 block text-body-medium text-on-surface-variant">
      The same syntax as {'NO_PROXY'}, handled by the same code Go uses for it.
    </span>
  </label>

  {#if proxy.error}
    <p class="text-body-medium text-error">{proxy.error}</p>
  {/if}

  {#if !canWrite && readOnlyReason}
    <p class="text-body-medium text-on-surface-variant">{readOnlyReason}</p>
  {/if}

  <div class="flex items-center gap-3">
    <Button
      variant="filled"
      disabled={!canWrite || !dirty || proxy.status === 'saving'}
      onclick={() => void proxy.save(mode, url, noProxy)}
    >
      {proxy.status === 'saving' ? 'Applying…' : 'Apply'}
    </Button>
    {#if proxy.status === 'saving'}
      <Loader class="size-4 animate-spin text-on-surface-variant" strokeWidth={2} />
    {:else if proxy.saved && !dirty}
      <!-- Says what actually happened: the setting is written AND every open
           cluster's client was rebuilt, which is the half an operator would
           otherwise have to discover by wondering why a tab still worked. -->
      <span class="flex items-center gap-1 text-body-medium text-success">
        <Check class="size-4" strokeWidth={2.5} />
        Applied, and open clusters reconnected
      </span>
    {/if}
  </div>
</div>
