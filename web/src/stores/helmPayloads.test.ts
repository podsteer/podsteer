import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The bindings do not exist outside the Wails runtime, so the one call this
// store makes is stubbed — the same pattern secretReveals.test.ts uses.
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    readHelmRelease: vi.fn(),
  }
})

import type { HelmListing, HelmReleaseDetail } from '$lib/api/client'
import { readHelmRelease } from '$lib/api/client'
import { toCSV } from '$lib/csv'
import { timeline } from './timeline.svelte'
import { helmPayloadKey, helmPayloads } from './helmPayloads.svelte'
import { REVEAL_HIDE_AFTER_MS } from './revealHolder.svelte'

const mockRead = vi.mocked(readHelmRelease)

/**
 * A sentinel that could only have come out of a release payload.
 *
 * Every assertion below is "this string is not there", so it has to be
 * distinctive enough that finding it anywhere is proof rather than
 * coincidence.
 */
const SENTINEL = 'PAYLOAD-SENTINEL-hunter2-must-not-escape'

const CLUSTER = 'dev'
const NAMESPACE = 'shop'
const RELEASE = 'podinfo'
const REVISION = 3

const KEY = helmPayloadKey(CLUSTER, NAMESPACE, RELEASE, REVISION)

function detail(overrides: Partial<HelmReleaseDetail> = {}): HelmReleaseDetail {
  return {
    namespace: NAMESPACE,
    name: RELEASE,
    revision: REVISION,
    status: 'deployed',
    chart: { name: 'podinfo', version: '6.5.4', appVersion: '6.5.4', description: '' },
    description: 'Upgrade complete',
    values: `password: ${SENTINEL}`,
    notes: `the admin password is ${SENTINEL}`,
    manifest: 'apiVersion: v1\nkind: Secret\ndata:\n  password: <hidden, 7 bytes>\n',
    maskedDocuments: 1,
    secretName: 'sh.helm.release.v1.podinfo.v3',
    ...overrides,
  } as HelmReleaseDetail
}

/** The listing the page renders and could export, which the payload must
    never be written back into. */
function listing(): HelmListing {
  return {
    releases: [
      {
        namespace: NAMESPACE,
        name: RELEASE,
        current: {
          namespace: NAMESPACE,
          name: RELEASE,
          revision: REVISION,
          status: 'deployed',
          createdAt: 1_757_000_000,
          modifiedAt: 0,
          secretName: 'sh.helm.release.v1.podinfo.v3',
        },
        revisionCount: 3,
        revisions: [],
      },
    ],
    status: 'listed',
    refusal: '',
    truncated: false,
    listedAt: 1_757_000_000,
    driver: 'secret',
  } as unknown as HelmListing
}

