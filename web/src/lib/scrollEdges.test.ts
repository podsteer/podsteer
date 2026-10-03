import { describe, expect, it } from 'vitest'
import { scrollEdges, scrollStep } from './scrollEdges'

describe('scroll edges', () => {
  it('offers neither side when everything fits', () => {
    expect(scrollEdges(0, 500, 500)).toEqual({ left: false, right: false })
  })

  it('offers the right at the start, both in the middle, the left at the end', () => {
    expect(scrollEdges(0, 300, 900)).toEqual({ left: false, right: true })
    expect(scrollEdges(200, 300, 900)).toEqual({ left: true, right: true })
    expect(scrollEdges(600, 300, 900)).toEqual({ left: true, right: false })
  })

  it('treats a fraction short of either end as the end', () => {
    expect(scrollEdges(0.5, 300, 900).left).toBe(false)
    expect(scrollEdges(599.5, 300, 900).right).toBe(false)
  })

  it('steps by most of the visible width', () => {
    expect(scrollStep(500)).toBe(400)
    expect(scrollStep(10)).toBe(40)
  })
})
