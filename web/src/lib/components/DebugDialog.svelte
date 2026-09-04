<!--
  Adds an ephemeral debug container to a pod — `kubectl debug -it POD
  --image=… --target=…`.

  The one thing this dialog must say plainly, and does: an ephemeral container
  CANNOT be removed once added. It stays in the pod's spec until the pod is
  deleted. That is Kubernetes' behaviour, not PodSteer's, and it is why this is
  a deliberate dialog rather than a one-click action.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { preferences } from '$stores/preferences.svelte'
  import { debugRequest } from '$lib/debugShell'
  import { debug as kubectlDebug } from '$lib/kubectl'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'

  interface Props {
    open: boolean
    clusterId: string
    namespace: string
    pod: string
    /** The pod's container names, for the --target selector. */
    containers: string[]
    /** The group's name when this cluster is marked production, else null. */
    productionGroup: string | null
    onclose: () => void
    /** Called with the chosen target (may be empty), image and command. */
    onconfirm: (target: string, image: string, command: string[]) => void
  }

  let { open, clusterId, namespace, pod, containers, productionGroup, onclose, onconfirm }: Props =
    $props()

  let image = $state(preferences.debugImage)
  let target = $state('')
  let command = $state('sh')

  // Fresh inputs each time the dialog opens, seeded from what was last used.
  $effect(() => {
    if (!open) return
    image = preferences.debugImage
    target = containers[0] ?? ''
    command = 'sh'
  })

  const request = $derived(debugRequest(image, command))
  const kubectlCommand = $derived(
    kubectlDebug(clusterId, pod, namespace, request.image, target, request.command),
  )

  function confirm(): void {
    // Remember the image so the next debug proposes it; the target and command
    // are per-pod and per-investigation, so they are not remembered.
    preferences.setDebugImage(image)
    onconfirm(target, request.image, request.command)
  }

  function onKeydown(event: KeyboardEvent): void {
    if (!open || event.key !== 'Escape') return
    if (!escape?.owns()) return
    onclose()
  }

  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!open) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close dialog"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed top-1/2 left-1/2 z-[70] w-[30rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Debug pod"
  >
    <h2 class="text-headline-small text-on-surface">Debug {pod}</h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Adds a temporary container with its own tools to this pod and opens a shell into it — the
      equivalent of <code class="text-on-surface">kubectl debug</code>. Targeting a container shares
      its process namespace, so you can see and signal its processes.
    </p>

    <div class="mt-4 flex flex-col gap-3">
      <label class="block">
        <span class="text-body-small text-on-surface-variant">Image</span>
        <input
          type="text"
          bind:value={image}
          placeholder="busybox:1.37"
          class="field mt-1 w-full px-3 py-2 font-mono text-body-small"
        />
      </label>

      <label class="block">
        <span class="text-body-small text-on-surface-variant">Target container (optional)</span>
        <select bind:value={target} class="field mt-1 w-full px-3 py-2 text-body-medium">
          <option value="">— none (share the pod's namespaces only) —</option>
          {#each containers as name (name)}
            <option value={name}>{name}</option>
          {/each}
        </select>
      </label>

      <label class="block">
        <span class="text-body-small text-on-surface-variant">Command</span>
        <input
          type="text"
          bind:value={command}
          placeholder="sh"
          class="field mt-1 w-full px-3 py-2 font-mono text-body-small"
        />
      </label>
    </div>

    <!-- The irremovable fact, stated where it cannot be missed. -->
    <p class="mt-4 rounded-sm border border-gauge-warn/40 bg-gauge-warn/10 px-3 py-2 text-body-small text-on-surface-variant">
      An ephemeral container cannot be removed once added. It stays in the pod's spec until the pod
      is deleted — this is Kubernetes' behaviour, not something PodSteer can undo.
    </p>

    {#if productionGroup}
      <p class="mt-3 rounded-sm border border-error/40 bg-error/10 px-3 py-2 text-body-small text-on-surface-variant">
        This cluster is in {productionGroup}, marked production.
      </p>
    {/if}

    <div class="mt-4">
      <KubectlHint command={kubectlCommand} />
    </div>

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={confirm}>Start debug</Button>
    </div>
  </div>
{/if}
