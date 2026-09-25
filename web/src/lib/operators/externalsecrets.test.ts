import { describe, expect, it } from 'vitest'

import { externalSecret } from './externalsecrets'

/** A synced ExternalSecret on v1beta1 — the ordinary case. */
const synced = {
  apiVersion: 'external-secrets.io/v1beta1',
  kind: 'ExternalSecret',
  metadata: { name: 'shop-database', namespace: 'shop' },
  spec: {
    refreshInterval: '1h',
    secretStoreRef: { name: 'vault-backend', kind: 'ClusterSecretStore' },
    target: {
      name: 'shop-database-credentials',
      creationPolicy: 'Owner',
      deletionPolicy: 'Retain',
      template: { type: 'Opaque', data: { DSN: '{{ .username }}:{{ .password }}@db' } },
    },
    data: [
      { secretKey: 'username', remoteRef: { key: 'shop/database', property: 'username' } },
      { secretKey: 'password', remoteRef: { key: 'shop/database', property: 'password' } },
    ],
    dataFrom: [
      { extract: { key: 'shop/shared' } },
      { find: { path: 'shop/legacy', name: { regexp: '^SHOP_.*' } } },
      { find: { tags: { environment: 'production', team: 'shop' } } },
    ],
  },
  status: {
    conditions: [
      {
        type: 'Ready',
        status: 'True',
        reason: 'SecretSynced',
        message: 'secret synced',
        lastTransitionTime: '2026-09-04T11:00:00Z',
      },
    ],
    refreshTime: '2026-09-04T11:00:00Z',
    syncedResourceVersion: '1-8f2c0a1d9e',
    binding: { name: 'shop-database-credentials' },
  },
}

/** The same object on v1, with a store that refused it. */
const refused = {
  apiVersion: 'external-secrets.io/v1',
  kind: 'ExternalSecret',
  metadata: { name: 'shop-api-token', namespace: 'shop' },
  spec: {
    refreshInterval: '0',
    // No kind: the CRD reads that as a namespaced SecretStore.
    secretStoreRef: { name: 'aws-backend' },
    data: [{ secretKey: 'token', remoteRef: { key: 'prod/shop/api' } }],
  },
  status: {
    conditions: [
      {
        type: 'Ready',
        status: 'False',
        reason: 'SecretSyncedError',
        message: 'could not get secret data from provider: AccessDeniedException: User is not authorized to perform secretsmanager:GetSecretValue',
        lastTransitionTime: '2026-09-04T10:12:00Z',
      },
    ],
  },
}

describe('reading an ExternalSecret', () => {
  it('quotes the Ready condition in the operator’s own words', () => {
    expect(externalSecret(synced)?.ready).toEqual({
      status: 'True',
      reason: 'SecretSynced',
      message: 'secret synced',
      since: '2026-09-04T11:00:00Z',
    })
  })

  it('reads the store, the target and the sync status without touching either', () => {
    // The Secret is NAMED so somebody can go and open it deliberately;
    // nothing here resolves it and nothing here contacts the store.
    const external = externalSecret(synced)

    expect(external?.storeKind).toBe('ClusterSecretStore')
    expect(external?.storeName).toBe('vault-backend')
    expect(external?.targetName).toBe('shop-database-credentials')
    expect(external?.creationPolicy).toBe('Owner')
    expect(external?.deletionPolicy).toBe('Retain')
    expect(external?.templated).toBe(true)
    expect(external?.refreshInterval).toBe('1h')
    expect(external?.refreshTime).toBe('2026-09-04T11:00:00Z')
    expect(external?.syncedResourceVersion).toBe('1-8f2c0a1d9e')
    expect(external?.boundSecret).toBe('shop-database-credentials')
  })

  it('reads a v1 object exactly as it reads a v1beta1 one', () => {
    // The panel is selected on the GROUP, so a cluster on either version —
    // or one mid-migration serving both — gets the same fields.
    const external = externalSecret(refused)

    expect(external?.storeName).toBe('aws-backend')
    expect(external?.mappings).toEqual([
      { remoteKey: 'prod/shop/api', property: '', localKey: 'token', origin: 'data', match: '' },
    ])
  })

  it('defaults a secretStoreRef with no kind to SecretStore, as the CRD does', () => {
    // The other value is ClusterSecretStore, which is not namespaced —
    // getting this wrong sends the reader to a namespace with no such object.
    expect(externalSecret(refused)?.storeKind).toBe('SecretStore')
  })

  it('defaults the target Secret’s name to the ExternalSecret’s own', () => {
    const external = externalSecret({
      metadata: { name: 'shop-database' },
      spec: { secretStoreRef: { name: 'vault-backend' } },
    })

    expect(external?.targetName).toBe('shop-database')
  })

  it('maps each remote key to its local key and says where the mapping came from', () => {
    // A dataFrom names no local key, because it copies every key the remote
    // reference yields — inventing one would claim a Secret key that may not
    // be what lands there.
    expect(externalSecret(synced)?.mappings).toEqual([
      { remoteKey: 'shop/database', property: 'username', localKey: 'username', origin: 'data', match: '' },
      { remoteKey: 'shop/database', property: 'password', localKey: 'password', origin: 'data', match: '' },
      { remoteKey: 'shop/shared', property: '', localKey: '', origin: 'dataFrom.extract', match: '' },
      { remoteKey: 'shop/legacy', property: '', localKey: '', origin: 'dataFrom.find', match: '^SHOP_.*' },
      { remoteKey: '', property: '', localKey: '', origin: 'dataFrom.find', match: 'environment=production, team=shop' },
    ])
  })

  it('reports a store that refused the read with the provider’s own message', () => {
    const external = externalSecret(refused)

    expect(external?.ready).toMatchObject({ status: 'False', reason: 'SecretSyncedError' })
    expect(external?.ready?.message).toContain('AccessDeniedException')
    // Nothing was synced, so there is no refresh time and no bound Secret.
    expect(external?.refreshTime).toBe('')
    expect(external?.syncedResourceVersion).toBe('')
    expect(external?.boundSecret).toBe('')
  })

  it('carries a Ready status the operator has never written before as itself', () => {
    const external = externalSecret({
      spec: {},
      status: { conditions: [{ type: 'Ready', status: 'Weird', reason: 'Whatever' }] },
    })

    expect(external?.ready).toMatchObject({ status: 'Weird', reason: 'Whatever' })
  })

  it('says nothing where the operator has said nothing', () => {
    const external = externalSecret({
      metadata: { name: 'shop-api-token' },
      spec: { secretStoreRef: { name: 'vault-backend' }, data: [] },
    })

    expect(external?.ready).toBeNull()
    expect(external?.refreshInterval).toBe('')
    expect(external?.creationPolicy).toBe('')
    expect(external?.deletionPolicy).toBe('')
    expect(external?.templated).toBe(false)
    expect(external?.mappings).toEqual([])
    expect(external?.refreshTime).toBe('')
    expect(external?.boundSecret).toBe('')
  })

  it('answers null for no manifest', () => {
    expect(externalSecret(null)).toBeNull()
    expect(externalSecret('not an object')).toBeNull()
  })
})
