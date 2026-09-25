import { beforeEach, describe, expect, it } from 'vitest'

import { loadDistributionTable, markFor, rememberedMark, type ClusterMark } from './clusterDistribution'
import type { Cluster, Distribution } from '$lib/api/client'

const TABLE: Distribution[] = [
  { id: 'hosted-one', label: 'Hosted One', hosted: true },
  { id: 'self-hosted-one', label: 'Self Hosted One', hosted: false },
]

const cluster = (fields: Partial<Cluster>) =>
  ({
    id: 'dev',
    distribution: '',
    distributionId: '',
    distributionHosted: false,
    distributionEvidence: '',
    ...fields,
  }) as unknown as Cluster

const nothingRemembered = (): ClusterMark | null => null

describe('what a cluster is, on the home list', () => {
  beforeEach(async () => {
    await loadDistributionTable(async () => TABLE)
  })

  it('shows what the backend identified now', () => {
    const mark = markFor(
      cluster({ distribution: 'Hosted One', distributionHosted: true, distributionEvidence: 'its version string says so' }),
      nothingRemembered,
    )

    expect(mark?.label).toBe('Hosted One')
    expect(mark?.hosted).toBe(true)
    expect(mark?.evidence).toBe('its version string says so')
  })

  it('falls back to what was learned last time', () => {
    // The strongest evidence exists only while a cluster is open, so a list
    // drawn before anything is connected would otherwise lose it.
    const mark = markFor(cluster({ id: 'dev' }), (id) =>
      id === 'dev' ? rememberedMark('self-hosted-one') : null,
    )

    expect(mark?.label).toBe('Self Hosted One')
    expect(mark?.evidence).toContain('last opened')
  })

  it('PREFERS what is true now over what was remembered', () => {
    // A cluster rebuilt on something else must not keep its old mark. The
    // backend's answer is about this cluster today; the remembered one is
    // about whatever answered last time.
    const mark = markFor(
      cluster({ distribution: 'Hosted One', distributionHosted: true, distributionEvidence: 'fresh' }),
      () => rememberedMark('self-hosted-one'),
    )

    expect(mark?.label).toBe('Hosted One')
  })

  it('shows NOTHING when nothing identified it', () => {
    // A hedge in this space — "Unknown", "Other" — is a label somebody reads
    // as information. A kubeadm cluster behind a private address gives away
    // nothing until it is opened, and that is a truthful blank.
    expect(markFor(cluster({}), nothingRemembered)).toBeNull()
  })

  it('forgets a mark whose row no longer exists', () => {
    // A table row removed in an upgrade would otherwise leave a label nothing
    // can explain on a cluster nobody can re-identify.
    expect(rememberedMark('a-row-that-was-removed')).toBeNull()
  })

  it('shows nothing before the table has loaded', async () => {
    await loadDistributionTable(async () => {
      throw new Error('the table did not load')
    })

    expect(rememberedMark('hosted-one')).toBeNull()
  })
})
