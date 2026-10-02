import { describe, expect, it } from 'vitest'
import { centreOn, locate, parseQuery, searchNodes } from './topologySearch'

const nodes = [
  { id: '1', apiKind: 'Service', name: 'checkout', namespace: 'shop' },
  { id: '2', apiKind: 'Deployment', name: 'checkout-worker', namespace: 'shop' },
  { id: '3', apiKind: 'Pod', name: 'web-checkout-1', namespace: 'web' },
  { id: '4', apiKind: 'Service', name: 'cart', namespace: 'web' },
]

describe('topology search', () => {
  it('ranks an exact name, then a prefix, then a substring', () => {
    expect(searchNodes(nodes, 'checkout')).toEqual(['1', '2', '3'])
  })

  it('narrows by kind and namespace', () => {
    expect(searchNodes(nodes, 'kind:service')).toEqual(['4', '1'])
    expect(searchNodes(nodes, 'ns:web checkout')).toEqual(['3'])
    expect(parseQuery('Kind:Pod  ns:a  x y')).toEqual({ kind: 'pod', namespace: 'a', words: ['x', 'y'] })
  })

  it('finds nothing for an empty query', () => {
    expect(searchNodes(nodes, '   ')).toEqual([])
  })

  it('locates a folded or grouped object on the box standing for it', () => {
    const fold = new Map([['3', 'fold/x/pod']])
    const groups = new Map([['fold/x/pod', 'group/ns:web']])
    expect(locate('3', new Set(['group/ns:web']), [fold, groups])).toBe('group/ns:web')
    expect(locate('3', new Set(['fold/x/pod']), [fold, new Map()])).toBe('fold/x/pod')
    expect(locate('1', new Set(['1']), [fold, groups])).toBe('1')
    expect(locate('9', new Set(['1']), [fold, groups])).toBeNull()
  })

  it('centres a box, zooming in to readable but never out', () => {
    expect(centreOn({ x: 100, y: 50 }, { width: 800, height: 600 }, 0.2)).toEqual({ zoom: 0.8, panX: 320, panY: 260 })
    expect(centreOn({ x: 100, y: 50 }, { width: 800, height: 600 }, 2).zoom).toBe(2)
  })
})
