import { describe, expect, it } from 'vitest'
import { matchesPodStatusChips, POD_STATUS_CHIPS } from './podStatusFilters'
import type { Pod } from './api/client'

/** A pod with just the fields a chip's predicate reads. Only the three DTO
    fields the domain already computes — phase, statusReason and each
    container's reason — ever appear here; nothing is re-derived. */
function pod(fields: Partial<Pod>): Pod {
  return { phase: 'Running', statusReason: '', containers: [], ...fields } as Pod
}

function chip(id: string) {
  const found = POD_STATUS_CHIPS.find((candidate) => candidate.id === id)
  if (!found) throw new Error(`no such chip: ${id}`)
  return found
}

describe('Failing', () => {
  it('quotes Pod.isHealthy, not the phase: a broken pod usually reports Running', () => {
    expect(chip('failing').predicate(pod({ phase: 'Failed', isHealthy: false }))).toBe(true)
    // CrashLoopBackOff and a container in Error both look like this.
    expect(chip('failing').predicate(pod({ phase: 'Running', isHealthy: false }))).toBe(true)
    expect(chip('failing').predicate(pod({ phase: 'Running', isHealthy: true }))).toBe(false)
    expect(chip('failing').predicate(pod({ phase: 'Succeeded', isHealthy: true }))).toBe(false)
  })

  it('leaves Pending and Terminating to their own chips', () => {
    expect(chip('failing').predicate(pod({ phase: 'Pending', isHealthy: false }))).toBe(false)
    expect(chip('failing').predicate(pod({ phase: 'Terminating', isHealthy: false }))).toBe(false)
  })
})

describe('Pending', () => {
  it('quotes Pod.phase === "Pending"', () => {
    expect(chip('pending').predicate(pod({ phase: 'Pending' }))).toBe(true)
    expect(chip('pending').predicate(pod({ phase: 'Running' }))).toBe(false)
  })
})

describe('Restarting', () => {
  it('quotes Pod.statusReason === "CrashLoopBackOff"', () => {
    expect(chip('restarting').predicate(pod({ statusReason: 'CrashLoopBackOff' }))).toBe(true)
    expect(chip('restarting').predicate(pod({ statusReason: 'ImagePullBackOff' }))).toBe(false)
  })

  it('is not tripped by a pod that merely HAS restarted, only one currently looping', () => {
    // RestartCount is deliberately not the field this chip reads: a stable
    // pod with a restart in its history must not be flagged as restarting
    // right now.
    expect(chip('restarting').predicate(pod({ statusReason: '', restarts: 12 } as Partial<Pod>))).toBe(
      false,
    )
  })
})

describe('ImagePullBackOff', () => {
  it('quotes Pod.statusReason === "ImagePullBackOff"', () => {
    expect(chip('imagepullbackoff').predicate(pod({ statusReason: 'ImagePullBackOff' }))).toBe(true)
    expect(chip('imagepullbackoff').predicate(pod({ statusReason: 'CrashLoopBackOff' }))).toBe(false)
  })
})

describe('Terminating', () => {
  it('quotes Pod.phase === "Terminating"', () => {
    expect(chip('terminating').predicate(pod({ phase: 'Terminating' }))).toBe(true)
    expect(chip('terminating').predicate(pod({ phase: 'Running' }))).toBe(false)
  })
})

describe('OOMKilled', () => {
  it('matches a pod whose CURRENT statusReason is OOMKilled', () => {
    expect(chip('oomkilled').predicate(pod({ statusReason: 'OOMKilled' }))).toBe(true)
  })

  it('matches a pod that has since restarted, from a container\'s lastTermination', () => {
    // statusReason now reads CrashLoopBackOff — the CURRENT state — and the
    // OOM only survives in the container's PREVIOUS life. This is the one
    // chip that has to look past statusReason for exactly that reason.
    const target = pod({
      statusReason: 'CrashLoopBackOff',
      containers: [
        { name: 'app', lastTermination: { reason: 'OOMKilled' } } as Pod['containers'][number],
      ],
    })
    expect(chip('oomkilled').predicate(target)).toBe(true)
  })

  it('does not match a pod with an unrelated restart history', () => {
    const target = pod({
      statusReason: 'CrashLoopBackOff',
      containers: [
        { name: 'app', lastTermination: { reason: 'Error' } } as Pod['containers'][number],
      ],
    })
    expect(chip('oomkilled').predicate(target)).toBe(false)
  })
})

describe('matchesPodStatusChips', () => {
  it('passes every pod through when nothing is selected', () => {
    expect(matchesPodStatusChips(pod({ phase: 'Running' }), [])).toBe(true)
  })

  it('ORs the selected chips: any one matching is enough', () => {
    const pending = pod({ phase: 'Pending' })
    expect(matchesPodStatusChips(pending, ['failing', 'pending'])).toBe(true)
    expect(matchesPodStatusChips(pending, ['failing', 'terminating'])).toBe(false)
  })
})
