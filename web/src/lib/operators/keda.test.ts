import { describe, expect, it } from 'vitest'

import { isCredentialKey, kedaScaledObject } from './keda'

/** A ScaledObject KEDA is happily driving — the ordinary case. */
const active = {
  apiVersion: 'keda.sh/v1alpha1',
  kind: 'ScaledObject',
  metadata: { name: 'checkout', namespace: 'shop' },
  spec: {
    scaleTargetRef: { kind: 'Deployment', name: 'checkout', apiVersion: 'apps/v1', envSourceContainerName: 'app' },
    minReplicaCount: 2,
    maxReplicaCount: 40,
    idleReplicaCount: 0,
    pollingInterval: 15,
    cooldownPeriod: 300,
    triggers: [
      {
        type: 'prometheus',
        name: 'queue-depth',
        metadata: {
          serverAddress: 'http://prometheus.monitoring:9090',
          query: 'sum(rate(checkout_requests_total[2m]))',
          threshold: '100',
        },
        authenticationRef: { name: 'prometheus-auth' },
      },
      {
        type: 'kafka',
        metadata: {
          bootstrapServers: 'kafka.shop:9092',
          topic: 'orders',
          consumerGroup: 'checkout',
          lagThreshold: '50',
        },
        authenticationRef: { name: 'kafka-auth', kind: 'ClusterTriggerAuthentication' },
      },
    ],
  },
  status: {
    conditions: [
      { type: 'Ready', status: 'True', reason: 'ScaledObjectReady', message: 'ScaledObject is defined correctly and is ready for scaling', lastTransitionTime: '2026-09-01T08:00:00Z' },
      { type: 'Active', status: 'True', reason: 'ScalerActive', message: 'Scaling is performed because triggers are active', lastTransitionTime: '2026-09-04T11:30:00Z' },
      { type: 'Fallback', status: 'False', reason: 'NoFallbackFound', message: 'No fallback exists' },
    ],
    hpaName: 'keda-hpa-checkout',
    scaleTargetKind: 'apps/v1.Deployment',
    originalReplicaCount: 3,
  },
}

/** A ScaledObject whose scaler will not answer, serving its fallback count. */
const failing = {
  apiVersion: 'keda.sh/v1alpha1',
  kind: 'ScaledObject',
  metadata: {
    name: 'reports',
    namespace: 'shop',
    annotations: { 'autoscaling.keda.sh/paused-replicas': '4' },
  },
  spec: {
    // No kind: the CRD reads that as a Deployment.
    scaleTargetRef: { name: 'reports' },
    maxReplicaCount: 10,
    triggers: [
      {
        type: 'azure-servicebus',
        metadata: {
          queueName: 'reports',
          // Inline, which KEDA permits and its own design discourages.
          connectionString: 'Endpoint=sb://example.servicebus.windows.net/;SharedAccessKey=hunter2',
          messageCount: '5',
        },
      },
    ],
  },
  status: {
    conditions: [
      { type: 'Ready', status: 'False', reason: 'ScalerFailed', message: 'failed to resolve the connection string', lastTransitionTime: '2026-09-04T10:00:00Z' },
      { type: 'Active', status: 'Unknown', reason: 'UnknownState' },
      { type: 'Fallback', status: 'True', reason: 'FallbackExists', message: 'At least one trigger is falling back on this scaled object' },
    ],
    hpaName: 'keda-hpa-reports',
    scaleTargetKind: 'apps/v1.Deployment',
  },
}

