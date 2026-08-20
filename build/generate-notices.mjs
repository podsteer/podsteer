#!/usr/bin/env node
/**
 * Generates the third-party notices K8Sense ships.
 *
 * Every dependency K8Sense distributes is under a permissive licence, and all
 * of them — MIT, BSD, ISC, Apache-2.0 — require the same thing in return: that
 * the licence and its copyright notice travel with the binary. This produces
 * that inventory so the Credits pane can show it, and so the obligation cannot
 * quietly fall out of date as dependencies change.
 *
 * Scope is deliberately what SHIPS, not what is installed:
 *
 *   - Go modules actually linked into the binary (`go list -deps`), not every
 *     module in go.sum, most of which are build- or test-only.
 *   - npm packages in the runtime dependency tree (`npm ls --omit=dev`), not
 *     the build toolchain. Vite, Tailwind and TypeScript never reach a user's
 *     machine, so there is nothing to notify anyone about.
 *
 * Licence texts are deduplicated by content: the MIT text repeats identically
 * across dozens of packages, and storing it once keeps the embedded file at a
 * size worth shipping.
 *
 * Run via `make notices`. The output is committed so that a build needs
 * neither the module cache nor node_modules to produce a compliant artifact.
 */

import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const webRoot = join(repoRoot, 'web')
const outputPath = join(repoRoot, 'app', 'adapters', 'notices', 'notices.json')

/** Filenames a licence is conventionally kept in, in order of preference. */
const LICENCE_FILES = [
  'LICENSE',
  'LICENSE.txt',
  'LICENSE.md',
  'LICENCE',
  'LICENCE.txt',
  'LICENSE.MIT',
  'LICENSE-MIT',
  'LICENSE-APACHE',
  'COPYING',
  'COPYING.txt',
  'UNLICENSE',
]

/** Identifies a licence from its text, for packages that declare none. */
function classify(text) {
  const head = text.slice(0, 2000)
  if (/Apache License/i.test(head)) return 'Apache-2.0'
  if (/GNU AFFERO/i.test(head)) return 'AGPL'
  if (/GNU LESSER/i.test(head)) return 'LGPL'
  if (/GNU GENERAL PUBLIC/i.test(head)) return 'GPL'
  if (/Mozilla Public License/i.test(head)) return 'MPL-2.0'
  if (/Business Source License/i.test(head)) return 'BSL'
  if (/Server Side Public License/i.test(head)) return 'SSPL'
  if (/Permission to use, copy, modify, and\/or distribute/i.test(head)) return 'ISC'
  if (/Permission is hereby granted, free of charge/i.test(head)) return 'MIT'
  if (/Redistribution and use in source and binary forms/i.test(head)) return 'BSD'
  return 'UNKNOWN'
}

/** Reads the first licence-shaped file in a directory. */
function readLicence(dir) {
  for (const name of LICENCE_FILES) {
    const path = join(dir, name)
    if (existsSync(path)) return readFileSync(path, 'utf8').trim()
  }
  // Some packages name it LICENSE-<something>; take the first such file.
  if (existsSync(dir)) {
    const fallback = readdirSync(dir).find((name) => /^licen[cs]e/i.test(name))
    if (fallback) {
      const path = join(dir, fallback)
      try {
        return readFileSync(path, 'utf8').trim()
      } catch {
        // A directory named LICENSES/, for instance. Nothing to read.
      }
    }
  }
  return ''
}

/**
 * Extracts the copyright line, which is the part MIT and BSD specifically
 * require to be reproduced.
 */
function copyrightOf(text) {
  const line = text.split('\n').find((entry) => /copyright/i.test(entry) && /\d{4}|©/.test(entry))
  return line ? line.trim() : ''
}

/** Go's module cache escapes capitals as `!x`, to stay case-insensitive. */
function escapeModulePath(modulePath) {
  return modulePath.replace(/[A-Z]/g, (character) => `!${character.toLowerCase()}`)
}

