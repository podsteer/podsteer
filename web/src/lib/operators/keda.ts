/**
 * What a KEDA ScaledObject says about itself, in KEDA's own words.
 *
 * QUOTATION, NOT VERDICT. The triggers, the target and the replica bounds are
 * lifted verbatim out of the ScaledObject's own manifest — the one GET the
 * drawer already made. Ready, Active and Fallback are KEDA's conclusions and
 * are shown with KEDA's own status, reason and message; this file never
 * decides what Active=False means, never asks a scaler whether it can be
 * reached, and never resolves the TriggerAuthentication a trigger names.
 *
 * A SCALER TYPE IS WHATEVER IT SAYS. KEDA ships more than sixty scalers and
 * gains more every release, so an unrecognised `type` renders as itself
 * rather than being mapped onto a shorter list of things this file happens to
 * know. The same holds for a metadata key.
 *
 * THE ONE RULE HERE IS `isCredentialKey`, AND IT IS CONSERVATIVE ON PURPOSE.
 * KEDA's own design puts credentials in a TriggerAuthentication rather than
 * inline, so on a well-formed cluster this normally redacts nothing at all.
 * It exists because `*FromEnv` metadata and inline connection strings do
 * occur — a `connectionString` pasted straight into `spec.triggers[].metadata`
 * is a documented, working configuration — and a panel that printed one would
 * put a credential on screen, into a screenshot, and into whatever that
 * screenshot was pasted into. The KEY is still shown, because an operator
 * needs to know a connection string is configured inline before they can go
 * and move it; the VALUE never crosses into the panel.
 *
 * Field names follow the ScaledObject spec (keda.sh/v1alpha1):
 * https://keda.sh/docs/latest/reference/scaledobject-spec/
 */

import { conditionOf } from './panel'
import type { OperatorCondition } from './panel'

/**
 * The annotation whose mere PRESENCE pauses a ScaledObject. Its value is the
 * replica count KEDA holds the target at.
 */
const PAUSED_ANNOTATION = 'autoscaling.keda.sh/paused-replicas'

/** One key of a trigger's metadata, as KEDA recorded it. */
export interface KedaTriggerMetadata {
  key: string
  /** Empty when `redacted` is set — the value is never rendered in that case. */
  value: string
  /** Whether the key names something that could carry a credential. */
  redacted: boolean
}

export interface KedaTrigger {
  /** The scaler type: "prometheus", "kafka", "cron", … verbatim, whatever it says. */
  type: string
  /** spec.triggers[].name, empty when the trigger is unnamed. */
  name: string
  /** The metadata, in the order KEDA wrote it. */
  metadata: KedaTriggerMetadata[]
  /** The TriggerAuthentication this trigger uses, NAMED and never resolved. Empty when none. */
  authenticationRef: string
  /** Whether the authenticationRef is a ClusterTriggerAuthentication. */
  clusterAuthentication: boolean
}

export interface KedaScaleTarget {
  /** Defaults to "Deployment" per the CRD when spec.scaleTargetRef.kind is unset. */
  kind: string
  name: string
  apiVersion: string
  /** spec.scaleTargetRef.envSourceContainerName, empty when unset. */
  containerName: string
}

export interface KedaScaledObject {
  ready: OperatorCondition | null
  active: OperatorCondition | null
  /** status.conditions[type=Fallback] — true means KEDA is serving the fallback replica count. */
  fallback: OperatorCondition | null
  target: KedaScaleTarget
  /**
   * spec.minReplicaCount / maxReplicaCount / idleReplicaCount. Null when unset —
   * which is not the same as zero, and KEDA's own defaults differ per field.
   */
  minReplicas: number | null
  maxReplicas: number | null
  idleReplicas: number | null
  /** spec.pollingInterval and spec.cooldownPeriod, in seconds. Null when unset. */
  pollingInterval: number | null
  cooldownPeriod: number | null
  triggers: KedaTrigger[]
  /** status.hpaName — the HorizontalPodAutoscaler KEDA created and drives. */
  hpaName: string
  /** status.scaleTargetKind, e.g. "apps/v1.Deployment". */
  scaleTargetKind: string
  /** status.originalReplicaCount — what the target had before KEDA first scaled it. */
  originalReplicaCount: number | null
  /**
   * The autoscaling.keda.sh/paused-replicas annotation, empty when absent. Its
   * mere presence pauses the ScaledObject, so an empty-string VALUE is still a pause.
   */
  pausedReplicas: string
  /** Whether that annotation is present at all — see pausedReplicas. */
  paused: boolean
}

/** The parts of the manifest this reads. */
interface ScaledObjectManifest {
  metadata?: {
    annotations?: Record<string, string>
  }
  spec?: {
    scaleTargetRef?: {
      kind?: string
      name?: string
      apiVersion?: string
      envSourceContainerName?: string
    }
    minReplicaCount?: number
    maxReplicaCount?: number
    idleReplicaCount?: number
    pollingInterval?: number
    cooldownPeriod?: number
    triggers?: RawTrigger[]
  }
  status?: {
    conditions?: unknown
    hpaName?: string
    scaleTargetKind?: string
    originalReplicaCount?: number
  }
}

