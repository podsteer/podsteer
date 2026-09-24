import { describe, expect, it } from 'vitest'

import { gitOpsOwner, revertWarning } from './gitops'

const secret = (metadata: Record<string, unknown>) => ({ apiVersion: 'v1', kind: 'Secret', metadata })

describe('a Secret written by External Secrets', () => {
  it('names the ExternalSecret that owns it', () => {
    const owner = gitOpsOwner(
      secret({
        name: 'db-credentials',
        ownerReferences: [
          { apiVersion: 'external-secrets.io/v1', kind: 'ExternalSecret', name: 'db-credentials-es', controller: true },
        ],
      }),
    )

    expect(owner).toMatchObject({
      tool: 'external-secrets',
      source: 'db-credentials-es',
      sourceKind: 'ExternalSecret',
    })
  })

  it('is recognised from the data hash alone, under a Merge policy with no owner', () => {
    const owner = gitOpsOwner(
      secret({ name: 'db', annotations: { 'reconcile.external-secrets.io/data-hash': 'abc123' } }),
    )

    expect(owner?.tool).toBe('external-secrets')
    expect(owner?.source).toBe('')
  })

  it('wins over an Argo CD tracking id copied onto the Secret', () => {
    // Argo CD tracks the ExternalSecret; what overwrites the Secret is ESO.
    const owner = gitOpsOwner(
      secret({
        name: 'db',
        annotations: { 'argocd.argoproj.io/tracking-id': 'app:external-secrets.io/ExternalSecret:ns/db' },
        ownerReferences: [{ apiVersion: 'external-secrets.io/v1beta1', kind: 'ExternalSecret', name: 'db' }],
      }),
    )

    expect(owner?.tool).toBe('external-secrets')
  })

  it('does not claim a Secret some other ExternalSecret-named kind owns', () => {
    // Matched on the group as well as the kind, the rule the operator panels
    // follow: a kind name alone is not an identity.
    const owner = gitOpsOwner(
      secret({ name: 'db', ownerReferences: [{ apiVersion: 'example.com/v1', kind: 'ExternalSecret', name: 'db' }] }),
    )

    expect(owner).toBeNull()
  })

  it('says the external store overwrites it, not Git', () => {
    const owner = gitOpsOwner(
      secret({
        name: 'db',
        ownerReferences: [{ apiVersion: 'external-secrets.io/v1', kind: 'ExternalSecret', name: 'db-es' }],
      }),
    )!

    const sentence = revertWarning(owner)
    expect(sentence).toContain('the db-es ExternalSecret')
    expect(sentence).toContain('external secret store')
    expect(sentence).not.toContain('Git')
  })
})
