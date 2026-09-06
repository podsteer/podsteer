<!--
  Settings → Terminal images: the two container images PodSteer puts INTO a
  cluster, and the namespace one of them lands in.

  WHY THESE ARE IN SETTINGS AT ALL. All three have been persisted preferences
  since the debug container and node shell shipped, but the only way to change
  one was to open the dialog that uses it and edit the field there — which
  meant an operator could only discover the setting at the moment they were
  trying to do something else with it, and an operator who could NOT pull from
  Docker Hub discovered it as a pod stuck in ImagePullBackOff. An air-gapped
  cluster mirrors images into its own registry; this is where you say so, once,
  rather than retyping a reference into a dialog every time.

  WHY THE TWO IMAGES DIFFER, WHICH IS THE THING WORTH EXPLAINING HERE. A debug
  container is injected into somebody else's pod, in their namespace, so Pod
  Security admission judges it — and under the `restricted` level a container
  running as root is rejected before it starts, which is why that default is
  the nonroot variant. A node shell is a privileged pod entering the host's
  namespaces with nsenter; that is root by definition and a nonroot image could
  not do the one thing it exists for. Same repository, two variants, for two
  different admission situations.

  Both are PINNED to an exact tag rather than floating, the same rule the whole
  application follows for anything it creates in a cluster: what PodSteer puts
  into your cluster must not change because an upstream tag moved. A new image
  arrives in a PodSteer release or not at all.

  NOTHING HERE CONTACTS A REGISTRY. PodSteer never pulls an image; it names one
  in a pod or an ephemeral container, and your NODES pull it with whatever
  credentials they have. That is why a private registry needs an imagePullSecret
  on the namespace rather than anything typed into PodSteer.
-->
<script lang="ts">
  import {
    preferences,
    DEFAULT_DEBUG_IMAGE,
    DEFAULT_NODE_SHELL_IMAGE,
    DEFAULT_NODE_SHELL_NAMESPACE,
  } from '$stores/preferences.svelte'
  import Button from './Button.svelte'

  /**
   * The fields are edited locally and committed on change, then read back.
   *
   * Read back because the setters NORMALISE: an empty field falls back to the
   * default rather than being stored as nothing. Without the read-back the box
   * would keep showing the blank the operator left while the preference had
   * already reverted, which reads as a setting that did not save.
   */
  let debugImage = $state(preferences.debugImage)
  let nodeShellImage = $state(preferences.nodeShellImage)
  let nodeShellNamespace = $state(preferences.nodeShellNamespace)

  function commitDebugImage() {
    preferences.setDebugImage(debugImage)
    debugImage = preferences.debugImage
  }

  function commitNodeShellImage() {
    preferences.setNodeShellImage(nodeShellImage)
    nodeShellImage = preferences.nodeShellImage
  }

  function commitNodeShellNamespace() {
    preferences.setNodeShellNamespace(nodeShellNamespace)
    nodeShellNamespace = preferences.nodeShellNamespace
  }

  /** True when all three are what PodSteer ships with, so Reset can say so. */
  const atDefaults = $derived(
    preferences.debugImage === DEFAULT_DEBUG_IMAGE &&
      preferences.nodeShellImage === DEFAULT_NODE_SHELL_IMAGE &&
      preferences.nodeShellNamespace === DEFAULT_NODE_SHELL_NAMESPACE,
  )

  function resetAll() {
    preferences.setDebugImage('')
    preferences.setNodeShellImage('')
    preferences.setNodeShellNamespace('')
    debugImage = preferences.debugImage
    nodeShellImage = preferences.nodeShellImage
    nodeShellNamespace = preferences.nodeShellNamespace
  }
</script>