interface RawTrigger {
  type?: string
  name?: string
  metadata?: Record<string, unknown>
  authenticationRef?: { name?: string; kind?: string }
}

/**
 * Reads a ScaledObject, or null when there is no manifest at all.
 *
 * A ScaledObject with no status yet — just applied, or one KEDA has not
 * reached — comes back with empty strings and nulls rather than null, so the
 * panel shows the triggers somebody just wrote and says nothing about
 * readiness, which is the truth of it.
 */
export function kedaScaledObject(manifest: unknown): KedaScaledObject | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { metadata = {}, spec = {}, status = {} } = manifest as ScaledObjectManifest

  const annotations = metadata.annotations ?? {}
  const pausedReplicas = annotations[PAUSED_ANNOTATION]

  return {
    ready: conditionOf(status.conditions, 'Ready'),
    active: conditionOf(status.conditions, 'Active'),
    fallback: conditionOf(status.conditions, 'Fallback'),
    target: {
      // The CRD defaults an unset kind to Deployment, which is the shape most
      // ScaledObjects in the wild are written in; an empty kind would leave
      // the reader unable to tell what the ScaledObject is actually driving.
      kind: spec.scaleTargetRef?.kind || 'Deployment',
      name: spec.scaleTargetRef?.name ?? '',
      apiVersion: spec.scaleTargetRef?.apiVersion ?? '',
      containerName: spec.scaleTargetRef?.envSourceContainerName ?? '',
    },
    // `?? null` and never `?? 0`: KEDA's defaults for these differ per field
    // (min 0, max 100, idle unset entirely), and rendering an absent bound as
    // zero would claim a floor of nought on a workload KEDA will never scale
    // below one. An unset bound is a fact about the spec, so the panel shows
    // the default's absence and lets KEDA own what it means.
    minReplicas: spec.minReplicaCount ?? null,
    maxReplicas: spec.maxReplicaCount ?? null,
    idleReplicas: spec.idleReplicaCount ?? null,
    pollingInterval: spec.pollingInterval ?? null,
    cooldownPeriod: spec.cooldownPeriod ?? null,
    triggers: (spec.triggers ?? []).map(readTrigger),
    hpaName: status.hpaName ?? '',
    scaleTargetKind: status.scaleTargetKind ?? '',
    originalReplicaCount: status.originalReplicaCount ?? null,
    pausedReplicas: pausedReplicas ?? '',
    // The annotation's PRESENCE is the pause, not its value: KEDA reads
    // `paused-replicas: ""` as paused just as it reads `"0"`. Deriving this
    // from a non-empty string instead would report a paused ScaledObject as
    // running, which is the one thing an operator opens this panel to check.
    paused: pausedReplicas !== undefined,
  }
}

function readTrigger(trigger: RawTrigger): KedaTrigger {
  const authenticationRef = trigger.authenticationRef

  return {
    type: trigger.type ?? '',
    name: trigger.name ?? '',
    metadata: Object.entries(trigger.metadata ?? {}).map(([key, value]) => {
      const redacted = isCredentialKey(key)
      return {
        key,
        // The value is dropped here, in the parser, rather than hidden in the
        // component: a value that never enters the panel's data cannot be
        // revealed by a future control that forgets to check the flag.
        value: redacted ? '' : String(value ?? ''),
        redacted,
      }
    }),
    authenticationRef: authenticationRef?.name ?? '',
    // A ClusterTriggerAuthentication is not namespaced, so it is a different
    // object to go and look at from a TriggerAuthentication of the same name.
    clusterAuthentication: authenticationRef?.kind === 'ClusterTriggerAuthentication',
  }
}

/**
 * The substrings that make a metadata key a candidate for carrying a
 * credential rather than naming a source.
 *
 * Matched case-insensitively as substrings, so `connectionStringFromEnv`,
 * `awsAccessKeyID` and `tlsClientCert` all match. Erring towards redacting a
 * harmless key costs the reader one lookup in the YAML tab; erring the other
 * way puts a secret on screen.
 */
const CREDENTIAL_KEY_PARTS = [
  'password',
  'secret',
  'token',
  'credential',
  'apikey',
  'api_key',
  'accesskey',
  'connectionstring',
  'connection',
  'sasl',
  'privatekey',
  'cert',
  'auth',
]

/**
 * Whether a trigger metadata key could carry a credential rather than name a
 * source.
 *
 * This is a match on the key's NAME and nothing else — it never looks at the
 * value, because a connection string and a queue name are both just strings
 * and no inspection of the content could tell them apart honestly. Keys that
 * name where to look rather than how to get in — `serverAddress`, `query`,
 * `topic`, `bootstrapServers` — are deliberately not matched: redacting them
 * would leave a panel that says a Prometheus trigger exists and refuses to
 * say what it queries, which is the whole content of the trigger.
 */
export function isCredentialKey(key: string): boolean {
  const lower = key.toLowerCase()
  return CREDENTIAL_KEY_PARTS.some((part) => lower.includes(part))
}
