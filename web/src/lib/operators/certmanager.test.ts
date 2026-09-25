import { describe, expect, it } from 'vitest'

import { certManagerCertificate, remainingLabel } from './certmanager'

const now = Date.parse('2026-09-04T12:00:00Z')

/** An issued certificate cert-manager is happy with — the ordinary case. */
const issued = {
  apiVersion: 'cert-manager.io/v1',
  kind: 'Certificate',
  metadata: { name: 'shop-tls', namespace: 'shop' },
  spec: {
    secretName: 'shop-tls',
    issuerRef: { kind: 'ClusterIssuer', name: 'letsencrypt-prod', group: 'cert-manager.io' },
    commonName: 'shop.example.com',
    dnsNames: ['shop.example.com', 'www.shop.example.com'],
    ipAddresses: ['203.0.113.10'],
    uris: ['spiffe://example.com/shop'],
    duration: '2160h0m0s',
    renewBefore: '360h0m0s',
  },
  status: {
    conditions: [
      {
        type: 'Ready',
        status: 'True',
        reason: 'Ready',
        message: 'Certificate is up to date and has not expired',
        lastTransitionTime: '2026-08-20T06:12:00Z',
      },
    ],
    notBefore: '2026-08-20T05:12:00Z',
    notAfter: '2026-11-18T05:12:00Z',
    renewalTime: '2026-11-03T05:12:00Z',
    revision: 4,
  },
}

/** A certificate whose issuance keeps failing, as cert-manager records it. */
const failing = {
  apiVersion: 'cert-manager.io/v1',
  kind: 'Certificate',
  metadata: { name: 'shop-tls', namespace: 'shop' },
  spec: {
    secretName: 'shop-tls',
    // No kind: the CRD reads that as a namespaced Issuer.
    issuerRef: { name: 'internal-ca' },
    dnsNames: ['shop.internal'],
  },
  status: {
    conditions: [
      {
        type: 'Ready',
        status: 'False',
        reason: 'Failed',
        message: 'The certificate request has failed to complete and will be retried: order is in "invalid" state',
        lastTransitionTime: '2026-09-04T09:41:00Z',
      },
      {
        type: 'Issuing',
        status: 'True',
        reason: 'Failed',
        message: 'The certificate request has failed to complete and will be retried',
        lastTransitionTime: '2026-09-04T09:41:00Z',
      },
    ],
    notAfter: '2026-09-06T00:00:00Z',
    renewalTime: '2026-09-04T00:00:00Z',
    revision: 2,
    failedIssuanceAttempts: 5,
  },
}

