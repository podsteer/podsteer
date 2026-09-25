/**
 * What an ExternalSecret says about itself, in the operator's own words.
 *
 * VALUES ARE NEVER READ. THIS IS THE WHOLE POINT OF THE FILE. An
 * ExternalSecret exists to copy material out of Vault, AWS Secrets Manager or
 * a cloud key store into a Kubernetes Secret, and the interesting question
 * when one is opened is which remote key maps to which local key and whether
 * the operator managed it — never what the value is. Nothing here resolves
 * the Secret this produces, and nothing here contacts the store: reading a
 * Secret's contents is `RevealSecretKey`'s deliberate, audited, one-key act,
 * and resolving one because a pane opened is exactly the pattern PodSteer
 * refuses (see "Secrets are read on request, never on render" in CLAUDE.md,
 * and Kubernetes' own guidance to alert on it). The panel names the target
 * Secret so an operator can go and open it on purpose.
 *
 * QUOTATION, NOT VERDICT, as everywhere in this directory. The Ready
 * condition is the operator's conclusion, shown with its own status, reason
 * and message (SecretSynced, SecretSyncedError, …); this file never decides
 * what a refusal from the store means.
 *
 * THE PANEL SERVES v1beta1 AND v1 ALIKE. The two API versions share this
 * shape field for field, and `operatorPanelFor` matches on the GROUP
 * (`external-secrets.io`) rather than the version, so a cluster on either —
 * or one mid-migration, serving both — gets the same panel with the same
 * fields rather than a blank pane on whichever version nobody thought to
 * enumerate.
 *
 * Field names follow the ExternalSecret CRD:
 * https://external-secrets.io/latest/api/externalsecret/
 */

import { conditionOf } from './panel'
import type { OperatorCondition } from './panel'

/** One remote-to-local key mapping, as the ExternalSecret declares it. */
export interface ExternalSecretMapping {
  /**
   * The key in the external store. For a `dataFrom.find` this is the path the
   * search is narrowed to, when one is given, because a find names no single
   * key — what it matched on is in `match`.
   */
  remoteKey: string
  /** The property within that key, when one is named. Empty otherwise. */
  property: string
  /**
   * The key written into the Kubernetes Secret. Empty for a dataFrom block,
   * which copies every key the remote reference yields and so names none.
   */
  localKey: string
  /** Where the mapping came from: 'data', 'dataFrom.extract' or 'dataFrom.find'. */
  origin: string
  /** For a dataFrom.find, what it matched on — a name regexp or a tag set — as written. */
  match: string
}

export interface ExternalSecret {
  ready: OperatorCondition | null
  /** spec.refreshInterval, e.g. "1h". Verbatim; "0" means refresh once and never again. */
  refreshInterval: string
  /** The Secret this writes. Defaults to the ExternalSecret's own name per the CRD. */
  targetName: string
  /** spec.target.creationPolicy / deletionPolicy, verbatim. */
  creationPolicy: string
  deletionPolicy: string
  /** Whether spec.target.template is set — the Secret's shape is templated rather than a straight copy. */
  templated: boolean
  /**
   * spec.secretStoreRef. `kind` defaults to "SecretStore" per the CRD; the other
   * value is "ClusterSecretStore", which is NOT namespaced and matters when following it.
   */
  storeKind: string
  storeName: string
  mappings: ExternalSecretMapping[]
  /** status.refreshTime — when the operator last read the store. */
  refreshTime: string
  /** status.syncedResourceVersion — the version of the Secret it last wrote. */
  syncedResourceVersion: string
  /** status.binding.name, when the operator recorded the Secret it bound to. */
  boundSecret: string
}

/** The parts of the manifest this reads. */
interface ExternalSecretManifest {
  metadata?: { name?: string }
  spec?: {
    refreshInterval?: string
    secretStoreRef?: { name?: string; kind?: string }
    target?: {
      name?: string
      creationPolicy?: string
      deletionPolicy?: string
      template?: unknown
    }
    data?: RawData[]
    dataFrom?: RawDataFrom[]
  }
  status?: {
    conditions?: unknown
    refreshTime?: string
    syncedResourceVersion?: string
    binding?: { name?: string }
  }
}

