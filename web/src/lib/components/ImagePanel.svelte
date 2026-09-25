<!--
  What a container's image is, without pulling it.

  WHAT IS HERE IS EVERYTHING KUBERNETES REPORTS: the reference the kubelet
  says it is running, the digest it recorded, the size the node that pulled it
  has on disk, every other name that node knows the same image by, and whether
  the pull needed credentials.

  WHAT IS NOT HERE — the layers, their creation commands, the entrypoint, the
  exposed ports and the labels — lives in the image's own manifest and config
  blob, in a registry. Reading those would mean opening a connection to a host
  that is not an API server, and for a private image an authenticated one
  whose credential is a pull Secret sitting in the cluster. PodSteer contacts
  the clusters your kubeconfig names and nothing else, so the panel says what
  it did not look at rather than leaving a gap: empty space where layers would
  be is a claim nothing checked, which is the same distinction the dependency
  map draws between Bounded and Unreadable.

  ONE REQUEST, WHEN ASKED. Two GETs behind it — the pod, and its node — and
  never on the refresh tick, for the reason BrowseAPI.ObjectGraph is never on
  one either.
-->
<script lang="ts">
  import { imageReport, type ImageReport } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { hasIdentity, identityRows, imageHeadline, sizeRow } from '$lib/imageReport'
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import Button from './Button.svelte'
  import Select from './Select.svelte'
  import { Layers, RotateCw } from '@lucide/svelte'

  interface Props {
    clusterId: string
    namespace: string
    /** The pod holding the containers. */
    podName: string
    /**
     * Every container of the pod, init containers included.
     *
     * ONE SECTION FOR ALL OF THEM rather than one per container: an image is
     * looked up rarely and deliberately, and a pane that grew a collapsed
     * section per container would push everything below it off the screen on
     * a pod with a sidecar and two init containers.
     */
    containers: string[]
  }

  let { clusterId, namespace, podName, containers }: Props = $props()

  let chosen = $state('')
  const containerName = $derived(
    containers.includes(chosen) ? chosen : (containers[0] ?? ''),
  )

  /**
   * The answer, component-local and nowhere else. It dies with the drawer:
   * an image report is about one container at one instant, and there is
   * nothing here worth persisting.
   */
  let report = $state<ImageReport | null>(null)
  let loading = $state(false)
  let error = $state('')

  // A different container is a different image, so whatever is on screen for
  // the last one has to go rather than sit under a new name.
  $effect(() => {
    void containerName
    report = null
    error = ''
  })

  async function describe(): Promise<void> {
    loading = true
    error = ''
    try {
      report = await imageReport(clusterId, namespace, podName, containerName)
    } catch (cause) {
      error = toApiError(cause).message
      report = null
    } finally {
      loading = false
    }
  }

  function rows(current: ImageReport): DetailRow[] {
    // The size row carries its own reason when nothing was measured, which is
    // why it is composed rather than formatted inline: a dash with no
    // explanation beside it is the failure this whole feature is careful
    // about.
    const size = sizeRow(current)
    return [
      ...identityRows(current),
      { label: size.label, value: size.value },
    ]
  }
</script>

<DetailSection level="h3" id="image" title="Image" defaultOpen={false}>
  <div class="flex flex-col gap-4">
    {#if containers.length > 1}
      <Select
        label="Container"
        value={containerName}
        onchange={(name) => (chosen = name)}
        options={containers.map((name) => ({ value: name, label: name }))}
      />
    {/if}

    {#if !report}
      <p class="text-body-small leading-relaxed text-on-surface-variant/70">
        What Kubernetes reports about this image: the reference and digest the
        kubelet recorded, and the size and names the node that pulled it holds. No
        registry is contacted.
      </p>
      <Button variant="outlined" {loading} onclick={describe} class="self-start">
        <Layers class="size-3.5" strokeWidth={2} />
        Describe image
      </Button>
      {#if error}
        <p class="text-body-small text-error" role="alert">{error}</p>
      {/if}
    {:else}
      <div class="flex items-start justify-between gap-3">
        {#if imageHeadline(report)}
          <p class="min-w-0 text-body-medium leading-relaxed text-on-surface" data-selectable>
            {imageHeadline(report)}
          </p>
        {:else}
          <span></span>
        {/if}
        <Button variant="text" {loading} onclick={describe} class="shrink-0" label="Re-read image">
          <RotateCw class="size-3.5" strokeWidth={2} />
          Re-read
        </Button>
      </div>

      {#if error}
        <p class="text-body-small text-error" role="alert">{error}</p>
      {/if}

      {#if hasIdentity(report)}
        <DetailList rows={rows(report)} />
      {/if}

      <!-- A node and a container status recording different digests is what a
           multi-platform image looks like, not a fault. Stated rather than
           hidden, because somebody comparing the two by hand would otherwise
           conclude one of them is wrong. -->
      {#if report.digestNote}
        <p class="text-body-small leading-relaxed text-on-surface-variant" data-selectable>
          {report.digestNote}
        </p>
      {/if}

      {#if (report.otherNames?.length ?? 0) > 0}
        <div class="flex flex-col gap-1">
          <p class="text-body-small font-medium text-on-surface">
            Also known on this node as
          </p>
          <!-- Frequently the most useful thing here: a moved tag shows up as
               one image carrying two names. -->
          {#each report.otherNames ?? [] as name (name)}
            <p class="truncate text-body-small text-on-surface-variant" title={name} data-selectable>
              {name}
            </p>
          {/each}
        </div>
      {/if}

      {#if report.credentialed}
        <div class="flex flex-col gap-1">
          <p class="text-body-small font-medium text-on-surface">
            Pulled with credentials
          </p>
          <p class="text-body-small text-on-surface-variant">
            {(report.pullSecrets ?? []).join(', ')}
          </p>
          <p class="text-body-small leading-relaxed text-on-surface-variant/80">
            {report.credentialNote}
          </p>
        </div>
      {/if}

      <!-- Always shown, never conditional. This is the line that keeps the
           panel honest about the half it did not look at. -->
      <p class="text-body-small leading-relaxed text-on-surface-variant/70" data-selectable>
        {report.bounded}
      </p>
    {/if}
  </div>
</DetailSection>
