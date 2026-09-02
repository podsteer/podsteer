import { describe, expect, it } from 'vitest'
import { iconGeometry } from './graphIcons'

describe('dependency map icons', () => {
  it('gives every kind the map draws its own icon', () => {
    // Every GraphKind in app/domain/graph.go. A kind with no icon silently
    // falls back to the pod's, so the map would draw two different things as
    // boxes and nobody would know which.
    const kinds = [
      'ingress', 'service', 'workload', 'replicaset', 'pod',
      'container', 'node', 'config', 'secret', 'claim', 'serviceaccount',
    ]

    const drawn = new Set(kinds.map(iconGeometry))

    expect(drawn.size).toBe(kinds.length)
  })

  it('falls back rather than drawing nothing for an unknown kind', () => {
    // A kind added in Go before this file catches up must still render.
    expect(iconGeometry('something-new')).toBe(iconGeometry('pod'))
  })

  it('draws real geometry on the Lucide grid', () => {
    // The map inlines this into a group it scales itself, so the shapes have
    // to be on the 24x24 grid every Lucide icon uses.
    const geometry = iconGeometry('ingress')

    expect(geometry).toMatch(/<(path|circle|rect|line)/)
    expect(geometry).not.toContain('<svg')
  })
})
