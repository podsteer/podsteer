import { describe, expect, it } from 'vitest'

import {
  capacityNote,
  OVER_REQUEST_MARGIN,
  RESERVED_MULTIPLE,
  SCHEDULER_TIGHT,
  shareOf,
} from './nodeCapacity'

describe('capacityNote', () => {
  it('says nothing about an ordinary node', () => {
    // SILENCE IS THE COMMON CASE. A note beside every figure on every node is
    // one nobody reads by the third node.
    expect(capacityNote(2000, 1600, 8000, 'CPU')).toBeNull()
  })

  it('leads with the scheduler when pods reserved more than the node has', () => {
    // Not a rounding error: a node that was resized, or whose pods were
    // placed when it was larger. Nothing new lands until something leaves.
    const note = capacityNote(9000, 500, 8000, 'CPU')
    expect(note?.tone).toBe('warn')
    expect(note?.text).toContain('reserved more CPU than this node has')
  })

  it('warns once the scheduler is nearly out of room, whatever the node is doing', () => {
    const note = capacityNote(SCHEDULER_TIGHT * 80, 100, 8000, 'memory')
    expect(note?.tone).toBe('warn')
    expect(note?.text).toContain('90% of memory is already reserved')
    // THE HALF THAT MATTERS: the machine is idle and it still cannot take work.
    expect(note?.text).toContain('whatever the node is actually using')
  })

  it('names the node whose pods request nothing at all', () => {
    // How a node ends up overloaded with BestEffort pods: the scheduler counts
    // it as free however hard it is working.
    const note = capacityNote(0, 6000, 8000, 'CPU')
    expect(note?.tone).toBe('info')
    expect(note?.text).toContain('request no CPU at all')
  })

  it('says nothing about an empty node that also requests nothing', () => {
    // Requesting nothing while using nothing is an idle node, not a finding.
    expect(capacityNote(0, 0, 8000, 'CPU')).toBeNull()
  })

  it('names the gap that explains a stuck cluster', () => {
    // "Reserved 95%, using 8%" is the one comparison that explains why a calm
    // cluster refuses to schedule, and it appeared on no surface before.
    const note = capacityNote(5600, 640, 8000, 'CPU')
    expect(note?.text).toContain('fuller than it looks')
  })

  it('lets the scheduler warning win over the idle-reservation note', () => {
    // Both are true of a node that reserved 95% and uses 8%, and only one of
    // them says the thing somebody has to act on. Two sentences where one
    // will do is how a panel stops being read.
    const note = capacityNote(7600, 640, 8000, 'CPU')
    expect(note?.tone).toBe('warn')
    expect(note?.text).toContain('already reserved')
    expect(note?.text).not.toContain('fuller than it looks')
  })

  it('holds its tongue when the reservation is only a little above usage', () => {
    // Just under twice: ordinary headroom, deliberately left, not a finding.
    const requests = 5000
    expect(capacityNote(requests, requests / RESERVED_MULTIPLE + 1, 8000, 'CPU')).toBeNull()
  })

  it('holds its tongue when a big multiple is still a small share of the node', () => {
    // Ten times almost nothing is still almost nothing. Without the share
    // test, every quiet node in the cluster would carry this sentence.
    expect(capacityNote(400, 40, 8000, 'CPU')).toBeNull()
  })

  it('says when a node is working past what its pods reserved, without calling it wrong', () => {
    const note = capacityNote(1000, 1000 * OVER_REQUEST_MARGIN + 1, 8000, 'CPU')
    expect(note?.tone).toBe('info')
    expect(note?.text).toContain('using more CPU than its pods reserved')
    // Requests are a floor rather than a cap: this is not a fault, and the
    // sentence must not read as one.
    expect(note?.text).toContain('nothing is wrong')
  })

  it('says nothing when nothing reported an allocatable figure', () => {
    // Every share would be a division by zero, and a confident sentence about
    // one is worse than no sentence.
    expect(capacityNote(1000, 500, 0, 'CPU')).toBeNull()
  })

  it('names the dimension it was given, so one function serves both', () => {
    expect(capacityNote(0, 1, 100, 'memory')?.text).toContain('memory')
    expect(capacityNote(0, 1, 100, 'CPU')?.text).toContain('CPU')
  })
})

describe('shareOf', () => {
  it('is a percentage of allocatable', () => {
    expect(shareOf(2000, 8000)).toBe(25)
  })

  it('keeps a share past 100 rather than clamping it, so the caller decides', () => {
    expect(shareOf(9000, 8000)).toBeCloseTo(112.5)
  })

  it('answers zero for the figures a bar cannot draw', () => {
    expect(shareOf(1, 0)).toBe(0)
    expect(shareOf(Number.NaN, 8000)).toBe(0)
    expect(shareOf(-5, 8000)).toBe(0)
    expect(shareOf(1, Number.POSITIVE_INFINITY)).toBe(0)
  })
})