<section>
  <h3 class="text-title-medium text-on-surface">Terminal images</h3>
  <p class="mt-0.5 text-body-small leading-relaxed text-on-surface-variant">
    Two of PodSteer's terminals run a container <em class="text-on-surface not-italic"
      >in your cluster</em
    >, so they need an image to run. PodSteer never pulls one itself — it names the image and your
    nodes pull it, with whatever registry credentials they already have. If your clusters cannot
    reach Docker Hub, mirror these into your own registry and name them here.
  </p>

  <h4 class="mt-6 text-label-large uppercase tracking-wider text-on-surface-variant">
    Debug container
  </h4>
  <label class="mt-2 block">
    <span class="sr-only">Debug container image</span>
    <input
      type="text"
      bind:value={debugImage}
      onchange={commitDebugImage}
      onblur={commitDebugImage}
      placeholder={DEFAULT_DEBUG_IMAGE}
      spellcheck="false"
      autocapitalize="off"
      autocorrect="off"
      class="field w-full px-3 py-2 font-mono text-body-small"
    />
  </label>
  <p class="mt-1.5 text-body-small leading-relaxed text-on-surface-variant">
    Added to a pod you are looking at, as an ephemeral container sharing its namespaces — the
    equivalent of <code class="text-on-surface">kubectl debug</code>. It lands in
    <em class="text-on-surface not-italic">someone else's namespace</em>, so Pod Security admission
    judges it: under the <code class="text-on-surface">restricted</code> level a root container is
    refused outright, which is why the default is a nonroot image.
  </p>

  <h4 class="mt-6 text-label-large uppercase tracking-wider text-on-surface-variant">Node shell</h4>
  <label class="mt-2 block">
    <span class="sr-only">Node shell image</span>
    <input
      type="text"
      bind:value={nodeShellImage}
      onchange={commitNodeShellImage}
      onblur={commitNodeShellImage}
      placeholder={DEFAULT_NODE_SHELL_IMAGE}
      spellcheck="false"
      autocapitalize="off"
      autocorrect="off"
      class="field w-full px-3 py-2 font-mono text-body-small"
    />
  </label>
  <p class="mt-1.5 text-body-small leading-relaxed text-on-surface-variant">
    A privileged pod pinned to one node, entering its host namespaces with
    <code class="text-on-surface">nsenter</code>. That is root by definition, so this one is
    <em class="text-on-surface not-italic">not</em> the nonroot variant — a nonroot image could not
    do the only thing this pod exists for. The pod is deleted when its terminal closes.
  </p>

  <label class="mt-4 block">
    <span class="text-body-small text-on-surface-variant">Namespace for the node-shell pod</span>
    <input
      type="text"
      bind:value={nodeShellNamespace}
      onchange={commitNodeShellNamespace}
      onblur={commitNodeShellNamespace}
      placeholder={DEFAULT_NODE_SHELL_NAMESPACE}
      spellcheck="false"
      autocapitalize="off"
      autocorrect="off"
      class="field mt-1 w-full px-3 py-2 font-mono text-body-small"
    />
  </label>
  <p class="mt-1.5 text-body-small leading-relaxed text-on-surface-variant">
    Where that pod is created. <code class="text-on-surface">kube-system</code> matches
    <code class="text-on-surface">kubectl node-shell</code> and is already permissive enough to
    admit a privileged pod; a namespace enforcing
    <code class="text-on-surface">restricted</code> will refuse one.
  </p>

  <div class="mt-5 flex items-center gap-3">
    <Button variant="outlined" disabled={atDefaults} onclick={resetAll}>Restore defaults</Button>
    <span class="text-body-small text-on-surface-variant/70">
      {atDefaults ? 'These are the images PodSteer ships with.' : 'Clearing a field also restores its default.'}
    </span>
  </div>

  <p
    class="mt-5 rounded-sm border border-outline-variant/50 bg-surface-container px-3 py-2
           text-body-small leading-relaxed text-on-surface-variant"
  >
    Both defaults are pinned to an exact tag rather than a moving one: what PodSteer creates in your
    cluster must not change because an upstream tag was republished. A new image arrives in a
    PodSteer release.
  </p>
</section>
