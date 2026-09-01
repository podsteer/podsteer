import { describe, expect, it, vi } from 'vitest'
import { iconURI, preloadIcons } from './graphIcons'

describe('dependency map icons', () => {
  it('gives every kind the map draws its own icon', () => {
    // Every GraphKind in app/domain/graph.go. A kind with no icon silently
    // falls back to the pod's, so the map would draw two different things as
    // boxes and nobody would know which.
    const kinds = [
      'ingress', 'service', 'workload', 'replicaset', 'pod',
      'container', 'node', 'config', 'secret', 'claim', 'serviceaccount',
    ]

    const drawn = new Set(kinds.map((kind) => iconURI(kind, '#000')))

    expect(drawn.size).toBe(kinds.length)
  })

  it('falls back rather than drawing nothing for an unknown kind', () => {
    // A kind added in Go before this file catches up must still render.
    expect(iconURI('something-new', '#000')).toBe(iconURI('pod', '#000'))
  })

  it('survives a hash in the colour', () => {
    // The reason the SVG is percent-encoded rather than inlined raw: a '#'
    // starts a fragment, so an unencoded colour truncates the data URI and
    // the symbol vanishes.
    const uri = iconURI('pod', '#8ab4f8')

    expect(uri.startsWith('image://data:image/svg+xml;utf8,')).toBe(true)
    expect(uri).not.toContain('#8ab4f8')
    expect(decodeURIComponent(uri)).toContain('#8ab4f8')
  })

  it('colours the stroke, since ECharts cannot recolour an image symbol', () => {
    expect(iconURI('pod', '#ff0000')).not.toBe(iconURI('pod', '#00ff00'))
  })

  it('returns the same string for the same request', () => {
    // Cached: the same kind and colour recur across every node in a tier, and
    // building the string is pure work that would repeat on every redraw.
    expect(iconURI('service', '#123456')).toBe(iconURI('service', '#123456'))
  })

  it('draws real geometry, not an empty frame', () => {
    const svg = decodeURIComponent(iconURI('ingress', '#000'))

    expect(svg).toContain('viewBox="0 0 24 24"')
    expect(svg).toMatch(/<(path|circle|rect|line)/)
  })
})

describe('preloading', () => {
  it('resolves even when an icon will not decode', async () => {
    // An icon that fails is a node drawn plainly, not a map that refuses to
    // appear — so this must never reject.
    await expect(preloadIcons(['image://data:image/svg+xml;utf8,not-an-svg'])).resolves.toBeUndefined()
  })

  it('loads each distinct icon once', async () => {
    // Every node in a tier asks for the same kind and colour, so the list
    // handed to this is mostly duplicates.
    const loaded: string[] = []
    class FakeImage {
      onload: (() => void) | null = null
      set src(value: string) {
        loaded.push(value)
        queueMicrotask(() => this.onload?.())
      }
    }
    vi.stubGlobal('Image', FakeImage)

    const one = iconURI('pod', '#111')
    await preloadIcons([one, one, one, iconURI('service', '#111')])

    expect(loaded).toHaveLength(2)
    vi.unstubAllGlobals()
  })

  it('strips the echarts scheme before loading', async () => {
    // `image://` is ECharts' own prefix and is not a URL scheme a browser
    // knows; left on, every icon fails to load and silently draws nothing.
    const loaded: string[] = []
    class FakeImage {
      onload: (() => void) | null = null
      set src(value: string) {
        loaded.push(value)
        queueMicrotask(() => this.onload?.())
      }
    }
    vi.stubGlobal('Image', FakeImage)

    await preloadIcons([iconURI('pod', '#111')])

    expect(loaded[0].startsWith('data:image/svg+xml')).toBe(true)
    vi.unstubAllGlobals()
  })
})