interface RawRemoteRef {
  key?: string
  property?: string
}

interface RawData {
  secretKey?: string
  remoteRef?: RawRemoteRef
}

interface RawDataFrom {
  extract?: RawRemoteRef
  find?: {
    path?: string
    name?: { regexp?: string }
    tags?: Record<string, string>
  }
}

/**
 * Reads an ExternalSecret, or null when there is no manifest at all.
 *
 * An ExternalSecret with no status yet — just applied, or one the operator
 * has not reached — comes back with empty strings rather than null, so the
 * panel shows the mappings somebody just wrote and says nothing about the
 * sync, which is the truth of it.
 */
export function externalSecret(manifest: unknown): ExternalSecret | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { metadata = {}, spec = {}, status = {} } = manifest as ExternalSecretManifest

  return {
    ready: conditionOf(status.conditions, 'Ready'),
    refreshInterval: spec.refreshInterval ?? '',
    // The CRD defaults an unnamed target to the ExternalSecret's own name, so
    // an empty field here would name no Secret at all on the most common
    // spelling of the object — and naming the Secret is what lets an operator
    // go and open it deliberately.
    targetName: spec.target?.name || metadata.name || '',
    creationPolicy: spec.target?.creationPolicy ?? '',
    deletionPolicy: spec.target?.deletionPolicy ?? '',
    // Presence, not content: a template rewrites the Secret's keys and type,
    // so the mappings below are not the whole story once one is set, and the
    // panel has to be able to say so.
    templated: spec.target?.template !== undefined && spec.target.template !== null,
    // ClusterSecretStore is cluster-scoped, so following a reference to one
    // means looking outside this namespace. Defaulting the kind wrong sends
    // the reader to a namespace that has no such object.
    storeKind: spec.secretStoreRef?.kind || 'SecretStore',
    storeName: spec.secretStoreRef?.name ?? '',
    mappings: mappingsOf(spec),
    refreshTime: status.refreshTime ?? '',
    syncedResourceVersion: status.syncedResourceVersion ?? '',
    boundSecret: status.binding?.name ?? '',
  }
}

/**
 * The mappings, `spec.data` first and `spec.dataFrom` after it, in the order
 * each was written.
 *
 * Both are shown because they answer the same question in two shapes: `data`
 * names one remote key per local key, `dataFrom` copies whatever a reference
 * yields. A panel showing only the first would silently omit most of what an
 * ExternalSecret written the second way actually produces.
 */
function mappingsOf(spec: NonNullable<ExternalSecretManifest['spec']>): ExternalSecretMapping[] {
  const mappings: ExternalSecretMapping[] = []

  for (const entry of spec.data ?? []) {
    mappings.push({
      remoteKey: entry.remoteRef?.key ?? '',
      property: entry.remoteRef?.property ?? '',
      localKey: entry.secretKey ?? '',
      origin: 'data',
      match: '',
    })
  }

  for (const entry of spec.dataFrom ?? []) {
    if (entry.extract) {
      mappings.push({
        remoteKey: entry.extract.key ?? '',
        property: entry.extract.property ?? '',
        // An extract copies every key the remote reference yields, so it
        // names no local key. Inventing one from the remote key would claim
        // a Secret key that may not be what lands there.
        localKey: '',
        origin: 'dataFrom.extract',
        match: '',
      })
      continue
    }
    if (entry.find) {
      mappings.push({
        remoteKey: entry.find.path ?? '',
        property: '',
        localKey: '',
        origin: 'dataFrom.find',
        match: findMatch(entry.find),
      })
    }
  }

  return mappings
}

/**
 * What a find matched on, as written: the name regexp, or the tag set as
 * `key=value` pairs.
 *
 * The regexp is quoted rather than described — an operator comparing it
 * against what turned up in the Secret needs the pattern itself, and any
 * paraphrase of a regular expression is a different regular expression.
 */
function findMatch(find: NonNullable<RawDataFrom['find']>): string {
  if (find.name?.regexp) return find.name.regexp
  const tags = Object.entries(find.tags ?? {})
  if (tags.length > 0) return tags.map(([key, value]) => `${key}=${value}`).join(', ')
  return ''
}
