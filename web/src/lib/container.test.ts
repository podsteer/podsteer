import { describe, expect, it } from 'vitest'

import {
  formatEnvValue,
  formatMount,
  formatPorts,
  formatProbe,
  looksSensitive,
  type PodManifest,
} from './container'

describe('formatProbe', () => {
  it('renders all four handler types', () => {
    // WHERE THE INCUMBENTS FALL DOWN. Headlamp renders only httpGet and exec
    // — a tcpSocket probe renders as nothing and an exec probe as the literal
    // string "undefined" — and neither it nor Freelens nor Octant handles
    // gRPC, a probe type since 1.24. A probe missing from a pane reads as a
    // container that has none, which is a calmer fact than the truth.
    expect(formatProbe({ httpGet: { path: '/healthz', port: 8080 } })).toContain('http-get http://:8080/healthz')
    expect(formatProbe({ exec: { command: ['cat', '/tmp/healthy'] } })).toContain('exec [cat /tmp/healthy]')
    expect(formatProbe({ tcpSocket: { port: 5432 } })).toContain('tcp-socket :5432')
    expect(formatProbe({ grpc: { port: 9090, service: 'health' } })).toContain('grpc :9090 health')
  })

  it('fills in the API server defaults rather than printing zeros', () => {
    // An omitted period is ten seconds and an omitted threshold is three.
    // Printing "period=0s #failure=0" would describe a probe that cannot
    // exist and would make the budget arithmetic look wrong.
    const probe = formatProbe({ httpGet: { port: 8080 } })
    expect(probe).toContain('period=10s')
    expect(probe).toContain('#failure=3')
  })

  it('says so when it does not recognise the handler', () => {
    // A Kubernetes newer than this code. Rendering an empty probe would read
    // as "no probe", which is the opposite of the truth.
    expect(formatProbe({ initialDelaySeconds: 5 })).toContain('unrecognised')
  })

  it('is empty for a container with no probe', () => {
    expect(formatProbe(undefined)).toBe('')
  })
})

describe('formatEnvValue', () => {
  it('never resolves a secret, and prints kubectl’s own wording', () => {
    // No API call, no permission needed, and the form operators recognise.
    expect(
      formatEnvValue({ name: 'PW', valueFrom: { secretKeyRef: { name: 'creds', key: 'pw' } } }),
    ).toBe("<set to the key 'pw' in secret 'creds'>")
  })

  it('keeps upstream’s asymmetry between secrets and config maps', () => {
    // "in secret" against "of config map". It is kubectl's, not a typo, and
    // matching it is the point of matching it.
    expect(
      formatEnvValue({ name: 'HOST', valueFrom: { configMapKeyRef: { name: 'cfg', key: 'host' } } }),
    ).toContain("of config map 'cfg'")
  })

  it('passes a literal through unchanged', () => {
    expect(formatEnvValue({ name: 'LOG_LEVEL', value: 'debug' })).toBe('debug')
  })
})

