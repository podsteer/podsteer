<!--
  Rules as API group, resource and verbs — shared by the Permissions page
  (what this kubeconfig may do) and the role dialog (what a role grants), so
  the two read identically. A wildcard verb is marked, because `*` is the one
  entry in a verb list that means more than it says.
-->
<script lang="ts">
  import type { verbRows } from '$lib/rbac'

  interface Props {
    rows: ReturnType<typeof verbRows>
  }

  let { rows }: Props = $props()
</script>

<div class="overflow-x-auto">
  <table class="w-full min-w-[36rem] border-collapse text-body-medium">
    <thead>
      <tr class="border-b border-outline-variant text-left text-label-large text-on-surface-variant">
        <th class="py-2 pr-4 font-medium">API group</th>
        <th class="py-2 pr-4 font-medium">Resource</th>
        <th class="py-2 font-medium">Verbs</th>
      </tr>
    </thead>
    <tbody>
      {#each rows as row (`${row.group}/${row.resource}/${row.resourceNames.join(',')}`)}
        <tr class="border-b border-outline-variant/40 align-middle">
          <td class="py-2 pr-4 font-mono text-body-small text-on-surface-variant">{row.group || '(core)'}</td>
          <td class="py-2 pr-4 font-mono text-body-small text-on-surface" data-selectable>
            {row.resource}
            {#if row.resourceNames.length > 0}
              <span class="text-on-surface-variant/70">— only {row.resourceNames.join(', ')}</span>
            {/if}
          </td>
          <td class="py-2">
            <div class="flex flex-wrap gap-1">
              {#each row.verbs as entry (entry)}
                <span
                  class="rounded-full px-2 py-0.5 font-mono text-label-small
                         {entry === '*'
                    ? 'bg-warning-container text-on-warning-container'
                    : 'bg-surface-container-highest text-on-surface-variant'}"
                >
                  {entry}
                </span>
              {/each}
            </div>
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
