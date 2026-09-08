<!--
  A Service's ports, with a forward for each one PodSteer can forward.

  THE SAME ROW SHAPE AS A CONTAINER'S PORTS, deliberately: the name on the
  left, the number and what it targets in the value column, the control at the
  end. An operator forwarding a database does the same thing on either panel
  and should not have to learn it twice.

  WHAT THE CONTROL PROMISES IS DECIDED IN $lib/servicePorts, not here. Three
  kinds of Service port cannot be forwarded at all — an ExternalName's, one on
  a Service that selects no pods, and anything that is not TCP — and each of
  those gets the sentence saying which it is rather than a button the backend
  would refuse. Hiding the port entirely would be worse: somebody looking for
  a DNS Service's 53 and not finding it concludes the panel is broken.

  The running forward is found through the store's own Service association
  rather than by pod name, because the pod under a Service forward CHANGES:
  that is the feature. See stores/forwards.svelte.ts.
-->
<script lang="ts">
  import DetailSection from './DetailSection.svelte'
  import ForwardAddress from './ForwardAddress.svelte'
  import PortForwardStart from './PortForwardStart.svelte'
  import { servicePortRows, servicePortSelector } from '$lib/servicePorts'
  import { forwards } from '$stores/forwards.svelte'
  import { Loader, Unplug } from '@lucide/svelte'

  interface Props {
    /** The Service's parsed manifest. */
    manifest: unknown
    clusterId: string
    namespace: string
    /** The Service's name, which is what a forward is asked for by. */
    name: string
  }

  let { manifest, clusterId, namespace, name }: Props = $props()

  const rows = $derived(servicePortRows(manifest))
</script>

{#if rows.length > 0}
  <DetailSection
    level="h3"
    id="service-ports"
    title="Ports"
    hint={String(rows.length)}
    help="service-ports"
  >
    <div class="flex flex-col gap-1.5">
      {#each rows as row (row.key)}
        {@const open = forwards.forService(clusterId, namespace, name, row.port)}
        {@const busy = forwards.isServiceBusy(clusterId, namespace, name, row.port)}
        <div class="detail-grid items-center text-body-medium">
          <span class="min-w-0 truncate text-on-surface">
            {row.name || 'Port'}
          </span>

          <!-- flex-wrap for the same reason as the container panel's ports:
               PortForwardStart's validation message is a sibling in this row
               rather than a row of its own. -->
          <span class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <span class="shrink-0 tabular-nums text-on-surface-variant">
              {row.port}/{row.protocol} → {row.target}
              {#if row.nodePort > 0}
                · node {row.nodePort}
              {/if}
            </span>

            {#if open?.reconnecting}
              <!-- The pod behind the Service went away and another is being
                   found. Said out loud rather than drawn as connected: the
                   local port is still bound and still correct, so whatever is
                   pointed at it is stalling, not broken. -->
              <span class="flex min-w-0 items-center gap-1.5 text-gauge-warn">
                <Loader class="size-3.5 shrink-0 animate-spin" strokeWidth={2} />
                <span class="truncate">holding {open.address} — finding a replacement pod</span>
              </span>
            {:else if open}
              <ForwardAddress forward={open} />
            {/if}

            {#if open}
              <button
                type="button"
                disabled={busy}
                onclick={() => void forwards.stop(open)}
                class="state-layer ml-auto inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm
                       border border-outline-variant px-2 text-label-large
                       text-on-surface-variant transition-colors duration-100
                       hover:bg-surface-container hover:text-on-surface disabled:opacity-50"
              >
                {#if busy}
                  <Loader class="size-3.5 animate-spin" strokeWidth={2} />
                {:else}
                  <Unplug class="size-3.5" strokeWidth={1.8} />
                {/if}
                Stop
              </button>
            {:else if row.forwardable}
              <PortForwardStart
                remotePort={row.port}
                portName={row.name}
                {busy}
                onstart={(localPort) =>
                  void forwards.startService(
                    clusterId,
                    namespace,
                    name,
                    servicePortSelector(row),
                    row.port,
                    localPort,
                  )}
              />
            {:else}
              <span class="ml-auto min-w-0 text-body-small text-on-surface-variant/60">
                {row.reason}
              </span>
            {/if}
          </span>
        </div>
      {/each}
    </div>
  </DetailSection>
{/if}
