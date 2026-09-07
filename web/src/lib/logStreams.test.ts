import { describe, expect, it } from 'vitest'

import { MAX_LOG_STREAMS, planLogStreams, streamLabel } from './logStreams'

const pods = [
  { name: 'api-0', containers: ['app', 'istio-proxy'] },
  { name: 'api-1', containers: ['app', 'istio-proxy'] },
]

describe('planning which log streams to open', () => {
  it('opens one per container of every pod when the choice is "All"', () => {
    // THE DEFECT THIS FILE EXISTS FOR. The pane resolved "All" as
    // `selectedContainer || pod.containers[0]`, so a pod with a sidecar
    // streamed its first container and reported that as all of them.
    const plan = planLogStreams(pods, '')

    expect(plan.streams).toEqual([
      { pod: 'api-0', container: 'app' },
      { pod: 'api-0', container: 'istio-proxy' },
      { pod: 'api-1', container: 'app' },
      { pod: 'api-1', container: 'istio-proxy' },
    ])
    expect(plan.missing).toEqual([])
    expect(plan.truncated).toBe(0)
  })

  it('opens exactly one per pod when a container is named', () => {
    const plan = planLogStreams(pods, 'istio-proxy')
    expect(plan.streams).toEqual([
      { pod: 'api-0', container: 'istio-proxy' },
      { pod: 'api-1', container: 'istio-proxy' },
    ])
  })

  it('names the pods a chosen container is not in, rather than asking anyway', () => {
    // The container list is the UNION across pods, so a selection that fits
    // one pod can be absent from another. Asking anyway earns "container is
    // not valid for pod" from the API server, once per pod, and the pane has
    // one error slot to put them all in.
    const mixed = [...pods, { name: 'worker-0', containers: ['app'] }]
    const plan = planLogStreams(mixed, 'istio-proxy')

    expect(plan.streams.map((s) => s.pod)).toEqual(['api-0', 'api-1'])
    expect(plan.missing).toEqual(['worker-0'])
  })

  it('contributes nothing for a pod whose containers are not known yet', () => {
    // A pod can legitimately arrive before its container list does; an empty
    // container name is a request the API server refuses.
    const plan = planLogStreams([{ name: 'api-0', containers: [] }], '')
    expect(plan.streams).toEqual([])
    expect(plan.truncated).toBe(0)
  })

  it('stops at the cap and says how many it left', () => {
    // Every container of every pod of a large workload is N×M requests
    // against a rate limiter shared with the tab's polling. A truncated read
    // that looks complete is the same fault as "All" streaming one container.
    const many = Array.from({ length: 30 }, (_, i) => ({
      name: `pod-${i}`,
      containers: ['app', 'sidecar'],
    }))
    const plan = planLogStreams(many, '', 40)

    expect(plan.streams).toHaveLength(40)
    expect(plan.truncated).toBe(20)
  })

  it('does not cap a named container, which was never multiplied', () => {
    // Naming a container has always opened one stream per pod, however many
    // pods there are. Capping that would stop showing logs somebody was
    // already getting — a regression dressed as a safeguard. The cap is for
    // the multiplication "All" introduces, and only for that.
    const many = Array.from({ length: 60 }, (_, i) => ({
      name: `pod-${i}`,
      containers: ['app', 'sidecar'],
    }))
    const plan = planLogStreams(many, 'app', 40)

    expect(plan.streams).toHaveLength(60)
    expect(plan.truncated).toBe(0)
  })

  it('caps at a number chosen to hold an ordinary workload whole', () => {
    // 40 is 20 pods with a sidecar each, or 40 single-container pods: past
    // the point where reading them in one pane is useful, and short of
    // starving the view behind it.
    expect(MAX_LOG_STREAMS).toBe(40)
  })

  it('labels a stream by pod and container', () => {
    expect(streamLabel('api-0', 'istio-proxy')).toBe('api-0/istio-proxy')
  })
})
