import { describe, expect, it } from 'vitest'

import { formatTaint, isCordoned, nodeTaints } from './taints'

describe('a node’s taints', () => {
  it('writes them the way a toleration is written', () => {
    // The pod panel opposite renders tolerations in this form, and the two
    // are read against each other — a taint written differently is a taint
    // somebody has to translate before they can answer the question.
    expect(formatTaint({ key: 'workload', value: 'gpu', effect: 'NoSchedule' })).toBe(
      'workload=gpu:NoSchedule',
    )
  })

  it('leaves out an absent value rather than writing an empty one', () => {
    // `node.kubernetes.io/unreachable:NoExecute` is a real taint with no
    // value, and `key=:NoExecute` is not how anything writes it.
    expect(formatTaint({ key: 'node.kubernetes.io/unreachable', effect: 'NoExecute' })).toBe(
      'node.kubernetes.io/unreachable:NoExecute',
    )
  })

  it('never drops the effect', () => {
    // The effect is the half that decides what happens: NoSchedule keeps new
    // pods off, NoExecute evicts the ones already running.
    expect(formatTaint({ key: 'spot', value: 'true', effect: 'NoExecute' })).toContain(':NoExecute')
  })

  it('reads cordoning as its own fact', () => {
    // Cordoning adds a taint, but an operator who ran `kubectl cordon` wants
    // to see that they did — not to decode their own action out of a list.
    expect(isCordoned({ spec: { unschedulable: true } })).toBe(true)
    expect(isCordoned({ spec: { taints: [{ key: 'x', effect: 'NoSchedule' }] } })).toBe(false)
    expect(isCordoned(null)).toBe(false)
  })

  it('has none when the node has none', () => {
    expect(nodeTaints({ spec: {} })).toEqual([])
    expect(nodeTaints(null)).toEqual([])
  })
})
