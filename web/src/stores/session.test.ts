import { beforeEach, describe, expect, it, vi } from 'vitest'

// The bindings do not exist outside the Wails runtime, and this suite is
// about what the session does with a list it already holds — so the manifest
// read is stubbed to fail. openDetail must still resolve the row object: a
// panel whose manifest is slow or refused still shows its live sections.
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    getManifest: vi.fn().mockRejectedValue(new Error('no cluster in a test')),
  }
})

import { ClusterSession, RICH_KIND_IDS } from './session.svelte'
import type { Cluster, Node, Pod } from '$lib/api/client'

// Only the three fields the constructor reads. Cast through unknown because
// the DTO is a generated class with a dozen more, none of which this touches.
const cluster = { id: 'dev', name: 'dev', defaultNamespace: 'default' } as unknown as Cluster

function session(): ClusterSession {
  return new ClusterSession(cluster)
}

describe('a revealed Secret', () => {
  it('can be put back, without closing the panel', async () => {
    // THE GAP THIS FILLS. Revealing swapped the control for nothing, so the
    // only way to re-mask a Secret was to close the panel and open it again
    // — which is not a policy, it is a missing button, and the reveal on an
    // environment variable does not share it.
    const open = session()
    open.selectedKindId = 'core/v1/secrets'
    open.selectedName = 'db-secrets'

    await open.revealManifestSecrets()
    expect(open.secretsRevealed).toBe(true)

    await open.hideManifestSecrets()
    expect(open.secretsRevealed).toBe(false)
  })

  it('starts hidden on every object', async () => {
    // A reveal is a decision about ONE object. Carrying it to the next is how
    // a client ends up showing a value somebody unmasked in private on the
    // object they open in a meeting.
    const open = session()
    open.selectedKindId = 'core/v1/secrets'

    await open.openDetail('db-secrets', 'web')
    await open.revealManifestSecrets()
    expect(open.secretsRevealed).toBe(true)

    await open.openDetail('other-secrets', 'web')
    expect(open.secretsRevealed).toBe(false)
  })
})

describe('opening an object that was not clicked', () => {
  let open: ClusterSession

  beforeEach(() => {
    open = session()
  })

  it('finds the node behind a followed link', async () => {
    // THE BUG THIS GUARDS. Following the Node link from a pod's panel opened
    // a node panel with no usage charts, because the charts are read from the
    // row object and only a clicked row hands one in. Closing the panel and
    // clicking the node in the list fixed it, which is how it was noticed.
    open.selectedKindId = RICH_KIND_IDS.nodes
    open.nodes = [{ name: 'node-1', hasMetrics: true } as Node]

    await open.openDetail('node-1', '')

    expect(open.selectedNode?.name).toBe('node-1')
    expect(open.selectedNode?.hasMetrics).toBe(true)
  })

  it('finds the pod behind a followed link, in its own namespace', async () => {
    open.selectedKindId = RICH_KIND_IDS.pods
    open.pods = [
      { name: 'api', namespace: 'web' } as Pod,
      { name: 'api', namespace: 'staging' } as Pod,
    ]

    await open.openDetail('api', 'staging')

    // Name alone is not an identity: two namespaces routinely hold pods with
    // the same name, and the wrong one would show the wrong pod's findings.
    expect(open.selectedPod?.namespace).toBe('staging')
  })

  it('prefers the object it was handed over a lookup', async () => {
    // A clicked row's object is authoritative and newer than the list it came
    // from; re-finding it would be work for a worse answer.
    const clicked = { name: 'node-1', hasMetrics: false } as Node
    open.selectedKindId = RICH_KIND_IDS.nodes
    open.nodes = [{ name: 'node-1', hasMetrics: true } as Node]

    await open.openDetail('node-1', '', undefined, undefined, clicked)

    // Asserted on the field the two disagree about rather than on identity:
    // Svelte's state wraps what is stored, so the reference is not the one
    // that went in even when the value is.
    expect(open.selectedNode?.hasMetrics).toBe(false)
  })

  it('opens on the manifest alone when the list holds no such row', async () => {
    // Following a link to something the current list does not contain is not
    // an error — the panel simply shows what the manifest can supply.
    open.selectedKindId = RICH_KIND_IDS.nodes
    open.nodes = []

    await open.openDetail('node-9', '')

    expect(open.selectedNode).toBeNull()
    expect(open.selectedName).toBe('node-9')
  })

  it('does not mistake one kind of row for another', async () => {
    // The lists are cleared per view, but a stale name collision would be
    // worse than a missing row: a Pod named like a Node must not furnish a
    // node panel.
    open.selectedKindId = RICH_KIND_IDS.pods
    open.nodes = [{ name: 'shared', hasMetrics: true } as Node]
    open.pods = [{ name: 'shared', namespace: 'web' } as Pod]

    await open.openDetail('shared', 'web')

    expect(open.selectedNode).toBeNull()
    expect(open.selectedPod?.name).toBe('shared')
  })
})

describe('filtering the pod list', () => {
  let open: ClusterSession

  beforeEach(() => {
    open = session()
    open.pods = [
      { name: 'web-1', namespace: 'prod', nodeName: 'node-a', phase: 'Running', statusReason: '' } as Pod,
      { name: 'web-2', namespace: 'prod', nodeName: 'node-a', phase: 'Pending', statusReason: '' } as Pod,
      {
        name: 'db-1',
        namespace: 'prod',
        nodeName: 'node-b',
        phase: 'Running',
        statusReason: 'CrashLoopBackOff',
      } as Pod,
    ]
  })

  it('combines the search query and a status chip with AND', () => {
    // THE BEHAVIOUR THIS GUARDS. A chip narrows what a search already
    // narrowed, not a separate question — selecting "Pending" while
    // searching "web" must not bring back a pod the search itself excluded.
    open.search = 'web'
    open.togglePodStatusFilter('pending')

    expect(open.visiblePods.map((pod) => pod.name)).toEqual(['web-2'])
  })

  it('ORs several selected chips together', () => {
    open.togglePodStatusFilter('pending')
    open.togglePodStatusFilter('restarting')

    expect(open.visiblePods.map((pod) => pod.name).sort()).toEqual(['db-1', 'web-2'])
  })

  it('toggling a chip off restores what it had removed', () => {
    open.togglePodStatusFilter('pending')
    expect(open.visiblePods).toHaveLength(1)

    open.togglePodStatusFilter('pending')
    expect(open.visiblePods).toHaveLength(3)
  })

  it('counts against the search-filtered list, not the chip-filtered one', () => {
    // THE BUG THIS GUARDS. Counting against `visiblePods` (search AND chips)
    // would make every OTHER chip's count collapse the moment one chip was
    // selected — a "Pending" count of zero while a pod is sitting right
    // there, merely because "Restarting" happened to be the chip picked.
    // `searchedPods` is search-only, so a chip's own count is unaffected by
    // which chips happen to be selected.
    open.togglePodStatusFilter('restarting')

    expect(open.searchedPods).toHaveLength(3)
    expect(open.searchedPods.some((pod) => pod.phase === 'Pending')).toBe(true)
  })
})