describe('looksSensitive', () => {
  it('catches the credential shapes that actually leak', () => {
    // A literal in env[].value sits in the pod spec, readable by anyone with
    // `get pod`, and no client in this category masks it — because Kubernetes
    // does not consider it a secret.
    expect(looksSensitive({ name: 'AWS_KEY', value: 'AKIAIOSFODNN7EXAMPLE' })).toBe(true)
    expect(looksSensitive({ name: 'TOKEN', value: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc' })).toBe(true)
    expect(looksSensitive({ name: 'KEY', value: '-----BEGIN RSA PRIVATE KEY-----' })).toBe(true)
    expect(looksSensitive({ name: 'DB_PASSWORD', value: 'hunter2-but-long-enough' })).toBe(true)
  })

  it('stays narrow, so ordinary values are not masked', () => {
    // Masking half an environment as suspected credentials would train people
    // to reveal everything by reflex, which is the failure mode that matters.
    expect(looksSensitive({ name: 'LOG_LEVEL', value: 'debug' })).toBe(false)
    expect(looksSensitive({ name: 'MONGODB_HOSTS', value: 'mongodb-0.mongodb,mongodb-1.mongodb' })).toBe(false)
    // Too short to be a credential, even with a suggestive name.
    expect(looksSensitive({ name: 'SECRET', value: 'abc' })).toBe(false)
    // A reference carries no value at all, so there is nothing to suspect.
    expect(looksSensitive({ name: 'PW', valueFrom: { secretKeyRef: { name: 'c', key: 'k' } } })).toBe(false)
  })
})

describe('formatPorts and formatMount', () => {
  it('calls out a host port, which decides whether a second replica fits', () => {
    expect(formatPorts([{ name: 'http', containerPort: 8080, hostPort: 80 }])).toContain('host 80')
  })

  it('renders a mount the way kubectl does, read-only flag included', () => {
    expect(formatMount({ name: 'cfg', mountPath: '/etc/cfg', readOnly: true })).toBe('/etc/cfg from cfg (ro)')
    expect(formatMount({ name: 'data', mountPath: '/data' })).toBe('/data from data (rw)')
    expect(formatMount({ name: 'cfg', mountPath: '/etc/x', subPath: 'a.yaml' })).toContain('path="a.yaml"')
  })
})

const pod: PodManifest = {
  metadata: {
    name: 'api-7d8f-k76wb',
    namespace: 'development',
    uid: 'a1b2c3',
    labels: { app: 'api' },
    annotations: { 'app.kubernetes.io/name': 'authentication-identity-service' },
  },
  spec: {
    nodeName: 'node-1',
    serviceAccountName: 'api',
    containers: [
      { name: 'app', resources: { requests: { cpu: '100m' }, limits: { memory: '512Mi' } } },
    ],
  },
  status: { podIP: '10.0.0.1', hostIP: '10.1.0.1' },
}

describe('the downward API', () => {
  it('shows the value, not the path to it', () => {
    // kubectl prints `<metadata.name>` because kubectl is describing a
    // template. This is describing a running pod, and the running pod knows
    // its own name — so a column of `<...>` placeholders is a column of
    // values nobody has been shown.
    expect(formatEnvValue({ name: 'POD_NAME', valueFrom: { fieldRef: { fieldPath: 'metadata.name' } } }, pod))
      .toBe('api-7d8f-k76wb')
    expect(
      formatEnvValue(
        { name: 'NS', valueFrom: { fieldRef: { fieldPath: 'metadata.namespace' } } },
        pod,
      ),
    ).toBe('development')
    expect(
      formatEnvValue({ name: 'IP', valueFrom: { fieldRef: { fieldPath: 'status.podIP' } } }, pod),
    ).toBe('10.0.0.1')
  })

  it('reads a keyed label or annotation', () => {
    expect(
      formatEnvValue(
        {
          name: 'SERVICE',
          valueFrom: {
            fieldRef: { fieldPath: "metadata.annotations['app.kubernetes.io/name']" },
          },
        },
        pod,
      ),
    ).toBe('authentication-identity-service')

    expect(
      formatEnvValue(
        { name: 'APP', valueFrom: { fieldRef: { fieldPath: "metadata.labels['app']" } } },
        pod,
      ),
    ).toBe('api')
  })

  it('resolves a container resource against that container', () => {
    expect(
      formatEnvValue(
        {
          name: 'CPU',
          valueFrom: { resourceFieldRef: { containerName: 'app', resource: 'requests.cpu' } },
        },
        pod,
      ),
    ).toBe('100m')
    expect(
      formatEnvValue(
        {
          name: 'MEM',
          valueFrom: { resourceFieldRef: { containerName: 'app', resource: 'limits.memory' } },
        },
        pod,
      ),
    ).toBe('512Mi')
  })

  it('keeps the path when it cannot resolve one', () => {
    // THE IMPORTANT HALF. A path this does not recognise, an annotation that
    // is not set, or a pane with no manifest yet must fall back to what
    // kubectl would have printed — never to a blank, and never to a guess.
    expect(
      formatEnvValue(
        { name: 'MISSING', valueFrom: { fieldRef: { fieldPath: "metadata.labels['absent']" } } },
        pod,
      ),
    ).toBe('<metadata.labels[\'absent\']>')

    expect(
      formatEnvValue({ name: 'NEW', valueFrom: { fieldRef: { fieldPath: 'spec.somethingNew' } } }, pod),
    ).toBe('<spec.somethingNew>')

    expect(
      formatEnvValue({ name: 'POD_NAME', valueFrom: { fieldRef: { fieldPath: 'metadata.name' } } }),
    ).toBe('<metadata.name>')
  })

  it('refuses paths the API server would have refused', () => {
    // Only the fields a fieldRef may actually name are resolved. A general
    // path walker would happily answer `spec.containers[0].image` — which
    // Kubernetes rejects — so the pane would be showing a value the pod could
    // never have been given.
    expect(
      formatEnvValue(
        { name: 'IMAGE', valueFrom: { fieldRef: { fieldPath: 'spec.containers[0].image' } } },
        pod,
      ),
    ).toBe('<spec.containers[0].image>')
  })

  it('still names a secret rather than resolving one', () => {
    // Nothing here ever reads a Secret. See container.ts.
    expect(
      formatEnvValue(
        { name: 'KEY', valueFrom: { secretKeyRef: { name: 'app-secrets', key: 'jwt' } } },
        pod,
      ),
    ).toBe("<set to the key 'jwt' in secret 'app-secrets'>")
  })
})