describe('reading a KEDA ScaledObject', () => {
  it('quotes Ready, Active and Fallback in KEDA’s own words', () => {
    const scaled = kedaScaledObject(active)

    expect(scaled?.ready).toMatchObject({ status: 'True', reason: 'ScaledObjectReady' })
    expect(scaled?.active).toMatchObject({ status: 'True', reason: 'ScalerActive', since: '2026-09-04T11:30:00Z' })
    expect(scaled?.fallback).toMatchObject({ status: 'False', reason: 'NoFallbackFound' })
  })

  it('reads the scale target and the HPA KEDA created to drive it', () => {
    const scaled = kedaScaledObject(active)

    expect(scaled?.target).toEqual({
      kind: 'Deployment',
      name: 'checkout',
      apiVersion: 'apps/v1',
      containerName: 'app',
    })
    expect(scaled?.hpaName).toBe('keda-hpa-checkout')
    expect(scaled?.scaleTargetKind).toBe('apps/v1.Deployment')
    expect(scaled?.originalReplicaCount).toBe(3)
  })

  it('defaults a scaleTargetRef with no kind to Deployment, as the CRD does', () => {
    expect(kedaScaledObject(failing)?.target).toEqual({
      kind: 'Deployment',
      name: 'reports',
      apiVersion: '',
      containerName: '',
    })
  })

  it('distinguishes an unset replica bound from a bound of zero', () => {
    // KEDA's defaults differ per field, so an absent bound is a fact about
    // the spec — rendering it as zero claims a floor of nought on a workload
    // KEDA will never take below one.
    const scaled = kedaScaledObject(active)

    expect(scaled?.minReplicas).toBe(2)
    expect(scaled?.maxReplicas).toBe(40)
    expect(scaled?.idleReplicas).toBe(0)
    expect(scaled?.pollingInterval).toBe(15)
    expect(scaled?.cooldownPeriod).toBe(300)

    const sparse = kedaScaledObject(failing)
    expect(sparse?.minReplicas).toBeNull()
    expect(sparse?.idleReplicas).toBeNull()
    expect(sparse?.pollingInterval).toBeNull()
    expect(sparse?.cooldownPeriod).toBeNull()
    expect(sparse?.maxReplicas).toBe(10)
  })

  it('lists every trigger with its metadata in the order KEDA wrote it', () => {
    const triggers = kedaScaledObject(active)?.triggers ?? []

    expect(triggers.map((trigger) => trigger.type)).toEqual(['prometheus', 'kafka'])
    expect(triggers[0]?.name).toBe('queue-depth')
    // An unnamed trigger is empty rather than invented.
    expect(triggers[1]?.name).toBe('')
    expect(triggers[0]?.metadata).toEqual([
      { key: 'serverAddress', value: 'http://prometheus.monitoring:9090', redacted: false },
      { key: 'query', value: 'sum(rate(checkout_requests_total[2m]))', redacted: false },
      { key: 'threshold', value: '100', redacted: false },
    ])
  })

  it('names a TriggerAuthentication and never resolves it', () => {
    // A ClusterTriggerAuthentication is not namespaced, so it is a different
    // object to go and look at from one of the same name in this namespace.
    const triggers = kedaScaledObject(active)?.triggers ?? []

    expect(triggers[0]).toMatchObject({ authenticationRef: 'prometheus-auth', clusterAuthentication: false })
    expect(triggers[1]).toMatchObject({ authenticationRef: 'kafka-auth', clusterAuthentication: true })
  })

  it('shows an inline connection string’s key and never its value', () => {
    // The key is shown because an operator needs to know a credential is
    // configured inline before they can move it; the value is dropped in the
    // parser, so nothing downstream can put it on screen or in a screenshot.
    const metadata = kedaScaledObject(failing)?.triggers[0]?.metadata ?? []

    expect(metadata).toEqual([
      { key: 'queueName', value: 'reports', redacted: false },
      { key: 'connectionString', value: '', redacted: true },
      { key: 'messageCount', value: '5', redacted: false },
    ])
    expect(JSON.stringify(kedaScaledObject(failing))).not.toContain('hunter2')
  })

  it('treats the paused annotation as a pause even when its value is empty', () => {
    // KEDA reads the annotation's PRESENCE as the pause, so `paused-replicas: ""`
    // is paused exactly as `"0"` is. Deriving it from a non-empty string
    // would report a paused ScaledObject as running.
    const empty = kedaScaledObject({
      metadata: { annotations: { 'autoscaling.keda.sh/paused-replicas': '' } },
      spec: {},
    })

    expect(empty?.paused).toBe(true)
    expect(empty?.pausedReplicas).toBe('')

    const held = kedaScaledObject(failing)
    expect(held?.paused).toBe(true)
    expect(held?.pausedReplicas).toBe('4')

    const running = kedaScaledObject(active)
    expect(running?.paused).toBe(false)
    expect(running?.pausedReplicas).toBe('')
  })

  it('carries a scaler type and a Ready status KEDA has never written before as themselves', () => {
    const scaled = kedaScaledObject({
      spec: { triggers: [{ type: 'some-new-scaler', metadata: { setting: 'on' } }] },
      status: { conditions: [{ type: 'Ready', status: 'Weird' }] },
    })

    expect(scaled?.triggers[0]?.type).toBe('some-new-scaler')
    expect(scaled?.triggers[0]?.metadata).toEqual([{ key: 'setting', value: 'on', redacted: false }])
    expect(scaled?.ready).toMatchObject({ status: 'Weird' })
  })

  it('says nothing where KEDA has said nothing', () => {
    const scaled = kedaScaledObject({
      spec: { scaleTargetRef: { name: 'reports' }, triggers: [{ type: 'cron', metadata: {} }] },
    })

    expect(scaled?.ready).toBeNull()
    expect(scaled?.active).toBeNull()
    expect(scaled?.fallback).toBeNull()
    expect(scaled?.hpaName).toBe('')
    expect(scaled?.scaleTargetKind).toBe('')
    expect(scaled?.originalReplicaCount).toBeNull()
    expect(scaled?.paused).toBe(false)
    expect(scaled?.triggers[0]?.metadata).toEqual([])
  })

  it('answers null for no manifest', () => {
    expect(kedaScaledObject(null)).toBeNull()
    expect(kedaScaledObject('not an object')).toBeNull()
  })
})

