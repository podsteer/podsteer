#!/usr/bin/env node
/**
 * Emits a CycloneDX 1.6 software bill of materials for K8Sense.
 *
 * An SBOM is what a customer's procurement or security team asks for before a
 * desktop binary is allowed onto their estate, and it is becoming a
 * requirement rather than a courtesy — the EU Cyber Resilience Act and US
 * Executive Order 14028 both push in that direction. Publishing one with each
 * release costs nothing here and removes a question from every future
 * enterprise conversation.
 *
 * Written by hand rather than delegated to a scanner, for one reason that
 * matters: it shares `licences/collect.mjs` with the notices generator, so the
 * SBOM and the Credits pane describe **provably the same set of packages**. A
 * third-party scanner would re-derive the dependency set with its own rules —
 * in particular it would not know that this project's shipped npm scope is
 * cross-checked against what the frontend actually imports — and two documents
 * that disagree about what ships are worse than one.
 *
 * Run via `make sbom`. Not committed: the output carries a timestamp, and it
 * is a release artefact rather than source.
 */

import { execFileSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

import { collect, repoRoot } from './licences/collect.mjs'
import { evaluate, loadPolicy } from './licences/policy.mjs'

const root = repoRoot()
const outputDir = join(root, 'build', 'bin', 'sbom')
const outputPath = join(outputDir, 'k8sense.cdx.json')

/** The version being described: the release tag in CI, else the git state. */
function version() {
  if (process.env.GITHUB_REF_NAME) return process.env.GITHUB_REF_NAME
  try {
    return execFileSync('git', ['describe', '--tags', '--always'], {
      cwd: root,
      encoding: 'utf8',
    }).trim()
  } catch {
    // A source tree with no git history still deserves an SBOM.
    return 'dev'
  }
}

/**
 * Builds a package URL.
 *
 * The `@` of a scoped npm package has to be percent-encoded or the purl parses
 * with the scope as its version — the single most common way a purl ends up
 * silently unmatched by a vulnerability scanner.
 */
function purl(entry) {
  if (entry.ecosystem === 'go') {
    return `pkg:golang/${entry.name}@${entry.version}`
  }
  const name = entry.name.startsWith('@') ? `%40${entry.name.slice(1)}` : entry.name
  return `pkg:npm/${name}@${entry.version}`
}

/** Renders one shipped package as a CycloneDX component. */
function component(entry) {
  const record = {
    type: 'library',
    'bom-ref': purl(entry),
    name: entry.name,
    version: entry.version,
    purl: purl(entry),
    scope: 'required',
  }

  // CycloneDX distinguishes a known SPDX id from free text; using `id` for
  // something unrecognised makes the document invalid rather than vague.
  if (entry.identifier && entry.identifier !== 'UNKNOWN') {
    record.licenses = entry.expression
      ? [{ expression: entry.expression }]
      : [{ license: { id: entry.identifier } }]
  }

  if (entry.copyright) record.copyright = entry.copyright
  return record
}

let collected
try {
  collected = collect(root)
} catch (cause) {
  console.error(`Cannot determine the dependency set: ${cause.message}`)
  process.exit(1)
}

const outcome = evaluate(loadPolicy(root), collected)
const shipped = outcome.resolved.filter((entry) => entry.scope === 'shipped')

const bom = {
  bomFormat: 'CycloneDX',
  specVersion: '1.6',
  serialNumber: `urn:uuid:${randomUUID()}`,
  version: 1,
  metadata: {
    timestamp: new Date().toISOString(),
    tools: {
      components: [
        {
          type: 'application',
          name: 'k8sense-sbom-generator',
          version: '1',
          description: 'build/generate-sbom.mjs, part of the K8Sense repository',
        },
      ],
    },
    component: {
      type: 'application',
      'bom-ref': 'pkg:generic/k8sense',
      name: 'K8Sense',
      version: version(),
      description: 'A native desktop Kubernetes client.',
      licenses: [{ license: { id: 'Apache-2.0' } }],
    },
  },
  components: shipped
    .map(component)
    .sort((left, right) => left.purl.localeCompare(right.purl)),
}

mkdirSync(outputDir, { recursive: true })
writeFileSync(outputPath, `${JSON.stringify(bom, null, 2)}\n`)

console.log(
  `Wrote ${bom.components.length} components to build/bin/sbom/k8sense.cdx.json ` +
    `(CycloneDX ${bom.specVersion}, K8Sense ${bom.metadata.component.version}).`,
)
