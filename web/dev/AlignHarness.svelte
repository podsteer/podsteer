<script lang="ts">
  import { Box } from '@lucide/svelte'

  import RowSelect from '../src/lib/components/RowSelect.svelte'
  import RowMenuCell from '../src/lib/components/RowMenuCell.svelte'
  import StatusIndicator from '../src/lib/components/StatusIndicator.svelte'
  import Checkbox from '../src/lib/components/Checkbox.svelte'
  import Radio from '../src/lib/components/Radio.svelte'
</script>

<table data-list-table class="w-full table-fixed">
  <tbody>
    <tr class="border-t border-outline-variant/25">
      <RowSelect selected={false} label="web-1" ontoggle={() => {}} />
      <td class="overflow-hidden py-1.5 pr-3 pl-5">
        <StatusIndicator tone="ok" label="Running" icon={Box} />
      </td>
      <td class="overflow-hidden py-1.5 pr-3" data-probe="name">
        <span class="text-body-medium">argocd-application-controller-0</span>
      </td>
      <RowMenuCell actions={[{ label: 'Copy', kind: 'copy', onclick: () => {} }]} label="web-1" />
    </tr>

    <!-- The GENERIC row, which draws its own icon rather than using
         StatusIndicator — every kind without a purpose-built view. It was the
         one left behind when the alignment rule went in. -->
    <tr class="border-t border-outline-variant/25" data-probe="generic">
      <RowSelect selected={false} label="hpa-1" ontoggle={() => {}} />
      <td class="py-1.5 pr-3 pl-5">
        <span class="flex" title="HorizontalPodAutoscaler">
          <Box class="size-4 shrink-0 text-on-surface-variant/60" strokeWidth={1.75} />
        </span>
      </td>
      <td class="truncate py-1.5 pr-3 pl-3 text-on-surface" data-probe="generic-name">
        <span class="text-body-medium">api-autoscaler</span>
      </td>
      <RowMenuCell actions={[{ label: 'Copy', kind: 'copy', onclick: () => {} }]} label="hpa-1" />
    </tr>
  </tbody>
</table>

<div class="mt-8 flex items-center gap-6 p-4" data-probe="controls">
  <Checkbox checked ariaLabel="a" />
  <Radio name="g" value="x" checked ariaLabel="b" />
</div>
