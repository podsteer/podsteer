/**
 * Shaping for the Helm page: which of three states it is in, and what its
 * empty state is allowed to say.
 *
 * EVERYTHING HERE IS PRESENTATION. Nothing decides what a release is, which
 * revision is current, or how revisions group — that is
 * `domain.GroupHelmRevisions` in Go, where it has a test per rule, and it
 * arrives already grouped and sorted. What is left is choosing a sentence,
 * which is the quotation side of the line CLAUDE.md draws.
 *
 * THE ONE RULE WORTH STATING TWICE: "no Helm here" and "not permitted here"
 * are different sentences and must never collapse into each other. `list
 * secrets` is exactly the permission this page's likeliest readers do not
 * hold — the Secrets doctrine exists because many engineers deliberately have
 * no Secret access — so a refused listing is the ORDINARY case for a real
 * part of the audience, and rendering it as an empty cluster would send
 * somebody to look for a Helm install that is sitting right there. See
 * decision 6 in podsteer/business-docs, which states this as a wording
 * requirement rather than as advice.
 */

import type { HelmListing, ResourceKind } from './api/client'

/**
 * Whether the listing has an answer in it, and what to say when it does not.
 *
 * THREE VALUES, AND THERE IS DELIBERATELY NO "ABSENT". A cluster with no Helm
 * releases is `listed` with zero rows — a real, successful answer about a real
 * cluster, and the common case on a GitOps estate. A fourth value meaning
 * "there was nothing here" would give the zero-row case somewhere else to go
 * and would stop it being distinguishable from a refusal, which is the one
 * collapse this feature is not allowed to make. Compare `ReviewStatus` in
 * `$lib/rbac`, which DOES carry an absent, because "this cluster serves no
 * review API" is a fact about the API server rather than about its contents.
 */
export type HelmStatus = 'listed' | 'forbidden' | 'failed'

/** What the page should render: its rows, or one sentence instead. */
export type HelmState =
  | { kind: 'listed' }
  | { kind: 'unavailable'; status: Exclude<HelmStatus, 'listed'>; tone: HelmTone; message: string; retryable: boolean }

/**
 * How an unavailable page should read.
 *
 * A refusal is NOT a fault — an account configured exactly as its owner
 * intended cannot list Secrets — so it is drawn as information, the way an
 * unreadable count is, and never as an error with a retry that will refuse
 * identically. Only a transient failure is drawn as an error worth retrying.
 */
export type HelmTone = 'info' | 'error'

/**
 * Reads a listing's status and its sentence into what the page should do.
 *
 * The sentence itself comes from Go, because a refusal names the permission
 * that would fix it and that is a fact about the cluster's API rather than
 * about this screen. An empty one falls back rather than rendering a blank
 * pane, which is the one outcome worse than any of the three.
 *
 * AN UNRECOGNISED STATUS BECOMES `failed`, never `listed`. Reading a status
 * this build has not seen as a successful listing would render zero rows and
 * claim a cluster holds no releases on the strength of a word nothing here
 * understands.
 */
export function helmState(status: string, refusal: string): HelmState {
  if (status === 'listed' || status === '') return { kind: 'listed' }

  const resolved: Exclude<HelmStatus, 'listed'> = status === 'forbidden' ? 'forbidden' : 'failed'

  return {
    kind: 'unavailable',
    status: resolved,
    tone: resolved === 'failed' ? 'error' : 'info',
    message: refusal || fallbackMessage(resolved),
    // A refusal is not retryable: pressing Refresh asks the same account the
    // same question and is refused identically, while adding one more denied
    // request to somebody's audit log.
    retryable: resolved === 'failed',
  }
}

/** The sentence for a status that arrived without one. */
function fallbackMessage(status: Exclude<HelmStatus, 'listed'>): string {
  switch (status) {
    case 'forbidden':
      return 'Your account may not list Secrets here, and Helm stores every release in one. This says nothing about whether Helm is used in this cluster.'
    default:
      return 'The release list could not be read. The cluster may be unreachable; try again.'
  }
}

/**
 * Whether the zero-row copy may be shown at all.
 *
 * A guard rather than a convenience: the zero-row copy asserts something
 * about the CLUSTER ("Helm has installed nothing here"), and only a listed
 * status has established that. Every caller goes through this rather than
 * testing `releases.length === 0` on its own, because a refused listing also
 * carries zero releases and the two would be indistinguishable at the call
 * site.
 */
export function showsEmptyCopy(listing: HelmListing | null): boolean {
  if (!listing) return false
  return helmState(listing.status, listing.refusal).kind === 'listed' && (listing.releases?.length ?? 0) === 0
}

/** Argo CD's API group, as its CRDs declare it. */
const ARGO_GROUP = 'argoproj.io'

/**
 * The sentence shown beside an empty listing on a cluster running Argo CD.
 *
 * Argo CD renders charts with `helm template` and APPLIES the result: there
 * is no Helm release and no release Secret, so an empty listing on such a
 * cluster is correct and complete rather than a gap. Saying so turns a
 * confusing nothing into an explanation.
 *
 * Flux is deliberately the opposite case and gets no sentence:
 * helm-controller uses Helm's OWN storage, so a HelmRelease's release Secret
 * is there and listable.
 */
export const ARGO_NOTE =
  'Argo CD is installed in this cluster. It renders charts with `helm template` and applies the result, ' +
  'storing no Helm release — so workloads it manages will not appear here.'

/**
 * Whether the cluster serves Argo CD's Application kind.
 *
 * READ FROM THE DISCOVERED KINDS THE SESSION ALREADY HOLDS, and that is the
 * load-bearing part. `gitops.ts` detects Argo provenance from
 * `argocd.argoproj.io/tracking-id`, which is an ANNOTATION on one object's
 * manifest — and annotations do not ride list rows unless somebody has put
 * them on a column (see the projection rules in CLAUDE.md), so that signal
 * simply is not available here and reaching for it would mean a read per row.
 * The presence of `argoproj.io/Application` among the kinds discovery already
 * returned costs nothing at all.
 *
 * IT IS A FACT ABOUT THE CLUSTER, NOT A CLAIM ABOUT THE WORKLOADS. "Argo CD
 * is installed here" is exactly what the kind list establishes; "these
 * workloads are managed by Argo CD" is not, and the sentence is worded to
 * claim only the former.
 *
 * Matched on GROUP AND KIND together, the same rule `gitops/panel.ts` and
 * `operators/panel.ts` follow: `Application` is a kind in three API groups,
 * and half a coordinate matches the wrong one.
 */
export function servesArgoApplications(kinds: readonly ResourceKind[]): boolean {
  return kinds.some((kind) => kind.group === ARGO_GROUP && kind.kind === 'Application')
}

/**
 * "3 revisions" — what a release row says about its history.
 *
 * Every revision is its own Secret, so this is also how many objects the
 * listing read for that release. One is stated rather than hidden: a release
 * installed once and never upgraded is worth seeing as such.
 */
export function countedRevisions(count: number): string {
  return count === 1 ? '1 revision' : `${count} revisions`
}

/**
 * A Helm timestamp label as a Date, or null when there was none.
 *
 * Helm writes `createdAt` and `modifiedAt` as Unix SECONDS, and Go carries
 * them through as seconds. Zero means the label was absent — which is the
 * ordinary case for `modifiedAt`, since Helm adds it only on an update — and
 * becomes null here rather than 1970, because a date nobody wrote must not
 * render as one somebody did.
 */
export function helmTime(seconds: number): Date | null {
  if (!seconds || seconds <= 0) return null
  return new Date(seconds * 1000)
}