beforeEach(() => {
  vi.useFakeTimers()
  helmPayloads.forgetAll()
  timeline.forget(CLUSTER)
  mockRead.mockReset()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('reading one revision', () => {
  it('splits the payload so only the values and notes are under a timer', async () => {
    mockRead.mockResolvedValueOnce(detail())
    await helmPayloads.read(CLUSTER, NAMESPACE, RELEASE, REVISION)

    expect(helmPayloads.at(KEY).facts?.chart.appVersion).toBe('6.5.4')
    expect(helmPayloads.sensitiveAt(KEY)?.values).toContain(SENTINEL)

    vi.advanceTimersByTime(REVEAL_HIDE_AFTER_MS)

    // THE MATERIAL IS GONE, NOT MERELY UNRENDERED — showing it again costs
    // another deliberate, audited read.
    expect(helmPayloads.sensitiveAt(KEY)).toBeUndefined()
    expect(helmPayloads.isRevealed(KEY)).toBe(false)

    // THE MASKED MANIFEST AND THE CHART STAY. The manifest arrived with its
    // Secret documents already masked in Go, so there is nothing in it to
    // time out, and the chart identity is the two columns the list could not
    // afford rather than anything secret.
    expect(helmPayloads.at(KEY).facts?.manifest).toContain('<hidden, 7 bytes>')
    expect(helmPayloads.at(KEY).facts?.chart.name).toBe('podinfo')
  })

  it('reads nothing until it is asked to', () => {
    // The store is imported and the key is computed, and no read has
    // happened. Opening a drawer must never become a Secret read.
    expect(mockRead).not.toHaveBeenCalled()
    expect(helmPayloads.at(KEY).facts).toBeNull()
  })

  it('drops everything about a revision when the drawer closes', async () => {
    mockRead.mockResolvedValueOnce(detail())
    await helmPayloads.read(CLUSTER, NAMESPACE, RELEASE, REVISION)

    helmPayloads.forget(KEY)

    // Decision 6 requires a payload is never held after the drawer closes,
    // and that covers the masked half too: masked or not, it is what a read
    // of somebody's release produced.
    expect(helmPayloads.at(KEY).facts).toBeNull()
    expect(helmPayloads.sensitiveAt(KEY)).toBeUndefined()
  })

  it('keeps a refusal on screen and holds no material beside it', async () => {
    mockRead.mockRejectedValueOnce(new Error('[forbidden] you may not read Secrets here'))
    await helmPayloads.read(CLUSTER, NAMESPACE, RELEASE, REVISION)

    expect(helmPayloads.at(KEY).error).toContain('may not read Secrets')
    expect(helmPayloads.sensitiveAt(KEY)).toBeUndefined()

    // A refusal does not take itself off the screen after thirty seconds —
    // an error message that removes itself is one nobody finished reading.
    vi.advanceTimersByTime(REVEAL_HIDE_AFTER_MS * 2)
    expect(helmPayloads.at(KEY).error).not.toBe('')
  })
})

describe('a release payload reaches nothing that persists or exports', () => {
  /**
   * THE ALLOWLIST GUARD, in the shape settingsFile.test.ts uses: populate
   * the forbidden category and assert it does not appear in the artefacts
   * that leave this session.
   *
   * Both destinations are structural rather than accidental — the timeline
   * records WRITES through `writing` in $lib/api/client.ts and this is a
   * read, and the CSV export renders the LISTING, which is built from labels
   * — but "structural" is a claim, and this is what checks it. The Go side
   * has the other half: dto_helm_test.go asserts the payload DTO's field set
   * against a literal list, so a field cannot join it unargued.
   */
  it('is never written into the session timeline', async () => {
    mockRead.mockResolvedValueOnce(detail())
    await helmPayloads.read(CLUSTER, NAMESPACE, RELEASE, REVISION)

    const recorded = JSON.stringify(timeline.forCluster(CLUSTER))

    expect(recorded).not.toContain(SENTINEL)
    expect(recorded).not.toContain('admin password')
    // Nothing about the read is recorded at all — not even as an event with
    // the payload stripped. The timeline is in memory but it is the record a
    // reader scrolls through, and a release name beside "read the payload"
    // would be one more copy of an act the audit log already has.
    expect(timeline.forCluster(CLUSTER)).toHaveLength(0)
  })

  it('is never written back into the listing a CSV export would render', async () => {
    const rendered = listing()

    mockRead.mockResolvedValueOnce(detail())
    await helmPayloads.read(CLUSTER, NAMESPACE, RELEASE, REVISION)

    // The listing the page holds is untouched by the read, so anything built
    // from it — a CSV export, a copied table — cannot carry a payload.
    expect(JSON.stringify(rendered)).not.toContain(SENTINEL)

    const release = rendered.releases![0]
    const csv = toCSV(
      ['Release', 'Namespace', 'Revision', 'Status', 'History'],
      [
        [
          release.name,
          release.namespace,
          String(release.current.revision),
          release.current.status,
          String(release.revisionCount),
        ],
      ],
    )

    expect(csv).not.toContain(SENTINEL)
    // And the export cannot pass by exporting nothing.
    expect(csv).toContain(RELEASE)
  })

  it('does not land on screen when the window blurred while the read was in flight', async () => {
    // THE RACE THE GENERATION GUARD EXISTS FOR. Press Read, alt-tab: the
    // blur handler empties both halves, and then this promise resolves.
    // Without the guard it writes the values and notes straight back —
    // revealed, under a fresh thirty seconds, in exactly the state the blur
    // rule exists to prevent.
    let resolveRead: (value: HelmReleaseDetail) => void = () => {}
    mockRead.mockReturnValueOnce(
      new Promise<HelmReleaseDetail>((resolve) => {
        resolveRead = resolve
      }) as ReturnType<typeof readHelmRelease>,
    )

    const inFlight = helmPayloads.read(CLUSTER, NAMESPACE, RELEASE, REVISION)

    window.dispatchEvent(new Event('blur'))
    resolveRead(detail())
    await inFlight

    expect(helmPayloads.sensitiveAt(KEY)).toBeUndefined()
    expect(helmPayloads.at(KEY).facts).toBeNull()
    expect(JSON.stringify(helmPayloads.at(KEY))).not.toContain(SENTINEL)
  })

  it('does not resurrect a revision the pane has moved away from', async () => {
    // Switching revision mid-read used to leave TWO revisions held at once,
    // the abandoned one's values sitting under a timer no control could
    // reach — Hide targets the key on screen, and that was no longer it.
    let resolveRead: (value: HelmReleaseDetail) => void = () => {}
    mockRead.mockReturnValueOnce(
      new Promise<HelmReleaseDetail>((resolve) => {
        resolveRead = resolve
      }) as ReturnType<typeof readHelmRelease>,
    )

    const inFlight = helmPayloads.read(CLUSTER, NAMESPACE, RELEASE, REVISION)

    // The pane moves to another revision, dropping this one.
    helmPayloads.forget(KEY)

    resolveRead(detail())
    await inFlight

    expect(helmPayloads.sensitiveAt(KEY)).toBeUndefined()
    expect(helmPayloads.at(KEY).facts).toBeNull()
  })

  it('goes when the window loses focus, which is when a screen share starts', async () => {
    mockRead.mockResolvedValueOnce(detail())
    await helmPayloads.read(CLUSTER, NAMESPACE, RELEASE, REVISION)

    window.dispatchEvent(new Event('blur'))

    expect(helmPayloads.sensitiveAt(KEY)).toBeUndefined()
    // BOTH HALVES, unlike the timer. A blur is somebody pointing a camera at
    // the screen rather than a value having sat there too long.
    expect(helmPayloads.at(KEY).facts).toBeNull()
  })
})
