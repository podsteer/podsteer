/**
 * One revision of one Helm release, read because somebody clicked.
 *
 * THIS IS THE ONLY PLACE IN THE WEBVIEW THAT HOLDS A RELEASE PAYLOAD, and
 * everything about it follows from what a payload IS: the decoded contents of
 * a Secret. The release LIST is a different act entirely — built from the
 * labels Helm puts on each release Secret, transferring no Secret contents at
 * all — and this store deliberately knows nothing about it (see
 * `$lib/helm.ts` and `HelmView.svelte`).
 *
 * THE SHAPE IS `secretReveals`', not a gentler version of it. Decision 6
 * permitted the payload read on the condition that it inherit ADR 3's
 * controls verbatim rather than a summary of them, so: it happens because
 * somebody pressed something, never on render and never on a tick; it is one
 * revision at a time; it is re-hideable; it expires after thirty seconds; and
 * it goes on window blur. Those last three are `RevealHolder`'s, shared with
 * `secretReveals` so there is one implementation and one set of tests.
 *
 * TWO HALVES OF ONE READ, AND THEY ARE NOT ALIKE — which is the one thing
 * here worth reading carefully.
 *
 *   - The CHART IDENTITY and the MASKED MANIFEST are held for as long as the
 *     drawer is open, with no timer. The chart's name and versions are not
 *     secret at all — they are the two columns the list could not afford —
 *     and the manifest arrives with every Secret document inside it already
 *     masked in the Go adapter, so there is nothing left in it to time out.
 *   - The VALUES and the NOTES are held in the reveal holder and expire.
 *     Values are where a chart puts a database password and PodSteer cannot
 *     know which key that is. Notes are the half people assume is harmless:
 *     a NOTES template is rendered from those same values, and the commonest
 *     thing a chart's notes do is print how to fetch the admin password —
 *     several print it inline.
 *
 * When the timer fires or the window blurs, the values and notes are GONE
 * from this process, not merely unrendered, and showing them again costs
 * another deliberate read with another audit line. That is the point.
 *
 * NOTHING IS RECORDED AND NOTHING REACHES DISK. No timeline entry, no CSV
 * column, no preference — see helmPayloads.test.ts, which asserts the first
 * two rather than trusting them. The session timeline records WRITES (through
 * `writing` in $lib/api/client.ts) and this is a read, so structurally
 * nothing here can reach it; the guard test exists because "structurally"
 * is a claim worth checking.
 */

import { readHelmRelease } from '$lib/api/client'
import type { HelmReleaseDetail } from '$lib/api/client'
import { toApiError } from '$lib/api/errors'
import { hideOnWindowBlur, RevealHolder } from './revealHolder.svelte'

/**
 * The half of a payload that is not under a timer: what the release is, plus
 * the manifest that arrived already masked.
 *
 * `Omit` rather than a hand-written list, because the fields it excludes are
 * exactly the two the holder owns and spelling the rest out again would let
 * a new payload field arrive here silently. The Go side's own allowlist test
 * (`dto_helm_test.go`) is what governs whether a new field may exist at all.
 */
export type HelmReleaseFacts = Omit<HelmReleaseDetail, 'values' | 'notes'>

/** The half that expires. */
export interface HelmReleaseSensitive {
  values: string
  notes: string
}

/** What a pane renders for one revision. */
export interface HelmPayloadState {
  facts: HelmReleaseFacts | null
  error: string
  loading: boolean
}

const EMPTY: HelmPayloadState = { facts: null, error: '', loading: false }

/** The key one revision is held under. Namespace included, because a release
    name is only unique within one. */
export function helmPayloadKey(
  clusterId: string,
  namespace: string,
  release: string,
  revision: number,
): string {
  return `${clusterId}/${namespace}/${release}/${revision}`
}

class HelmPayloads {
  /** The non-secret half, held while the drawer is open. */
  #facts = $state<Record<string, HelmPayloadState>>({})

  /**
   * The half that expires. A holder rather than a field, so the timing, the
   * re-hide and the blur are `secretReveals`' own and cannot drift from it.
   */
  #sensitive = new RevealHolder<HelmReleaseSensitive>()

  /** What the pane should render for one revision. Never undefined. */
  at(key: string): HelmPayloadState {
    return this.#facts[key] ?? EMPTY
  }

