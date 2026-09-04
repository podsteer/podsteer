import { describe, expect, it } from 'vitest'
import {
  decideNotification,
  NOTIFY_COOLDOWN_MS,
  sourcesAreComparable,
  type NotifiableFinding,
  type NotifyInput,
} from './notify'
import { diffFindings } from './timeline'

const NOW = 1_700_000_000_000

function finding(id: string, severity = 'critical', title = id): NotifiableFinding {
  return { id, severity, title }
}

/** An input with every gate open, so each test closes exactly one. */
function input(overrides: Partial<NotifyInput> = {}): NotifyInput {
  return {
    clusterId: 'staging',
    enabled: true,
    deliverable: true,
    appeared: [finding('pods:crashloop', 'critical', 'CrashLoopBackOff')],
    comparable: true,
    isSnoozed: () => false,
    now: NOW,
    lastNotifiedAt: 0,
    ...overrides,
  }
}

describe('deciding on a desktop notification', () => {
  it('raises one for a new critical finding', () => {
    const plan = decideNotification(input())

    expect(plan).not.toBeNull()
    expect(plan?.title).toBe('New critical finding')
    expect(plan?.count).toBe(1)
    expect(plan?.clusterId).toBe('staging')
  })

  it('raises nothing when the operator has not turned it on', () => {
    // THE HALF THAT MATTERS, and it is the default. Asserting the state is not
    // enough — the question is whether a notification would be composed at
    // all, because everything downstream of this is a call to the OS.
    expect(decideNotification(input({ enabled: false }))).toBeNull()
  })

  it('raises nothing when the platform will not deliver', () => {
    // Not an error, and not something to retry: a machine that shows no
    // notifications is a machine the Settings pane says so about.
    expect(decideNotification(input({ deliverable: false }))).toBeNull()
  })

  it('never raises for a warning, only for a critical', () => {
    // The sound offers a warning motif because a sound is over in half a
    // second. A notification persists in a tray until it is dismissed, so the
    // bar for one is higher.
    const plan = decideNotification(
      input({ appeared: [finding('pods:restarting', 'warning'), finding('caps:waste', 'info')] }),
    )

    expect(plan).toBeNull()
  })

  it('counts only the criticals when a batch carries both', () => {
    const plan = decideNotification(
      input({
        appeared: [
          finding('a', 'critical', 'CrashLoopBackOff'),
          finding('b', 'warning', 'Restarting'),
          finding('c', 'critical', 'Pods cannot be scheduled'),
        ],
      }),
    )

    expect(plan?.count).toBe(2)
    expect(plan?.title).toBe('2 new critical findings')
  })

  it('raises nothing at all for a snoozed finding', () => {
    // A snooze is the operator saying they already know. Announcing it on the
    // desktop would be the loudest available way of ignoring that.
    const plan = decideNotification(input({ isSnoozed: () => true }))

    expect(plan).toBeNull()
  })

  it('counts the un-snoozed half of a batch and no more', () => {
    const plan = decideNotification(
      input({
        appeared: [finding('a', 'critical', 'CrashLoopBackOff'), finding('b', 'critical', 'OOMKilled')],
        isSnoozed: (found) => found.id === 'b',
      }),
    )

    expect(plan?.count).toBe(1)
    expect(plan?.body).toContain('CrashLoopBackOff')
    expect(plan?.body).not.toContain('OOMKilled')
  })

  it('is one notification naming the count, never one per finding', () => {
    // THE RATE LIMIT'S FIRST HALF. Twenty pods failing from one event is one
    // thing to an operator, and twenty notifications is a column nobody can
    // read — the same argument the alert sound makes about twenty chimes.
    const appeared = Array.from({ length: 20 }, (_, index) =>
      finding(`pods:crashloop:${index}`, 'critical', 'CrashLoopBackOff'),
    )

    const plan = decideNotification(input({ appeared }))

    expect(plan?.count).toBe(20)
    expect(plan?.title).toBe('20 new critical findings')
    // Twenty of the same rule is ONE line, because the headline already says
    // twenty and the rule is what somebody acts on.
    expect(plan?.body).toBe('staging — CrashLoopBackOff')
  })

  it('names at most three rules and counts the rest', () => {
    const appeared = ['one', 'two', 'three', 'four', 'five'].map((name) =>
      finding(name, 'critical', name),
    )

    const plan = decideNotification(input({ appeared }))

    expect(plan?.body).toBe('staging — one, two, three and 2 more')
  })

  it('holds off a second notification about the same cluster', () => {
    // THE RATE LIMIT'S SECOND HALF. A flapping workload produces a new batch
    // on every refresh, and the fastest refresh is five seconds.
    const plan = decideNotification(input({ lastNotifiedAt: NOW - 1_000 }))

    expect(plan).toBeNull()
  })

  it('raises again once the cooldown has passed', () => {
    const plan = decideNotification(input({ lastNotifiedAt: NOW - NOTIFY_COOLDOWN_MS }))

    expect(plan).not.toBeNull()
  })

  it('raises nothing when the assessment cannot be compared with the last', () => {
    // A partial refresh. See sourcesAreComparable.
    expect(decideNotification(input({ comparable: false }))).toBeNull()
  })

  it('composes a body carrying no object name, only rules and the cluster', () => {
    // THE COMMITMENT, asserted rather than commented. A delivered notification
    // is kept by the operating system, so a namespace or a pod name in one is
    // a namespace or a pod name written to disk.
    const plan = decideNotification(
      input({
        appeared: [finding('pods:crashloop', 'critical', 'CrashLoopBackOff')],
      }),
    )

    expect(plan?.body).toBe('staging — CrashLoopBackOff')
    expect(plan?.body).not.toContain('/')
  })

  it('replaces rather than stacks, by keying the notification on the cluster', () => {
    const first = decideNotification(input())
    const second = decideNotification(input({ lastNotifiedAt: 0 }))

    expect(first?.id).toBe(second?.id)
    expect(decideNotification(input({ clusterId: 'production' }))?.id).not.toBe(first?.id)
  })
})

