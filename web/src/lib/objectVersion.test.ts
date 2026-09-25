import { describe, expect, it } from 'vitest'

import { imageTag, lastUpdated, latestTagWarning, objectVersion } from './objectVersion'

describe('imageTag', () => {
  it.each([
    ['registry.k8s.io/kube-proxy:v1.32.7', 'v1.32.7'],
    ['nginx:1.27', '1.27'],
    ['registry:5000/team/app:2.0.1', '2.0.1'],
    ['app:1.2@sha256:abc', '1.2'],
  ])('%s → %s', (image, tag) => expect(imageTag(image)).toBe(tag))

  it.each(['nginx', 'nginx:latest', 'app@sha256:abc', 'registry:5000/app', ''])('%j names no version', (image) => {
    expect(imageTag(image)).toBeNull()
  })
})

describe('objectVersion', () => {
  it('prefers the label, as written', () => {
    expect(
      objectVersion({ labels: { 'app.kubernetes.io/version': 'v1.0.0-dev-65' } }, [{ name: 'app', image: 'x:9' }])
        ?.version,
    ).toBe('v1.0.0-dev-65')
  })

  it('falls back to the main container\'s tag, not a sidecar listed first', () => {
    const found = objectVersion({ name: 'web', labels: {} }, [
      { name: 'istio-proxy', image: 'istio/proxyv2:1.24.0' },
      { name: 'web', image: 'shop/web:3.4.5' },
    ])
    expect(found?.version).toBe('3.4.5')
    expect(found?.source).toContain('web container')
  })

  it('says nothing rather than quote latest or a digest', () => {
    expect(objectVersion({ name: 'web' }, [{ name: 'web', image: 'shop/web:latest' }])).toBeNull()
  })
})

describe('lastUpdated', () => {
  const created = '2026-01-01T00:00:00Z'

  it('takes the newest write to the object, ignoring status writes', () => {
    expect(
      lastUpdated(
        [
          { time: '2026-03-01T00:00:00Z' },
          { time: '2026-09-24T10:00:00Z', subresource: 'status' },
          { time: '2026-05-01T00:00:00Z' },
        ],
        created,
      ),
    ).toBe('2026-05-01T00:00:00Z')
  })

  it('is null for an object never changed since it was created', () => {
    expect(lastUpdated([{ time: created }], created)).toBeNull()
    expect(lastUpdated(undefined, created)).toBeNull()
  })
})

describe('latestTagWarning', () => {
  it.each(['nginx:latest', 'registry.io/team/app:latest'])('warns on %s written out', (image) => {
    expect(latestTagWarning(image)).toContain(':latest tag')
  })

  it.each(['nginx', 'registry:5000/team/app'])('warns on %s, which pulls :latest implicitly', (image) => {
    expect(latestTagWarning(image)).toContain('No tag')
  })

  it.each(['nginx:1.27', 'app:latest@sha256:abc', 'app@sha256:abc', '', undefined])(
    'is quiet for %j — a version, or bytes pinned by digest',
    (image) => {
      expect(latestTagWarning(image)).toBeNull()
    },
  )
})
