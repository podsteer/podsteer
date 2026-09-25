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
  import { managementWarning, rollbackWarning, type GitOpsManagement } from '$lib/gitops'

  interface Props {
    management?: GitOpsManagement | null
    /**
     * The act the warning is about, when it has a sentence of its own. A
     * rollback does: "changes are reverted" is true and leaves somebody to
     * work out that the fix is a revert in Git, not a second rollback.
     */
    action?: 'rollback'
    /** Spacing from what is above. `mt-3` suits its usual place, under a
        dialog's title; a pane that spaces its own children passes ''. */
    class?: string
  }

  let { management = null, action, class: className = 'mt-3' }: Props = $props()
</script>

{#if management}
  <!-- mt-3: it follows the dialog's title, and flush against it the warning
       read as a subtitle. A hairline under it, as in the drawer's edit footer: the sentence is
       about everything below it, and the rule keeps it from reading as a
       caption to whatever happens to come next. -->
  <p
    class="{className} flex min-w-0 items-start gap-2 border-b border-outline-variant/60 pb-3
           text-body-small text-gauge-warn-ink"
    role="status"
  >
    <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={2} />
    <span class="min-w-0">{action === 'rollback' ? rollbackWarning(management) : managementWarning(management)}</span>
  </p>
{/if}
