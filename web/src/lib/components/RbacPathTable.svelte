<!--
  Non-resource URL paths and their verbs — the half of a rules review that
  belongs to the API server rather than to a namespace. Shared by the
  Permissions page and the role dialog.
-->
<script lang="ts">
  import type { pathRows } from '$lib/rbac'

  interface Props {
    rows: ReturnType<typeof pathRows>
  }

  let { rows }: Props = $props()
</script>

<div class="overflow-x-auto">
  <table class="w-full min-w-[24rem] border-collapse text-body-medium">
    <thead>
      <tr class="border-b border-outline-variant text-left text-label-large text-on-surface-variant">
        <th class="py-2 pr-4 font-medium">Path</th>
        <th class="py-2 font-medium">Verbs</th>
      </tr>
    </thead>
    <tbody>
      {#each rows as row (row.path)}
        <tr class="border-b border-outline-variant/40">
          <td class="py-2 pr-4 font-mono text-body-small text-on-surface" data-selectable>{row.path}</td>
          <td class="py-2 font-mono text-body-small text-on-surface-variant">{row.verbs.join(', ')}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
