import { describe, expect, it } from 'vitest'

import { podTemplateOf } from './podTemplate'

const template = { spec: { containers: [{ name: 'app', image: 'nginx' }] } }

describe('finding a controller’s pod template', () => {
  it('reads it straight off the five kinds that carry it directly', () => {
    const manifest = { spec: { template } }

    for (const kind of ['Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job']) {
      expect(podTemplateOf(manifest, kind)).toBe(template)
    }
  })

  it('reaches through the Job a CronJob creates', () => {
    // THE ONE THAT IS DIFFERENT, and the one it matters most for: between
    // runs a CronJob has no pods to open, so its template is the only
    // description of what it does. Read at the wrong depth it shows nothing.
    const manifest = { spec: { jobTemplate: { spec: { template } } } }

    expect(podTemplateOf(manifest, 'CronJob')).toBe(template)
  })

  it('does not find a CronJob’s template at the shallow path', () => {
    // The mirror of the above: a CronJob has no spec.template, so reading it
    // as though it were a Deployment must come back empty rather than
    // silently picking up something else.
    const manifest = { spec: { jobTemplate: { spec: { template } } } }

    expect(podTemplateOf(manifest, 'Deployment')).toBeNull()
  })

  it('answers null for anything without one', () => {
    // Null rather than an empty template: a kind this does not know has not
    // got a template with no containers in it, it has one this cannot find —
    // and the caller renders nothing rather than an empty section.
    expect(podTemplateOf({ spec: {} }, 'Deployment')).toBeNull()
    expect(podTemplateOf(null, 'Deployment')).toBeNull()
    expect(podTemplateOf({ kind: 'Pod' }, 'Pod')).toBeNull()
  })
})
