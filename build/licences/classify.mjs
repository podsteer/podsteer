/**
 * Turning licence text and SPDX expressions into a single identifier.
 *
 * The hard part is not recognising MIT. It is that packages describe
 * themselves in four different registers — a declared SPDX field, a file of
 * legal prose, a compound expression, or nothing at all — and each has a
 * different failure mode. Every rule below exists because one of them can
 * otherwise resolve to "probably fine" when it is not.
 */

/**
 * Text signatures, in a deliberate order.
 *
 * Order is load-bearing:
 *   • 0BSD before BSD, because the Zero-Clause text still contains the BSD
 *     redistribution wording.
 *   • BSD-4-Clause before BSD-3-Clause, because the advertising clause is an
 *     addition to the 3-clause text, not a replacement.
 *   • AFFERO before LESSER before GENERAL, because each of those strings is a
 *     substring of the next licence's title.
 */
const SIGNATURES = [
  [/GNU AFFERO GENERAL PUBLIC LICENSE/i, 'AGPL-3.0'],
  [/GNU LESSER GENERAL PUBLIC LICENSE/i, 'LGPL-3.0'],
  [/GNU GENERAL PUBLIC LICENSE/i, 'GPL-3.0'],
  [/Server Side Public License/i, 'SSPL-1.0'],
  [/Business Source License/i, 'BUSL-1.1'],
  [/Elastic License/i, 'Elastic-2.0'],
  [/Mozilla Public License.*2\.0/is, 'MPL-2.0'],
  [/Mozilla Public License.*1\.1/is, 'MPL-1.1'],
  [/Eclipse Public License.*2\.0/is, 'EPL-2.0'],
  [/Eclipse Public License/i, 'EPL-1.0'],
  [/Common Development and Distribution License/i, 'CDDL-1.0'],
  [/Apache License/i, 'Apache-2.0'],
  [/BSD Zero.Clause License|Zero-Clause BSD/i, '0BSD'],
  [/All advertising materials mentioning features/i, 'BSD-4-Clause'],
  [/Permission to use, copy, modify, and\/or distribute this software/i, 'ISC'],
  [/Permission is hereby granted, free of charge/i, 'MIT'],
  [/Redistribution and use in source and binary forms/i, 'BSD-3-Clause'],
  [/DO WHAT THE FUCK YOU WANT TO PUBLIC LICENSE/i, 'WTFPL'],
  [/This is free and unencumbered software released into the public domain/i, 'Unlicense'],
  [/Blue Oak Model License/i, 'BlueOak-1.0.0'],
  [/Creative Commons Legal Code[\s\S]{0,200}CC0/i, 'CC0-1.0'],
  [/SIL OPEN FONT LICENSE/i, 'OFL-1.1'],
]

/** The identifier used when nothing could be established. */
export const UNKNOWN = 'UNKNOWN'

/**
 * Classifies raw licence text.
 *
 * Returns UNKNOWN rather than guessing. A wrong guess here is worse than no
 * guess: UNKNOWN routes to human review, whereas a mistaken "MIT" ships.
 */
export function classifyText(text) {
  if (!text || !text.trim()) return UNKNOWN

  for (const [pattern, identifier] of SIGNATURES) {
    if (pattern.test(text)) return identifier
  }
  return UNKNOWN
}

/**
 * Normalises an SPDX identifier to the base form the policy tiers hold.
 *
 * `GPL-3.0-only` and `GPL-3.0-or-later` are the same tier decision — the
 * difference matters to a licensee choosing a version, not to us deciding
 * whether we may ship it at all.
 */
export function normalise(identifier) {
  if (!identifier) return UNKNOWN
  return identifier.trim().replace(/-(only|or-later)$/i, '')
}

/** Ranks tiers so the best and worst of a set can be elected. */
const TIER_RANK = { allowed: 3, reviewRequired: 2, forbidden: 1 }

/** Returns the more permissive of two tiers. */
function betterTier(left, right) {
  return TIER_RANK[left] >= TIER_RANK[right] ? left : right
}

