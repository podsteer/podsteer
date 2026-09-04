<!--
  Renders the debug, node-shell and local-terminal dialogs and, once one is
  confirmed, the terminal for it — mounted ONCE at the workspace level so all
  three can be launched from anywhere (the node drawer, the node row menu, the
  pod terminal toolbar, the workspace header) and outlive the surface that
  launched them.

  See $stores/sessionLauncher: `pending` is the dialog phase, `running` is the
  terminal phase. The terminal is the same component the pod terminal uses,
  in its 'debug' / 'nodeshell' / 'local' variant.
-->
<script lang="ts">
  import { sessionLauncher } from '$stores/sessionLauncher.svelte'
  import { nodeShells } from '$stores/nodeShells.svelte'
  import DebugDialog from './DebugDialog.svelte'
  import NodeShellDialog from './NodeShellDialog.svelte'
  import LocalShellDialog from './LocalShellDialog.svelte'
  import PaneDialog from './PaneDialog.svelte'
  import Terminal from './Terminal.svelte'
  import { Bug, SquareTerminal, Laptop } from '@lucide/svelte'

  const pending = $derived(sessionLauncher.pending)
  const running = $derived(sessionLauncher.running)
</script>

{#if pending?.kind === 'debug'}
  <DebugDialog
    open
    clusterId={pending.clusterId}
    namespace={pending.namespace}
    pod={pending.pod}
    containers={pending.containers}
    productionGroup={pending.productionGroup}
    onclose={() => sessionLauncher.cancel()}
    onconfirm={(target, image, command) => sessionLauncher.startDebug(target, image, command)}
  />
{:else if pending?.kind === 'nodeshell'}
  <NodeShellDialog
    open
    clusterId={pending.clusterId}
    node={pending.node}
    productionGroup={pending.productionGroup}
    onclose={() => sessionLauncher.cancel()}
    onconfirm={(image, namespace) => sessionLauncher.startNodeShell(image, namespace)}
  />
{:else if pending?.kind === 'local'}
  <LocalShellDialog
    open
    clusterId={pending.clusterId}
    agents={pending.agents}
    onclose={() => sessionLauncher.cancel()}
    onconfirm={(agentId, readOnly) => sessionLauncher.startLocal(agentId, readOnly)}
  />
{/if}

{#if running?.kind === 'debug'}
  <PaneDialog
    open
    icon={Bug}
    kind="Debug"
    name={running.pod}
    label="Debug container"
    onclose={() => sessionLauncher.close()}
  >
    <Terminal
      variant="debug"
      clusterId={running.clusterId}
      namespace={running.namespace}
      podName={running.pod}
      containerName=""
      debugTarget={running.target}
      debugImage={running.image}
      debugCommand={running.command}
    />
  </PaneDialog>
{:else if running?.kind === 'nodeshell'}
  <PaneDialog
    open
    icon={SquareTerminal}
    kind="Node shell"
    name={running.node}
    label="Node shell"
    onclose={() => sessionLauncher.close()}
  >
    <Terminal
      variant="nodeshell"
      clusterId={running.clusterId}
      namespace={running.namespace}
      podName=""
      containerName=""
      nodeName={running.node}
      nodeShellNamespace={running.namespace}
      nodeShellImage={running.image}
      onstarted={() => void nodeShells.refresh()}
    />
  </PaneDialog>
{:else if running?.kind === 'local'}
  <PaneDialog
    open
    icon={Laptop}
    kind="Local"
    name={running.clusterId || 'this machine'}
    label={running.title}
    onclose={() => sessionLauncher.close()}
  >
    <Terminal
      variant="local"
      clusterId={running.clusterId}
      namespace=""
      podName=""
      containerName=""
      agent={running.agent}
      agentReadOnly={running.readOnly}
      subject={running.subject}
    />
  </PaneDialog>
{/if}
