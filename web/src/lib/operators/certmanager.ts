/**
 * What a cert-manager Certificate says about itself, in cert-manager's words.
 *
 * QUOTATION, NOT VERDICT. Everything here is lifted verbatim out of the
 * Certificate's own manifest — the one GET the drawer already made. Ready and
 * Issuing are the controller's conclusions and are shown with the
 * controller's own status, reason and message (True/False/Unknown;
 * DoesNotExist, Failed, Renewing, …). PodSteer draws no conclusion of its own
 * on top: it does not go and read the Secret the key pair lands in, does not
 * parse the certificate, and does not compare `notAfter` to anything.
 *
 * THE EXPIRY JUDGEMENT IS NOT HERE, AND THAT IS THE POINT. "This certificate
 * expires in nine days and cert-manager is not renewing it" is a comparison
 * of two facts against a threshold, so it lives in the Go domain where a test
 * can argue with it and reaches the panel through a bound call. What this
 * file does with a timestamp is FORMAT it — see `remainingLabel`, which is
 * presentation of a subtraction, the same quotation `formatAge` performs on
 * an age in seconds.
 *
 * The Secret is NAMED and never read. `spec.secretName` is where cert-manager
 * writes the key pair; resolving it when this pane opens is exactly the
 * pattern the Secrets rule in CLAUDE.md refuses, and `InspectTLSSecret` is
 * the deliberate, audited way to look at one.
 *
 * Field names follow the Certificate CRD (cert-manager.io/v1):
 * https://cert-manager.io/docs/reference/api-docs/#cert-manager.io/v1.Certificate
 */

import { certificateExpiryLabel } from '$lib/certificateExpiry'

import { conditionOf, secondsUntil } from './panel'
import type { OperatorCondition } from './panel'

/** The Issuer or ClusterIssuer a Certificate is issued by. */
export interface CertManagerIssuerRef {
  kind: string
  name: string
  group: string
}

export interface CertManagerCertificate {
  /** status.conditions[type=Ready] — cert-manager's own conclusion. Null before it has said anything. */
  ready: OperatorCondition | null
  /** status.conditions[type=Issuing] — present while a (re)issuance is in flight. */
  issuing: OperatorCondition | null
  /** spec.issuerRef. `kind` defaults to "Issuer" per the CRD when unset. */
  issuerRef: CertManagerIssuerRef
  /** spec.secretName — the Secret the key pair is written to. */
  secretName: string
  commonName: string
  dnsNames: string[]
  ipAddresses: string[]
  uris: string[]
  /** spec.duration / spec.renewBefore, Go duration strings, verbatim. */
  duration: string
  renewBefore: string
  /** status.notBefore / status.notAfter / status.renewalTime, RFC 3339. */
  notBefore: string
  notAfter: string
  renewalTime: string
  /** status.revision — how many times cert-manager has issued this certificate. Null before the first. */
  revision: number | null
  /** status.failedIssuanceAttempts — non-null only once an issuance has failed. */
  failedIssuanceAttempts: number | null
}

/** The parts of the manifest this reads. */
interface CertificateManifest {
  spec?: {
    issuerRef?: { kind?: string; name?: string; group?: string }
    secretName?: string
    commonName?: string
    dnsNames?: string[]
    ipAddresses?: string[]
    uris?: string[]
    duration?: string
    renewBefore?: string
  }
  status?: {
    conditions?: unknown
    notBefore?: string
    notAfter?: string
    renewalTime?: string
    revision?: number
    failedIssuanceAttempts?: number
  }
}

/**
 * Reads a Certificate, or null when there is no manifest at all.
 *
 * A Certificate with no status yet — just applied, or one the controller has
 * not reached — comes back with empty strings and nulls rather than null, so
 * the panel renders the spec somebody just wrote and says nothing where
 * cert-manager has said nothing, rather than disappearing.
 */
export function certManagerCertificate(manifest: unknown): CertManagerCertificate | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as CertificateManifest

  return {
    ready: conditionOf(status.conditions, 'Ready'),
    issuing: conditionOf(status.conditions, 'Issuing'),
    issuerRef: {
      // The CRD defaults an unset kind to Issuer, so a reference with only a
      // name points at a namespaced Issuer — not at a ClusterIssuer, which
      // has to be named to be meant. Showing an empty kind would leave the
      // reader unable to tell which of the two to go and look at.
      kind: spec.issuerRef?.kind || 'Issuer',
      name: spec.issuerRef?.name ?? '',
      group: spec.issuerRef?.group ?? '',
    },
    secretName: spec.secretName ?? '',
    commonName: spec.commonName ?? '',
    dnsNames: spec.dnsNames ?? [],
    ipAddresses: spec.ipAddresses ?? [],
    uris: spec.uris ?? [],
    duration: spec.duration ?? '',
    renewBefore: spec.renewBefore ?? '',
    notBefore: status.notBefore ?? '',
    notAfter: status.notAfter ?? '',
    renewalTime: status.renewalTime ?? '',
    // Null, never zero: a Certificate cert-manager has never issued has no
    // revision, and "revision 0" is not a thing the controller ever writes.
    revision: status.revision ?? null,
    // Likewise the failure count, which cert-manager adds only once an
    // issuance has actually failed — absent means none has, not zero
    // failures out of an attempt that happened.
    failedIssuanceAttempts: status.failedIssuanceAttempts ?? null,
  }
}

/**
 * The remaining-time sentence for a status timestamp, or an em dash when it
 * does not parse.
 *
 * Delegates the wording to `$lib/certificateExpiry` so a certificate's
 * remaining time reads the same here as it does in the TLS Secret inspector
 * — two panels describing the same certificate in two different phrasings is
 * how an operator ends up believing they are looking at two certificates.
 * The em dash is the same answer `formatAge` gives an unreadable age, and for
 * the same reason: a timestamp this cannot read is not a deadline of now.
 */
export function remainingLabel(timestamp: string, now: number = Date.now()): string {
  const seconds = secondsUntil(timestamp, now)
  if (Number.isNaN(seconds)) return '—'
  return certificateExpiryLabel(seconds)
}