  /** The values and notes, while they are on screen. Undefined once they are
      not — which is a real state and the pane says so rather than showing an
      empty tab. */
  sensitiveAt(key: string): HelmReleaseSensitive | undefined {
    return this.#sensitive.at(key)
  }

  /** Whether the values and notes are on screen right now. */
  isRevealed(key: string): boolean {
    return this.#sensitive.has(key)
  }

  /**
   * Reads ONE revision of ONE release.
   *
   * ONLY EVER FROM A CLICK HANDLER. Nothing may call this from an `$effect`,
   * from a poll, or when a drawer opens: that would turn opening a pane into
   * a Secret read, which is the pattern Kubernetes' own good-practices page
   * tells cluster operators to alert on and the exact thing this feature was
   * permitted on the condition of not doing. The list beside it costs no
   * Secret contents at all and is what a page load is allowed to make.
   *
   * Pressing it again on a revision whose values have expired is a second
   * deliberate act and makes a second read, with a second audit line. That
   * is deliberate rather than wasteful — a cached payload would be Secret
   * material sitting in a process after somebody stopped looking at it.
   */
  read = async (
    clusterId: string,
    namespace: string,
    release: string,
    revision: number,
  ): Promise<void> => {
    const key = helmPayloadKey(clusterId, namespace, release, revision)

    // TAKEN BEFORE THE AWAIT, CHECKED AFTER IT, and it closes two real bugs
    // rather than one.
    //
    // The first is the blur race: press Read, alt-tab, the blur handler
    // empties both halves — and then this promise resolves and writes the
    // values and notes straight back, revealed and under a fresh thirty
    // seconds, in a window nobody is looking at. Emptying is not enough on
    // its own, because the read was already in the air when it happened.
    //
    // The second is switching revision mid-read: the pane forgets the old
    // key and moves on, the old read resolves and repopulates it, and two
    // revisions are held at once — the abandoned one's values sitting under
    // a timer no control can reach, because Hide targets the key on screen.
    // The token is per KEY, so forgetting one invalidates exactly its own
    // read and leaves any other alone.
    //
    // It is the same guard `HelmView.load` puts on the listing, and the same
    // one `secretReveals.reveal` now puts on a key.
    const issued = this.#sensitive.claim(key)
    this.#facts[key] = { facts: null, error: '', loading: true }

    try {
      // Assigned only after the call resolves — the same rule
      // `secretReveals` follows, and for the same reason: rendering
      // optimistically is how a client shows a value to somebody who was not
      // allowed to read it, for the moment before the error lands.
      const detail = await readHelmRelease(clusterId, namespace, release, revision)
      if (!this.#sensitive.isCurrent(key, issued)) return

      const { values, notes, ...facts } = detail
      this.#facts[key] = { facts, error: '', loading: false }
      // EXPIRING. This is the material.
      this.#sensitive.put(key, { values, notes }, true)
    } catch (cause) {
      // A refusal about a read somebody has navigated away from, or that a
      // blur has already answered, is dropped rather than shown.
      if (!this.#sensitive.isCurrent(key, issued)) return
      this.#facts[key] = { facts: null, error: toApiError(cause).message, loading: false }
      this.#sensitive.hide(key)
    }
  }

  /**
   * Puts the values and notes away, leaving the chart identity and the masked
   * manifest on screen.
   *
   * The re-hide half of the discipline: what was revealed can always be
   * un-revealed, which is the control Freelens does not offer at all.
   */
  hide = (key: string): void => {
    this.#sensitive.hide(key)
  }

  /**
   * Drops everything about one revision, material and facts alike.
   *
   * Called when the drawer closes. Decision 6 requires that a payload is
   * "never held after the drawer closes", and holding the masked manifest
   * for a pane nobody is looking at would be holding a decoded Secret for no
   * reason — masked or not, it is what a read of somebody's release produced.
   */
  forget = (key: string): void => {
    this.#sensitive.hide(key)
    delete this.#facts[key]
  }

  /** Drops everything. Called on window blur and when the page unmounts. */
  forgetAll = (): void => {
    this.#sensitive.hideAll()
    this.#facts = {}
  }

  /**
   * The blur handler. It empties BOTH halves, unlike the timer, which takes
   * only the material: a blur is somebody pointing a camera at the screen,
   * and the honest response is that the pane is empty when they come back.
   */
  hideAll = (): void => {
    this.forgetAll()
  }
}

export const helmPayloads = new HelmPayloads()

hideOnWindowBlur(helmPayloads)
