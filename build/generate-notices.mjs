#!/usr/bin/env node
/**
 * Generates the third-party notices K8Sense ships, and enforces the licence
 * policy while it is there.
 *
 * Two jobs, deliberately in one command. The inventory and the policy check
 * read exactly the same dependency set, so neither can drift from the other —
 * a policy that passed against a different set of packages than the one we
 * credit would be worth nothing.
 *
 * Scope is what SHIPS: Go modules linked into the binary on any of the three
 * release platforms, and npm packages in the runtime dependency tree. Build
 * tooling is collected too, but only to be policy-checked; it is never written
 * into the inventory, because nothing obliges you to credit a compiler you did
 * not distribute.
 *
 * Run via `make notices`. The output is committed so a build needs neither the
 * module cache nor node_modules to produce a compliant artefact, and CI fails
 * if it is out of date.
 *
 * See docs/LICENCE-POLICY.md for what the tiers mean and why.
 */

import { createHash } from 'node:crypto'
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'

import { collect, repoRoot } from './licences/collect.mjs'
import { evaluate, loadPolicy, report } from './licences/policy.mjs'

const root = repoRoot()
const outputPath = join(root, 'app', 'adapters', 'notices', 'notices.json')

let collected
try {
  collected = collect(root)
} catch (cause) {
  console.error(`Cannot determine the dependency set: ${cause.message}`)
  process.exit(1)
}

const policy = loadPolicy(root)
const outcome = evaluate(policy, collected)

for (const line of report(collected, outcome)) console.log(line)

// --- the shipped inventory -------------------------------------------------
//
// Licence texts are deduplicated by content: the MIT text repeats identically
// across dozens of packages, and storing it once is the difference between an
// embedded file of tens of kilobytes and one of several megabytes.

const texts = {}

/** Interns a text, returning its id, or "" when there is nothing to intern. */
function intern(text) {
  if (!text) return ''
  const id = createHash('sha256').update(text).digest('hex').slice(0, 16)
  texts[id] = text
  return id
}

const shipped = outcome.resolved
  .filter((entry) => entry.scope === 'shipped')
  .map((entry) => {
    const record = {
      name: entry.name,
      version: entry.version,
      ecosystem: entry.ecosystem,
      licence: entry.identifier,
      copyright: entry.copyright,
      textId: intern(entry.text),
    }
    // Only present when there is one, so the committed file stays readable.
    if (entry.expression) record.expression = entry.expression
    if (entry.noticeText) record.noticeTextId = intern(entry.noticeText)
    return record
  })

// No timestamp and no absolute paths: this file is drift-checked in CI, so
// anything that changes between two identical runs would fail every build.
writeFileSync(
  outputPath,
  `${JSON.stringify(
    {
      generated: 'by build/generate-notices.mjs — run `make notices` after changing dependencies',
      packages: shipped,
      texts,
    },
    null,
    1,
  )}\n`,
)

// --- policy enforcement ----------------------------------------------------

let failed = false

/** Reports a failure and marks the run as failed. */
function fail(heading, lines) {
  failed = true
  console.error(`\n${heading}`)
  for (const line of lines) console.error(`  ${line}`)
}

if (collected.mislabelled.length > 0) {
  fail('Packages imported by shipped source are declared as devDependencies:', [
    ...collected.mislabelled.map((name) => name),
    '',
    'They are bundled into the application, so they ship — move them to',
    '"dependencies" in web/package.json. Left as they are, they would be',
    'absent from the notices above and `npm ci --omit=dev` would not install',
    'them.',
  ])
}

if (collected.unresolved.length > 0) {
  fail('Packages imported by shipped source but not installed at all:', collected.unresolved)
}

if (outcome.violations.length > 0) {
  fail('Licence policy violations:', [
    ...outcome.violations.map((entry) => {
      const where = entry.scope === 'shipped' ? 'SHIPPED' : 'build'
      const detail = entry.conflict
        ? ` (declares ${entry.conflict.declared}, text reads as ${entry.conflict.detected})`
        : ''
      return `[${where}] ${entry.ecosystem}:${entry.name}@${entry.version} — ${entry.licence} (${entry.tier})${detail}`
    }),
    '',
    'Either remove the dependency, or record an exception in',
    'build/licence-policy.json with a justification. See docs/LICENCE-POLICY.md.',
  ])
}

if (outcome.brokenPremise.length > 0) {
  fail('Build-only exceptions whose premise no longer holds:', [
    ...outcome.brokenPremise.map(
      (exception) => `${exception.ecosystem}:${exception.package} — ${exception.licence}`,
    ),
    '',
    'These were excused on the grounds that they are never distributed, and',
    'they are now in the shipped dependency tree. The exception must be',
    're-argued for shipped scope, or the dependency removed.',
  ])
}

if (outcome.stale.length > 0) {
  fail('Exceptions that match no current dependency:', [
    ...outcome.stale.map(
      (exception) => `${exception.ecosystem}:${exception.package} — ${exception.licence}`,
    ),
    '',
    'Delete them. An exception nobody needs is an exception nobody reviews.',
  ])
}

// An expired review is a warning locally — it should not stop somebody
// building — but it stops a release, which is the point at which "we will look
// at that later" has run out.
if (outcome.expired.length > 0) {
  const lines = outcome.expired.map(
    (exception) => `${exception.ecosystem}:${exception.package} — review due ${exception.reviewBy}`,
  )
  if (process.env.GITHUB_ACTIONS === 'true') {
    fail('Licence exceptions are past their review date:', lines)
  } else {
    console.warn('\nLicence exceptions are past their review date:')
    for (const line of lines) console.warn(`  ${line}`)
  }
}

if (failed) process.exit(1)

console.log(`\nPolicy satisfied. Wrote ${shipped.length} packages to app/adapters/notices/notices.json.`)