/** Returns the less permissive of two tiers. */
function worseTier(left, right) {
  return TIER_RANK[left] <= TIER_RANK[right] ? left : right
}

/**
 * Resolves an SPDX expression to the single identifier we rely on.
 *
 * `tierOf` maps a normalised identifier to its tier, or to `undefined` when
 * the policy lists no tier for it. Unlisted always means reviewRequired here —
 * an identifier nobody has classified is precisely the thing a person should
 * look at, and defaulting it to allowed is how an unnoticed licence ships.
 *
 * Returns `{ identifier, tier, expression, arms }`, where `expression` is the
 * original string when it was compound and `arms` lists the identifiers that
 * contribute obligations — one for OR (the elected arm), all of them for AND.
 */
export function resolveExpression(raw, tierOf) {
  /** The tier for an identifier, defaulting unlisted ones to review. */
  const tier = (identifier) => tierOf(identifier) ?? 'reviewRequired'

  const expression = (raw ?? '').trim()
  if (!expression) {
    return { identifier: UNKNOWN, tier: tier(UNKNOWN), expression: '', arms: [UNKNOWN] }
  }

  // Strip the parentheses npm wraps compound expressions in.
  const inner = expression.replace(/^\(([\s\S]*)\)$/, '$1').trim()

  // WITH is never decomposed. "Apache-2.0 WITH LLVM-exception" is its own
  // licence: the exception can only widen or narrow the grant, and stripping
  // it to reuse Apache-2.0's tier would be deciding a question nobody asked.
  if (/\sWITH\s/i.test(inner)) {
    // Only an exact, verbatim tier entry clears a WITH expression; otherwise
    // it falls to review.
    return { identifier: inner, tier: tier(inner), expression, arms: [inner] }
  }

  // AND: every arm's obligations apply, so the strictest tier governs.
  if (/\sAND\s/i.test(inner)) {
    const arms = inner.split(/\s+AND\s+/i).map((arm) => normalise(arm))
    const strictest = arms.map(tier).reduce(worseTier, 'allowed')
    return { identifier: arms.join(' AND '), tier: strictest, expression, arms }
  }

  // OR: we may pick, so we pick the arm that costs us least.
  if (/\sOR\s/i.test(inner)) {
    const arms = inner.split(/\s+OR\s+/i).map((arm) => normalise(arm))
    let elected = arms[0]
    let electedTier = tier(elected)

    for (const arm of arms.slice(1)) {
      const armTier = tier(arm)
      if (betterTier(armTier, electedTier) === armTier && armTier !== electedTier) {
        elected = arm
        electedTier = armTier
      }
    }
    return { identifier: elected, tier: electedTier, expression, arms: [elected] }
  }

  const identifier = normalise(inner)
  return { identifier, tier: tier(identifier), expression: '', arms: [identifier] }
}

/**
 * Reconciles what a package SAYS it is with what its licence file reads as.
 *
 * The disagreement case is the one that matters. A package declaring MIT while
 * shipping GPL text is either mislabelled or relicensed, and both are
 * questions for a person — so it resolves to UNKNOWN, which routes to review
 * rather than quietly trusting the friendlier of the two.
 */
export function reconcile({ declared, text }) {
  const detected = classifyText(text)

  if (!declared) {
    return { identifier: detected, source: text ? 'text' : 'none' }
  }

  const normalised = normalise(declared)

  // A compound declaration is resolved by the caller against the tiers; text
  // matching cannot confirm or deny it, so the declaration stands.
  if (/\s(OR|AND|WITH)\s/i.test(declared)) {
    return { identifier: declared, source: 'declared' }
  }

  if (detected === UNKNOWN) {
    return { identifier: normalised, source: 'declared' }
  }
  if (detected === normalised) {
    return { identifier: normalised, source: 'agreed' }
  }
  return {
    identifier: UNKNOWN,
    source: 'conflict',
    conflict: { declared: normalised, detected },
  }
}
