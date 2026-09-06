<!--
  Opens a shell INSIDE the cluster — an ordinary, unprivileged pod PodSteer
  creates in a namespace and attaches to, for somebody who wants kubectl, dig
  and curl from the cluster's own network.

  Two things separate this dialog from NodeShellDialog beside it, and both are
  the point of the feature:

  - THE NAMESPACE FOLLOWS THE TAB, so the two terminals and the rest of the
    interface agree. When the tab is on "All namespaces" there is no answer and
    the field opens EMPTY with the reason beside it — never a fallback to a
    system namespace, which is where the node shell's own default points and is
    the wrong answer here. See $lib/clusterShell.
  - REUSE IS OFFERED FIRST. If PodSteer already has a RUNNING shell pod in this
    namespace, attaching to it is the primary action; creating a second one is
    still there. Pods of ours in any other state are REPORTED and never offered
    — an attach to an exited pod fails for a reason the offer gave nobody a way
    to see — and saying so is what explains a namespace that has been
    accumulating them.
-->
<script lang="ts">
  import { untrack } from 'svelte'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { preferences } from '$stores/preferences.svelte'
  import {
    canOpenClusterShell,
    clusterShellRequest,
    exitedShellsNote,
    CLUSTER_SHELL_NAMESPACE_PROMPT,
  } from '$lib/clusterShell'
  import { findClusterShells, type ClusterShellCandidate } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { runShellPod as kubectlRunShellPod } from '$lib/kubectl'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'
  import { Container, Loader } from '@lucide/svelte'

  interface Props {
    open: boolean
    clusterId: string
    /** The namespace the tab is filtered to, or '' when it is on every one. */
    namespace: string
    onclose: () => void
    /** Called with the image and namespace for a NEW pod. */
    onconfirm: (image: string, namespace: string) => void
    /** Called with the namespace and pod name to attach to an EXISTING one. */
    onattach: (namespace: string, pod: string) => void
  }

  let { open, clusterId, namespace, onclose, onconfirm, onattach }: Props = $props()

  let image = $state(preferences.clusterShellImage)
  // Seeded from the prop through `untrack`, and re-seeded by the effect below
  // when the dialog opens. Reading it reactively here is what Svelte warns
  // about and would be wrong anyway: this field is the operator's from the
  // moment they can type in it, and a reactive seed would overwrite what they
  // typed the next time anything upstream changed.
  let chosenNamespace = $state(untrack(() => namespace))

  /** What this namespace already holds. Empty until the look-up answers. */
  let reusable = $state.raw<ClusterShellCandidate[]>([])
  let exited = $state.raw<ClusterShellCandidate[]>([])
  let looking = $state(false)
  let lookupError = $state('')

  const request = $derived(clusterShellRequest(image, chosenNamespace))
  const canConfirm = $derived(canOpenClusterShell(chosenNamespace))
  const kubectlCommand = $derived(
    canConfirm ? kubectlRunShellPod(clusterId, request.namespace, request.image) : '',
  )

  $effect(() => {
    if (!open) return
    image = preferences.clusterShellImage
    chosenNamespace = namespace
  })

  /**
   * Looks for pods PodSteer already has here, whenever the namespace settles.
   *
   * ONE READ PER NAMESPACE, on a dialog somebody opened — never on the refresh
   * tick. A generation token guards the await the way every other read in this
   * application does: the operator can type a second namespace while the first
   * answer is in the air, and a late answer must not describe a namespace the
   * dialog has left.
   */
  let lookupGeneration = 0
  $effect(() => {
    const target = chosenNamespace.trim()
    if (!open || target === '') {
      reusable = []
      exited = []
      lookupError = ''
      return
    }

    const generation = ++lookupGeneration
    looking = true
    lookupError = ''
    void (async () => {
      try {
        const plan = await findClusterShells(clusterId, target)
        if (generation !== lookupGeneration) return
        reusable = plan.reusable
        exited = plan.other
      } catch (cause) {
        if (generation !== lookupGeneration) return
        // A failure to LOOK is not a failure to open: the operator can still
        // create one, and saying nothing about what is already there is the
        // honest outcome rather than a blocked dialog.
        reusable = []
        exited = []
        lookupError = toApiError(cause).message
      } finally {
        if (generation === lookupGeneration) looking = false
      }
    })()
  })

  function confirm(): void {
    if (!canConfirm) return
    preferences.setClusterShellImage(image)
    onconfirm(request.image, request.namespace)
  }

  function attach(pod: string): void {
    if (!canConfirm) return
    onattach(request.namespace, pod)
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
    class="fixed top-1/2 left-1/2 z-[70] w-[32rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Open an in-cluster shell"
  >
    <h2 class="flex items-center gap-2 text-headline-small text-on-surface">
      <Container class="size-5 text-primary" strokeWidth={2} aria-hidden="true" />
      In-cluster shell
    </h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Runs a throwaway pod in this cluster and attaches to a shell in it, so
      <code class="font-mono">kubectl</code>, <code class="font-mono">dig</code> and
      <code class="font-mono">curl</code> see the cluster's network from the inside — as a workload
      does. It is an <strong class="text-on-surface">ordinary, unprivileged pod</strong>: not a
      debug container on somebody's pod, and not a root shell on a node.
    </p>

    <div class="mt-4 flex flex-col gap-3">
      <label class="block">
        <span class="text-body-small text-on-surface-variant">Namespace</span>
        <input
          type="text"
          bind:value={chosenNamespace}
          placeholder="name the namespace"
          autocomplete="off"
          spellcheck="false"
          class="field mt-1 w-full px-3 py-2 font-mono text-body-small"
        />
      </label>

      {#if !canConfirm}
        <p class="rounded-sm border border-outline-variant/60 bg-surface-container px-3 py-2 text-body-small text-on-surface-variant">
          {CLUSTER_SHELL_NAMESPACE_PROMPT}
        </p>
      {/if}

      <label class="block">
        <span class="text-body-small text-on-surface-variant">Image</span>
        <input
          type="text"
          bind:value={image}
          placeholder="docker.io/cloudresty/dockydeb:v1.2.28-nonroot"
          class="field mt-1 w-full px-3 py-2 font-mono text-body-small"
        />
      </label>
      <p class="text-body-small text-on-surface-variant/70">
        The default is the nonroot build. The pod asks to run as non-root, with no privilege
        escalation, every capability dropped and the runtime's default seccomp profile, so it is
        admitted in a namespace enforcing Pod Security's <code class="font-mono">restricted</code>
        profile. If admission still refuses, the message here is the API server's own.
      </p>
    </div>

    <!-- What this namespace already holds. Only a RUNNING pod is an offer. -->
    {#if looking}
      <p class="mt-4 flex items-center gap-2 text-body-small text-on-surface-variant">
        <Loader class="size-3.5 animate-spin" strokeWidth={2} aria-hidden="true" />
        Looking for a shell PodSteer already has here…
      </p>
    {:else if reusable.length > 0}
      <div class="mt-4 rounded-sm border border-primary/40 bg-primary/10 px-3 py-2">
        <p class="text-body-small text-on-surface-variant">
          PodSteer already has {reusable.length === 1 ? 'a shell' : 'shells'} running in this namespace.
          Attaching costs no new pod.
        </p>
        <ul class="mt-2 flex flex-col gap-1">
          {#each reusable as candidate (candidate.pod)}
            <li class="flex items-center justify-between gap-2">
              <span class="min-w-0 truncate font-mono text-body-small text-on-surface">
                {candidate.pod}
              </span>
              <Button variant="outlined" onclick={() => attach(candidate.pod)}>Attach</Button>
            </li>
          {/each}
        </ul>
      </div>
    {/if}

    {#if exited.length > 0}
      <p class="mt-3 rounded-sm border border-outline-variant/60 bg-surface-container px-3 py-2 text-body-small text-on-surface-variant">
        {exitedShellsNote(exited.length)}
      </p>
    {/if}

    {#if lookupError}
      <p class="mt-3 text-body-small text-on-surface-variant/70">
        PodSteer could not check what is already here: {lookupError}
      </p>
    {/if}

    <p class="mt-4 rounded-sm border border-outline-variant/60 bg-surface-container px-3 py-2 text-body-small text-on-surface-variant">
      The pod is deleted when you close its terminal, and self-destructs after one hour as a
      backstop. While it runs it appears in the activity list, where it can also be stopped.
    </p>

    {#if kubectlCommand}
      <div class="mt-4">
        <KubectlHint command={kubectlCommand} />
      </div>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={confirm} disabled={!canConfirm}>
        {reusable.length > 0 ? 'Create another' : 'Open shell'}
      </Button>
    </div>
  </div>
{/if}
