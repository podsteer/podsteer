import { describe, expect, it } from 'vitest'

import { formatEnvValue, formatMount, formatPorts, formatProbe, looksSensitive } from './container'

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
