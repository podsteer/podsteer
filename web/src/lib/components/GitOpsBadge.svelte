<!--
  Says which GitOps controller owns an object.

  A neutral branch glyph rather than the Argo CD or Flux logo, and that is a
  deliberate choice rather than an omission. Both are CNCF projects whose
  marks are trademarks with their own usage terms; shipping them inside an
  Apache-2.0 binary means redistributing someone else's brand, which is a
  licensing question this project does not need to open to say the word
  "Flux". The name is written out beside the icon, which identifies the
  controller more precisely than a logo does anyway.

  If the marks are wanted later, the way to do it is to obtain them under
  their own terms and record them in the licence inventory like any other
  third-party asset — not to trace something logo-shaped.
-->
<script lang="ts">
  import { GitBranch } from '@lucide/svelte'
  import type { GitOpsOwner } from '$lib/gitops'
  import { revertWarning } from '$lib/gitops'

  interface Props {
    owner: GitOpsOwner
    /** Compact drops the owning object's name, for a tight header. */
    compact?: boolean
  }

  let { owner, compact = false }: Props = $props()
</script>

<!-- The whole sentence on hover. The chip has room for who, not for what
     happens if you ignore it. -->
<span
  class="inline-flex min-w-0 shrink-0 items-center gap-1.5 rounded-full bg-surface-container-high
         px-2 py-0.5 text-body-small text-on-surface-variant"
  title={revertWarning(owner)}
>
  <GitBranch class="size-3.5 shrink-0 text-on-surface-variant/70" strokeWidth={1.8} />
  <span class="shrink-0">{owner.label}</span>
  {#if !compact && owner.source}
    <span class="text-on-surface-variant/40" aria-hidden="true">/</span>
    <span class="min-w-0 truncate">{owner.source}</span>
  {/if}
</span>