describe('reading a cert-manager Certificate', () => {
  it('quotes the Ready condition in cert-manager’s own words', () => {
    expect(certManagerCertificate(issued)?.ready).toEqual({
      status: 'True',
      reason: 'Ready',
      message: 'Certificate is up to date and has not expired',
      since: '2026-08-20T06:12:00Z',
    })
    // Nothing is issuing on a settled certificate, and that absence is a fact.
    expect(certManagerCertificate(issued)?.issuing).toBeNull()
  })

  it('reads the issuer, the target Secret and every subject alternative name', () => {
    const certificate = certManagerCertificate(issued)

    expect(certificate?.issuerRef).toEqual({
      kind: 'ClusterIssuer',
      name: 'letsencrypt-prod',
      group: 'cert-manager.io',
    })
    // The Secret is NAMED and never read — see the module comment and the
    // Secrets rule in CLAUDE.md.
    expect(certificate?.secretName).toBe('shop-tls')
    expect(certificate?.commonName).toBe('shop.example.com')
    expect(certificate?.dnsNames).toEqual(['shop.example.com', 'www.shop.example.com'])
    expect(certificate?.ipAddresses).toEqual(['203.0.113.10'])
    expect(certificate?.uris).toEqual(['spiffe://example.com/shop'])
  })

  it('carries the durations and the status timestamps verbatim', () => {
    const certificate = certManagerCertificate(issued)

    expect(certificate?.duration).toBe('2160h0m0s')
    expect(certificate?.renewBefore).toBe('360h0m0s')
    expect(certificate?.notBefore).toBe('2026-08-20T05:12:00Z')
    expect(certificate?.notAfter).toBe('2026-11-18T05:12:00Z')
    expect(certificate?.renewalTime).toBe('2026-11-03T05:12:00Z')
    expect(certificate?.revision).toBe(4)
  })

  it('defaults an issuerRef with no kind to Issuer, as the CRD does', () => {
    // An unqualified reference is to a namespaced Issuer, never to a
    // ClusterIssuer, which has to be named to be meant.
    expect(certManagerCertificate(failing)?.issuerRef).toEqual({
      kind: 'Issuer',
      name: 'internal-ca',
      group: '',
    })
  })

  it('reports a failed issuance with the reason and the attempt count', () => {
    const certificate = certManagerCertificate(failing)

    expect(certificate?.ready).toMatchObject({ status: 'False', reason: 'Failed' })
    expect(certificate?.issuing).toMatchObject({ status: 'True', reason: 'Failed' })
    expect(certificate?.failedIssuanceAttempts).toBe(5)
    expect(certificate?.revision).toBe(2)
  })

  it('carries a Ready status cert-manager has never written before as itself', () => {
    // An unknown enum value renders as itself: this file maps nothing onto a
    // vocabulary it happens to know.
    const certificate = certManagerCertificate({
      spec: {},
      status: { conditions: [{ type: 'Ready', status: 'Weird', reason: 'Whatever' }] },
    })

    expect(certificate?.ready).toMatchObject({ status: 'Weird', reason: 'Whatever' })
  })

  it('says nothing where cert-manager has said nothing', () => {
    // A just-applied Certificate has a spec and no status: empty strings and
    // nulls, never a throw, so the panel shows what somebody wrote.
    const certificate = certManagerCertificate({
      spec: { secretName: 'new-tls', issuerRef: { name: 'internal-ca' }, dnsNames: ['new.internal'] },
    })

    expect(certificate?.ready).toBeNull()
    expect(certificate?.issuing).toBeNull()
    expect(certificate?.notAfter).toBe('')
    expect(certificate?.renewalTime).toBe('')
    expect(certificate?.commonName).toBe('')
    expect(certificate?.ipAddresses).toEqual([])
    // Null and never zero: cert-manager writes no revision until it has
    // issued once, and no failure count until an issuance has failed.
    expect(certificate?.revision).toBeNull()
    expect(certificate?.failedIssuanceAttempts).toBeNull()
  })

  it('answers null for no manifest', () => {
    expect(certManagerCertificate(null)).toBeNull()
    expect(certManagerCertificate('not an object')).toBeNull()
  })
})

describe('labelling the time left on a certificate', () => {
  it('borrows the TLS Secret inspector’s own wording', () => {
    // Same sentence here as in the TLS Secret inspector: two panels phrasing
    // the same certificate differently is how somebody ends up believing
    // there are two certificates.
    expect(remainingLabel('2026-11-18T05:12:00Z', now)).toBe('expires in 74 days')
    expect(remainingLabel('2026-09-04T18:00:00Z', now)).toBe('expires today')
  })

  it('says a certificate has expired rather than clamping it at zero', () => {
    expect(remainingLabel('2026-08-30T12:00:00Z', now)).toBe('expired 5 days ago')
    expect(remainingLabel('2026-09-04T06:00:00Z', now)).toBe('expired today')
  })

  it('returns an em dash for an absent or unparseable timestamp', () => {
    // The same answer formatAge gives an unreadable age, and for the same
    // reason: a timestamp this cannot read is not a deadline of now.
    expect(remainingLabel('', now)).toBe('—')
    expect(remainingLabel('soon', now)).toBe('—')
  })
})
