<!--
  Opens a root shell on a node — a privileged pod that enters the node's host
  namespaces with nsenter, the way `kubectl node-shell` and Lens do.

  This is the most powerful thing PodSteer can do, so the dialog says exactly
  what it is (root on the node, host PID/network/mount namespaces) and, on a
  cluster marked production, requires typing the node's name to confirm — the
  same guardrail Delete uses. See CLAUDE.md's read-only section and
  $lib/confirm.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { preferences } from '$stores/preferences.svelte'
  import { nodeShellRequest } from '$lib/debugShell'
  import { nameConfirmed } from '$lib/confirm'
  import { debugNode as kubectlDebugNode } from '$lib/kubectl'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'
  import { TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    clusterId: string
    node: string
    /** The group's name when this cluster is marked production, else null. */
    productionGroup: string | null
    onclose: () => void
    /** Called with the chosen image and namespace. */
    onconfirm: (image: string, namespace: string) => void
  }

  let { open, clusterId, node, productionGroup, onclose, onconfirm }: Props = $props()

  let image = $state(preferences.nodeShellImage)
  let namespace = $state(preferences.nodeShellNamespace)
  let typedName = $state('')

  $effect(() => {
    if (!open) return
    image = preferences.nodeShellImage
    namespace = preferences.nodeShellNamespace
    typedName = ''
  })

  const request = $derived(nodeShellRequest(image, namespace))
  const kubectlCommand = $derived(kubectlDebugNode(clusterId, node, request.image))

  // On a production cluster, the node's name must be typed — a node shell is
  // too powerful to open on one keystroke there.
  const requiresTypedName = $derived(!!productionGroup)
  const canConfirm = $derived(!requiresTypedName || nameConfirmed(typedName, node))

  function confirm(): void {
    if (!canConfirm) return
    preferences.setNodeShellImage(image)
    preferences.setNodeShellNamespace(namespace)
    onconfirm(request.image, request.namespace)
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
    aria-label="Open a node shell"
  >
    <h2 class="flex items-center gap-2 text-headline-small text-on-surface">
      <TriangleAlert class="size-5 text-gauge-warn" strokeWidth={2} aria-hidden="true" />
      Node shell on {node}
    </h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Opens a <strong class="text-on-surface">root shell on the node</strong>, in its host process,
      network and mount namespaces. It runs a privileged pod pinned to this node and enters PID 1's
      namespaces with nsenter — anything you can do on the node's own console, you can do here.
    </p>

    <div class="mt-4 flex flex-col gap-3">
      <label class="block">
        <span class="text-body-small text-on-surface-variant">Image</span>
        <input
          type="text"
          bind:value={image}
          placeholder="docker.io/library/alpine:3.20"
          class="field mt-1 w-full px-3 py-2 font-mono text-body-small"
        />
      </label>

      <label class="block">
        <span class="text-body-small text-on-surface-variant">Namespace</span>
        <input
          type="text"
          bind:value={namespace}
          placeholder="kube-system"
          class="field mt-1 w-full px-3 py-2 font-mono text-body-small"
        />
      </label>
    </div>

    <p class="mt-4 rounded-sm border border-gauge-warn/40 bg-gauge-warn/10 px-3 py-2 text-body-small text-on-surface-variant">
      The pod is deleted when you close its terminal, and self-destructs after one hour as a
      backstop. While it runs it appears in the activity list, where it can also be stopped.
    </p>

    {#if productionGroup}
      <div class="mt-3 rounded-sm border border-error/40 bg-error/10 px-3 py-2">
        <p class="text-body-small text-on-surface-variant">
          This cluster is in {productionGroup}, marked production. Type the node's name to confirm.
        </p>
        <input
          type="text"
          bind:value={typedName}
          placeholder={node}
          autocomplete="off"
          spellcheck="false"
          class="field mt-2 w-full px-3 py-2 font-mono text-body-small"
        />
      </div>
    {/if}

    <div class="mt-4">
      <KubectlHint command={kubectlCommand} />
    </div>

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={confirm} disabled={!canConfirm}>Open node shell</Button>
    </div>
  </div>
{/if}