describe('whether two assessments can be compared at all', () => {
  it('refuses to compare against nothing', () => {
    // The first assessment of a session establishes a baseline and announces
    // nothing — which diffFindings already says by reporting nothing
    // appeared, and this says about the sources for the same reason.
    expect(sourcesAreComparable(null, [])).toBe(false)
  })

  it('compares two assessments that read the same sources', () => {
    expect(sourcesAreComparable(['metrics'], ['metrics'])).toBe(true)
    expect(sourcesAreComparable([], [])).toBe(true)
    expect(sourcesAreComparable(['metrics', 'events'], ['events', 'metrics'])).toBe(true)
  })

  it('refuses when a source that was missing has come back', () => {
    // THE PARTIAL-REFRESH BUG. Every finding that source produces arrives in
    // the same instant, and not one of them is new — they were simply not
    // being looked at.
    expect(sourcesAreComparable(['metrics'], [])).toBe(false)
  })

  it('refuses when a source that answered has gone missing', () => {
    expect(sourcesAreComparable([], ['metrics'])).toBe(false)
  })

  it('keeps comparing a cluster that is permanently short of one source', () => {
    // A cluster with no metrics-server reports that on every refresh for
    // ever. Refusing to notify there would permanently mute exactly the
    // clusters most likely to need it — which is why the rule is that the
    // source set did not MOVE, not that it is empty.
    expect(sourcesAreComparable(['metrics'], ['metrics'])).toBe(true)
  })
})

describe('what reaches the decision', () => {
  it('announces nothing for findings that were already there at startup', () => {
    // END TO END THROUGH THE REAL DIFF, because this is the rule the whole
    // feature stands on and it lives in diffFindings rather than here: a
    // cluster broken since Tuesday is not news happening now.
    const current = new Map([['pods:crashloop', finding('pods:crashloop')]])

    const first = diffFindings<NotifiableFinding>(null, current)
    expect(decideNotification(input({ appeared: first.appeared }))).toBeNull()

    // The next refresh, with the same finding still outstanding: still
    // nothing, because it is in the baseline now.
    const second = diffFindings(first.next, current)
    expect(decideNotification(input({ appeared: second.appeared }))).toBeNull()
  })

  it('announces nothing when a refresh failed', () => {
    // A FAILED REFRESH CARRIES NO FINDINGS, and reading that as an assessment
    // reports the whole cluster clearing. diffFindings is passed null for it,
    // and null is not evidence about anything — so the baseline survives and
    // nothing appeared.
    const current = new Map([['pods:crashloop', finding('pods:crashloop')]])
    const established = diffFindings<NotifiableFinding>(null, current)

    const failed = diffFindings(established.next, null)

    expect(failed.appeared).toEqual([])
    expect(failed.next).toBe(established.next)
    expect(decideNotification(input({ appeared: failed.appeared }))).toBeNull()
  })

  it('announces a finding that cleared and came back', () => {
    // What somebody watching a flapping workload needs to see, and the reason
    // "new" is measured against the last assessment rather than against
    // everything ever seen.
    const outstanding = new Map([['pods:crashloop', finding('pods:crashloop')]])

    const first = diffFindings<NotifiableFinding>(null, outstanding)
    const cleared = diffFindings(first.next, new Map<string, NotifiableFinding>())
    const back = diffFindings(cleared.next, outstanding)

    expect(back.appeared).toHaveLength(1)
    expect(decideNotification(input({ appeared: back.appeared }))).not.toBeNull()
  })
})
