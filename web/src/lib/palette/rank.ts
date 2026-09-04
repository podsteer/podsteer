/**
 * A fast fuzzy scorer for the command palette.
 *
 * Every candidate — a kind's title, a namespace name, a cluster id, a
 * command's title — is scored against whatever the operator typed as a
 * case-insensitive SUBSEQUENCE match: every character of the query has to
 * appear in the candidate's text, in order, but not necessarily adjacent.
 * That is what lets "dp" find "Deployments" and "kube-sys" find the
 * "kube-system" namespace without either being typed in full.
 *
 * A bare subsequence match ranks "Pod" and "PodDisruptionBudget" as equally
 * good matches of "pod", which is not how anybody experiences the two, so
 * three bonuses on top of the raw match decide the order:
 *
 *   - EXACT: the candidate and the query are the same string.
 *   - PREFIX: the candidate starts with the query outright.
 *   - WORD BOUNDARY: a matched character sits at the START of a word —
 *     after a space, hyphen, underscore or slash, or at a camelCase
 *     transition — rather than in the middle of one.
 *
 * A CONSECUTIVE run of matched characters is worth more than the same
 * characters scattered across the candidate, because a contiguous match is
 * what a query typed as a fragment of the real word produces.
 *
 * No dependency on anything Svelte or PodSteer-specific: this is arguable on
 * its own, in a table-driven test, the same reason $lib/query stays a plain
 * module.
 */

/** One candidate's match against a query, or the shape a caller compares. */
export interface FuzzyMatch {
  /** Higher is a better match. Only meaningful relative to other matches of
      the SAME query — the number carries no meaning on its own. */
  score: number
  /** Indices into the matched string the query's characters landed on, for
      a caller that wants to highlight them. Empty for an empty query. */
  indices: number[]
}

const EXACT_BONUS = 24
const PREFIX_BONUS = 12
const WORD_BOUNDARY_BONUS = 6
const CONSECUTIVE_BONUS = 4

/** Whether `target[index]` starts a "word" within `target` — the beginning
    of the string, right after a separator, or a camelCase transition
    (`ConfigMap`'s `M`, `HorizontalPodAutoscaler`'s `P` and `A`). */
function isWordBoundary(target: string, index: number): boolean {
  if (index === 0) return true
  const previous = target[index - 1]
  const current = target[index]
  if (/[\s\-_/.]/.test(previous)) return true
  return /[a-z0-9]/.test(previous) && /[A-Z]/.test(current)
}

/**
 * Scores `target` against `query` as a case-insensitive subsequence match.
 *
 * Returns null when `query` is not a subsequence of `target` at all — the
 * caller's job is to drop that candidate, not to render a zero-scored one.
 * An empty query matches everything with a score of 0, so a blank palette
 * input shows every candidate in whatever order the caller's own tie-break
 * (see `rank` below) puts them, rather than nothing at all.
 */
export function fuzzyMatch(query: string, target: string): FuzzyMatch | null {
  if (query.length === 0) return { score: 0, indices: [] }
  if (target.length === 0) return null

  const q = query.toLowerCase()
  const t = target.toLowerCase()

  const indices: number[] = []
  let score = 0
  let qi = 0
  let lastMatched = -1

  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] !== q[qi]) continue

    let points = 1
    if (isWordBoundary(target, ti)) points += WORD_BOUNDARY_BONUS
    if (lastMatched !== -1 && ti === lastMatched + 1) points += CONSECUTIVE_BONUS

    score += points
    indices.push(ti)
    lastMatched = ti
    qi++
  }

  // Not every query character was found, in order — this is not a match at
  // all, never a low-scoring one.
  if (qi < q.length) return null

  if (t === q) score += EXACT_BONUS
  else if (t.startsWith(q)) score += PREFIX_BONUS

  return { score, indices }
}

/** What `rank` needs from a candidate. Extra fields ride along on the
    ranked result, since `rank` returns the candidates themselves. */
export interface RankCandidate {
  /** What the query is matched against. */
  label: string
  /** Extra text matched but never shown or highlighted — a kind's API
      group, a command's synonyms. The BEST of `label` and every keyword's
      score wins; a candidate is included if either matches. */
  keywords?: string[]
  /** Breaks a score tie: higher sorts first. Omit for "no particular
      recency" — every candidate without one ties at the bottom of the
      recency comparison and falls through to the alphabetical one. */
  recency?: number
}

/**
 * Ranks a list of candidates against a query, best match first.
 *
 * A candidate whose `label` is not a match is still included if one of its
 * `keywords` is — "networking" finding an Ingress kind whose title is just
 * "Ingresses", say — scored by whichever of the two matched best.
 *
 * Ties, including every candidate when `query` is empty, are broken by
 * recency (higher first) and then alphabetically by label, so a palette
 * opened with nothing typed shows something predictable rather than
 * whatever order the caller happened to build the list in.
 */
export function rank<T extends RankCandidate>(query: string, candidates: T[]): T[] {
  const scored: { candidate: T; match: FuzzyMatch }[] = []

  for (const candidate of candidates) {
    const attempts = [candidate.label, ...(candidate.keywords ?? [])]
      .map((text) => fuzzyMatch(query, text))
      .filter((match): match is FuzzyMatch => match !== null)

    if (attempts.length === 0) continue

    const best = attempts.reduce((a, b) => (b.score > a.score ? b : a))
    scored.push({ candidate, match: best })
  }

  scored.sort((a, b) => {
    if (a.match.score !== b.match.score) return b.match.score - a.match.score
    const recencyA = a.candidate.recency ?? 0
    const recencyB = b.candidate.recency ?? 0
    if (recencyA !== recencyB) return recencyB - recencyA
    return a.candidate.label.localeCompare(b.candidate.label)
  })

  return scored.map((entry) => entry.candidate)
}
