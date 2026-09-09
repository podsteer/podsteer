<!--
  What happens to this change once a GitOps controller notices.

  SAID WHERE THE BUTTON IS. The drawer has carried this sentence since the
  YAML editor shipped, but only there — so scaling a Deployment, setting its
  image, rolling it back or resizing one of its pods all wrote to an object
  held in Git with nothing on screen to say so. The controller does not care
  which control made the change.

  Nothing renders when nothing is managed, which is the common case.
-->
<script lang="ts">
  import { TriangleAlert } from '@lucide/svelte'
  import { managementWarning, type GitOpsManagement } from '$lib/gitops'

  interface Props {
    management?: GitOpsManagement | null
  }

  let { management = null }: Props = $props()
</script>

{#if management}
  <p class="flex min-w-0 items-start gap-2 text-body-small text-gauge-warn" role="status">
    <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={2} />
    <span class="min-w-0">{managementWarning(management)}</span>
  </p>
{/if}