function collectGoModules() {
  const modCache = execFileSync('go', ['env', 'GOMODCACHE'], { cwd: repoRoot }).toString().trim()
  const listed = execFileSync(
    'go',
    ['list', '-deps', '-f', '{{if not .Standard}}{{.Module.Path}}@{{.Module.Version}}{{end}}', './...'],
    { cwd: repoRoot, maxBuffer: 32 * 1024 * 1024 },
  )
    .toString()
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line && line !== '@' && !line.startsWith('k8sense@'))

  const seen = new Set()
  const modules = []

  for (const entry of [...new Set(listed)].sort()) {
    const at = entry.lastIndexOf('@')
    const name = entry.slice(0, at)
    const version = entry.slice(at + 1)
    if (seen.has(name)) continue
    seen.add(name)

    const dir = join(modCache, `${escapeModulePath(name)}@${version}`)
    const text = readLicence(dir)
    modules.push({
      name,
      version,
      ecosystem: 'go',
      licence: text ? classify(text) : 'UNKNOWN',
      copyright: copyrightOf(text),
      text,
    })
  }
  return modules
}

function collectNpmPackages() {
  const tree = JSON.parse(
    execFileSync('npm', ['ls', '--omit=dev', '--all', '--json', '--long'], {
      cwd: webRoot,
      maxBuffer: 64 * 1024 * 1024,
    }).toString(),
  )

  const found = new Map()

  const walk = (node) => {
    for (const [name, child] of Object.entries(node.dependencies ?? {})) {
      if (!found.has(name) && child.path) {
        const text = readLicence(child.path)
        found.set(name, {
          name,
          version: child.version ?? '',
          ecosystem: 'npm',
          licence: child.license ?? (text ? classify(text) : 'UNKNOWN'),
          copyright: copyrightOf(text),
          text,
        })
      }
      walk(child)
    }
  }
  walk(tree)

  return [...found.values()].sort((left, right) => left.name.localeCompare(right.name))
}

const packages = [...collectGoModules(), ...collectNpmPackages()]

// Deduplicate the licence texts. The MIT text is byte-identical across dozens
// of packages; storing it once takes the embedded file from megabytes to tens
// of kilobytes.
const texts = {}
const entries = packages.map((entry) => {
  let textId = ''
  if (entry.text) {
    textId = createHash('sha256').update(entry.text).digest('hex').slice(0, 16)
    texts[textId] = entry.text
  }
  return {
    name: entry.name,
    version: entry.version,
    ecosystem: entry.ecosystem,
    licence: entry.licence,
    copyright: entry.copyright,
    textId,
  }
})

const missing = entries.filter((entry) => !entry.textId || entry.licence === 'UNKNOWN')
const copyleft = entries.filter((entry) => /^(GPL|LGPL|AGPL|BSL|SSPL)$/.test(entry.licence))

writeFileSync(
  outputPath,
  `${JSON.stringify(
    {
      generated: 'by build/generate-notices.mjs — run `make notices` after changing dependencies',
      packages: entries,
      texts,
    },
    null,
    1,
  )}\n`,
)

const byLicence = entries.reduce((counts, entry) => {
  counts[entry.licence] = (counts[entry.licence] ?? 0) + 1
  return counts
}, {})

console.log(`${entries.length} shipped dependencies:`)
for (const [licence, count] of Object.entries(byLicence).sort((a, b) => b[1] - a[1])) {
  console.log(`  ${String(count).padStart(3)}  ${licence}`)
}
for (const entry of missing) {
  console.warn(`  warning: no licence text found for ${entry.ecosystem}:${entry.name}`)
}

// A copyleft dependency in a product intended for commercial distribution is
// a decision, not a detail. Fail rather than let one arrive unnoticed.
if (copyleft.length > 0) {
  console.error('\nRefusing to generate: copyleft licences found in shipped dependencies.')
  for (const entry of copyleft) {
    console.error(`  ${entry.licence.padEnd(6)} ${entry.ecosystem}:${entry.name}@${entry.version}`)
  }
  process.exit(1)
}