describe('deciding which trigger metadata keys could carry a credential', () => {
  it('redacts anything named like a credential, whatever its casing', () => {
    expect(isCredentialKey('connectionString')).toBe(true)
    expect(isCredentialKey('connectionStringFromEnv')).toBe(true)
    expect(isCredentialKey('password')).toBe(true)
    expect(isCredentialKey('PASSWORD')).toBe(true)
    expect(isCredentialKey('apiKey')).toBe(true)
    expect(isCredentialKey('api_key')).toBe(true)
    expect(isCredentialKey('awsAccessKeyID')).toBe(true)
    expect(isCredentialKey('sasl')).toBe(true)
    expect(isCredentialKey('tlsClientCert')).toBe(true)
    expect(isCredentialKey('privateKey')).toBe(true)
    expect(isCredentialKey('authMode')).toBe(true)
    expect(isCredentialKey('bearerToken')).toBe(true)
    expect(isCredentialKey('secretRef')).toBe(true)
    expect(isCredentialKey('credentialFromEnv')).toBe(true)
  })

  it('leaves the keys that name a source rather than a way in', () => {
    // Redacting these would leave a panel that says a Prometheus trigger
    // exists and refuses to say what it queries — the whole content of the
    // trigger.
    expect(isCredentialKey('serverAddress')).toBe(false)
    expect(isCredentialKey('query')).toBe(false)
    expect(isCredentialKey('topic')).toBe(false)
    expect(isCredentialKey('bootstrapServers')).toBe(false)
    expect(isCredentialKey('queueName')).toBe(false)
    expect(isCredentialKey('threshold')).toBe(false)
    expect(isCredentialKey('consumerGroup')).toBe(false)
    expect(isCredentialKey('')).toBe(false)
  })
})
