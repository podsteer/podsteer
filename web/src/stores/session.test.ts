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

describe('recent objects', () => {
  // No object names are ever written to disk (see SECURITY.md), so this is
  // tracked in memory on the session rather than in preferences — these
  // tests are about that in-memory list, not about persistence.
  let open: ClusterSession

  beforeEach(() => {
    open = session()
    open.selectedKindId = RICH_KIND_IDS.pods
  })

  it('is bounded at twelve, dropping the oldest first', async () => {
    for (let index = 0; index < 15; index++) {
      await open.openDetail(`pod-${index}`, 'web')
    }

    expect(open.recentObjects).toHaveLength(12)
    // Most recent first: the last one opened leads, and the three oldest
    // (pod-0, pod-1, pod-2) fell off the end.
    expect(open.recentObjects[0]).toEqual({
      kindId: RICH_KIND_IDS.pods,
      name: 'pod-14',
      namespace: 'web',
    })
    expect(open.recentObjects.map((entry) => entry.name)).not.toContain('pod-2')
  })

  it('moves a reopened entry to the top instead of duplicating it', async () => {
    await open.openDetail('api', 'web')
    await open.openDetail('worker', 'web')
    await open.openDetail('cache', 'web')
    // Reopening the first one must surface it again, not leave a second copy
    // behind at the bottom of the list.
    await open.openDetail('api', 'web')

    expect(open.recentObjects.map((entry) => entry.name)).toEqual(['api', 'cache', 'worker'])
  })

  it('tells apart two objects that share a name in different namespaces', async () => {
    await open.openDetail('api', 'staging')
    await open.openDetail('api', 'production')

    expect(open.recentObjects).toHaveLength(2)
    expect(open.recentObjects.map((entry) => entry.namespace)).toEqual(['production', 'staging'])
  })

  it('clears on Clear, and on disconnect', async () => {
    await open.openDetail('api', 'web')
    expect(open.recentObjects).toHaveLength(1)

    open.clearRecents()
    expect(open.recentObjects).toEqual([])

    await open.openDetail('api', 'web')
    expect(open.recentObjects).toHaveLength(1)

    // dispose() is what a tab close (a disconnect) runs — see
    // workspace.svelte.ts's close(). Nothing about a past connection should
    // outlive it.
    open.dispose()
    expect(open.recentObjects).toEqual([])
  })
})
