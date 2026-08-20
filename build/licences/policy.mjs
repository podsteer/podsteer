/**
 * Applying the licence policy to what the collector found.
 *
 * The policy itself is data (`build/licence-policy.json`) and the reasoning
 * behind it is prose (`docs/LICENCE-POLICY.md`). This module is only the
 * arbiter: it decides which packages violate the policy, which exceptions
 * cover them, and — the part that makes the exceptions trustworthy — whether
 * an exception's premise still holds.
 */

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { resolveExpression } from './classify.mjs'

/** Loads the policy document. */
export function loadPolicy(repoRoot) {
  return JSON.parse(readFileSync(join(repoRoot, 'build', 'licence-policy.json'), 'utf8'))
}

/**
 * Builds the tier lookup for a scope.
 *
 * Returns `undefined` for identifiers the policy does not list, which the
 * resolver treats as review-required. That default is the whole safety
 * property: a licence nobody has classified must stop a build, not pass one.
 */
function tierLookup(policy, scope) {
  const tiers = new Map()
  for (const [tier, identifiers] of Object.entries(policy.tiers)) {
    for (const identifier of identifiers) tiers.set(identifier, tier)
  }

  return (identifier) => {
    // Build-time tooling is never distributed, so a few licences that would
    // be unacceptable in the binary are merely worth knowing about here.
    if (scope === 'build' && policy.buildOverrides?.[identifier]) {
      return policy.buildOverrides[identifier]
    }
    return tiers.get(identifier)
  }
}

/** Matches a package name against an exception pattern (one trailing `*`). */
function matches(pattern, name) {
  if (pattern.endsWith('*')) return name.startsWith(pattern.slice(0, -1))
  return pattern === name
}

/** Finds the exception covering a package, if any. */
function exceptionFor(policy, entry) {
  return (policy.exceptions ?? []).find(
    (exception) =>
      matches(exception.package, entry.name) &&
      exception.ecosystem === entry.ecosystem &&
      exception.licence === entry.identifier &&
      // A build-scope exception never excuses a shipped package. That is the
      // invariant the whole build/shipped split rests on.
      exception.scope === entry.scope,
  )
}

/**
 * Evaluates every collected package against the policy.
 *
 * Returns violations (nothing permits them), the exceptions actually used,
 * exceptions that no longer match anything, and exceptions past review.
 */
export function evaluate(policy, collected, now = new Date()) {
  const violations = []
  const used = new Set()
  const resolved = []

  for (const entry of collected.packages) {
    const tierOf = tierLookup(policy, entry.scope)
    const decision = resolveExpression(entry.declared || entry.identifier, tierOf)

    const record = { ...entry, identifier: decision.identifier, expression: decision.expression }
    resolved.push(record)

    if (decision.tier === 'allowed') continue

    const exception = exceptionFor(policy, record)
    if (exception && decision.tier !== 'forbidden') {
      used.add(exception)
      continue
    }
    // Nothing excuses a forbidden licence in shipped scope — not even an
    // exception. If that ever needs to change, it needs to change here, in
    // review, rather than by adding a line to a data file.
    if (exception && decision.tier === 'forbidden' && entry.scope === 'build') {
      used.add(exception)
      continue
    }

    violations.push({
      name: entry.name,
      version: entry.version,
      ecosystem: entry.ecosystem,
      scope: entry.scope,
      licence: decision.identifier,
      tier: decision.tier,
      conflict: entry.conflict,
    })
  }

  const stale = (policy.exceptions ?? []).filter((exception) => !used.has(exception))

  const expired = (policy.exceptions ?? []).filter(
    (exception) => exception.reviewBy && new Date(exception.reviewBy) < now,
  )

  // The cross-check that makes a build-scope exception honest: if the package
  // it excuses has since entered the shipped tree, its justification — "this
  // never reaches a user's machine" — is no longer true.
  const brokenPremise = (policy.exceptions ?? [])
    .filter((exception) => exception.scope === 'build')
    .filter((exception) =>
      collected.shipped.some(
        (entry) => matches(exception.package, entry.name) && exception.ecosystem === entry.ecosystem,
      ),
    )

  return { violations, resolved, stale, expired, brokenPremise }
}

/** Formats the outcome for a terminal, returning the lines to print. */
export function report(collected, outcome) {
  const lines = []

  const counts = new Map()
  for (const entry of outcome.resolved.filter((record) => record.scope === 'shipped')) {
    counts.set(entry.identifier, (counts.get(entry.identifier) ?? 0) + 1)
  }

  lines.push(`${collected.shipped.length} shipped dependencies:`)
  for (const [identifier, count] of [...counts].sort((left, right) => right[1] - left[1])) {
    lines.push(`  ${String(count).padStart(3)}  ${identifier}`)
  }
  lines.push(`${collected.build.length} build-only dependencies (not distributed)`)
  if (collected.unfetchedGo.length > 0) {
    lines.push(
      `${collected.unfetchedGo.length} Go modules in the graph but never fetched (nothing builds with them)`,
    )
  }

  const platforms = Object.entries(collected.perPlatform)
    .map(([platform, count]) => `${platform}:${count}`)
    .join('  ')
  lines.push(`Go modules per platform — ${platforms}`)

  return lines
}
