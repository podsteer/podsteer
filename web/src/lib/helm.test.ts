import { describe, expect, it } from 'vitest'

import type { HelmListing, ResourceKind } from './api/client'
import {
  ARGO_NOTE,
  countedRevisions,
  helmState,
  helmTime,
  servesArgoApplications,
  showsEmptyCopy,
} from './helm'

/** A listing in whichever state a test needs, with everything else plausible. */
function listing(overrides: Partial<HelmListing> = {}): HelmListing {
  return {
    releases: [],
    status: 'listed',
    refusal: '',
    truncated: false,
    listedAt: 1_757_000_000,
    driver: 'secret',
    ...overrides,
  } as HelmListing
}

/** A discovered kind, as the navigator's kind list carries one. */
function kind(group: string, name: string): ResourceKind {
  return {
    id: `${group || 'core'}/v1alpha1/${name.toLowerCase()}s`,
    group,
    version: 'v1alpha1',
    kind: name,
    namespaced: true,
    category: 'Custom Resources',
    subcategory: group,
    title: `${name}s`,
    singular: name,
    rich: false,
  } as ResourceKind
}

describe('helmState', () => {
  it('reads a listed status as answered', () => {
    expect(helmState('listed', '')).toEqual({ kind: 'listed' })
  })

  it('reads an empty status as listed, so an older shape still renders', () => {
    expect(helmState('', '')).toEqual({ kind: 'listed' })
  })

  it('reads a refusal as information rather than an error, and not retryable', () => {
    // A refusal is not a fault: pressing Refresh asks the same account the
    // same question and adds one more denied request to an audit log.
    const state = helmState('forbidden', 'Your account may not list Secrets in shop.')

    expect(state).toEqual({
      kind: 'unavailable',
      status: 'forbidden',
      tone: 'info',
      message: 'Your account may not list Secrets in shop.',
      retryable: false,
    })
  })

  it('reads a failure as an error with a retry', () => {
    const state = helmState('failed', 'The cluster could not be reached.')

    expect(state.kind).toBe('unavailable')
    if (state.kind !== 'unavailable') return
    expect(state.tone).toBe('error')
    expect(state.retryable).toBe(true)
  })

  it('falls back to its own sentence when Go sent none', () => {
    const state = helmState('forbidden', '')

    expect(state.kind).toBe('unavailable')
    if (state.kind !== 'unavailable') return
    expect(state.message).toContain('may not list Secrets')
    // Even the fallback must not read as an absence of releases.
    expect(state.message).toContain('says nothing about whether Helm is used')
  })

  it('treats a status it has never seen as failed rather than as listed', () => {
    // Reading an unknown word as a successful listing would render zero rows
    // and claim the cluster holds no releases.
    const state = helmState('something-new', '')

    expect(state.kind).toBe('unavailable')
    if (state.kind !== 'unavailable') return
    expect(state.status).toBe('failed')
  })
})

describe('showsEmptyCopy', () => {
  it('allows the zero-row copy on a listed, empty cluster', () => {
    expect(showsEmptyCopy(listing({ status: 'listed', releases: [] }))).toBe(true)
  })

  it('NEVER allows the zero-row copy on a forbidden listing', () => {
    // THE LOAD-BEARING TEST OF THIS FEATURE. A refused listing carries zero
    // releases exactly as an empty cluster does, so a call site testing
    // `releases.length === 0` on its own would render "Helm has installed
    // nothing here" to somebody who was simply not allowed to look.
    const refused = listing({
      status: 'forbidden',
      releases: [],
      refusal: 'Your account may not list Secrets in shop.',
    })

    expect(showsEmptyCopy(refused)).toBe(false)
    expect(refused.releases?.length ?? 0).toBe(0)
  })

  it('does not allow it on a failed listing either', () => {
    expect(showsEmptyCopy(listing({ status: 'failed', releases: [] }))).toBe(false)
  })

  it('does not allow it before anything has been read', () => {
    expect(showsEmptyCopy(null)).toBe(false)
  })

  it('does not allow it when there are rows to show', () => {
    const populated = listing({
      releases: [
        {
          namespace: 'shop',
          name: 'podinfo',
          current: {
            namespace: 'shop',
            name: 'podinfo',
            revision: 1,
            status: 'deployed',
            createdAt: 1,
            modifiedAt: 0,
            secretName: 'sh.helm.release.v1.podinfo.v1',
          },
          revisionCount: 1,
          revisions: [],
        },
      ] as HelmListing['releases'],
    })

    expect(showsEmptyCopy(populated)).toBe(false)
  })
})

describe('servesArgoApplications', () => {
  it('finds Argo CD by its group AND kind together', () => {
    expect(servesArgoApplications([kind('argoproj.io', 'Application')])).toBe(true)
  })

  it('does not match Application in another group', () => {
    // "Application" is a kind in three API groups; half a coordinate matches
    // the wrong controller, the same trap gitops/panel.ts names.
    expect(servesArgoApplications([kind('app.k8s.io', 'Application')])).toBe(false)
    expect(servesArgoApplications([kind('argoproj.io', 'Rollout')])).toBe(false)
  })

  it('is false on a cluster with no custom kinds at all', () => {
    expect(servesArgoApplications([])).toBe(false)
  })

  it('claims only that Argo CD is installed, never who manages what', () => {
    // The wording is the feature: it is a fact about the cluster derived from
    // discovery, not a claim about any workload's provenance.
    expect(ARGO_NOTE).toContain('installed in this cluster')
    expect(ARGO_NOTE).toContain('helm template')
    expect(ARGO_NOTE).toContain('storing no Helm release')
  })
})

describe('countedRevisions', () => {
  it('states one rather than hiding it', () => {
    expect(countedRevisions(1)).toBe('1 revision')
    expect(countedRevisions(0)).toBe('0 revisions')
    expect(countedRevisions(12)).toBe('12 revisions')
  })
})

describe('helmTime', () => {
  it('reads Helm\'s Unix seconds', () => {
    expect(helmTime(1_757_000_000)?.toISOString()).toBe(new Date(1_757_000_000_000).toISOString())
  })

  it('answers null for an absent label rather than 1970', () => {
    // `modifiedAt` is absent on most revisions by design, and a date nobody
    // wrote must not render as one somebody did.
    expect(helmTime(0)).toBeNull()
    expect(helmTime(-1)).toBeNull()
  })
})
