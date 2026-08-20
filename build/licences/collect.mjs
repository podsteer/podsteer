/**
 * Enumerating what PodSteer ships, and what merely builds it.
 *
 * That distinction is the crux of the whole exercise. A licence obligation
 * attaches to DISTRIBUTION: an MPL-2.0 CSS compiler that runs on a developer's
 * machine and emits ordinary CSS carries none, while the same code inside the
 * binary would. Getting the boundary wrong in one direction over-credits
 * harmlessly; getting it wrong in the other ships an obligation nobody
 * recorded.
 *
 * So the boundary is computed, never asserted:
 *
 *   • Go — the union of `go list -deps` across all three release platforms.
 *     Running it once on the host is the trap: `go-webview2` and two others
 *     are reached only under GOOS=windows, so a macOS-generated inventory is
 *     missing modules the Windows binary actually contains.
 *   • npm — the `--omit=dev` tree, cross-checked against what `web/src`
 *     actually imports. The cross-check exists because a package imported by
 *     shipped source but declared as a devDependency would otherwise vanish
 *     from the inventory entirely, which is the one silent way this goes
 *     wrong.
 */

import { execFileSync } from 'node:child_process'
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { reconcile } from './classify.mjs'

/** Files a licence is conventionally kept in, in order of preference. */
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

/**
 * Files carrying an Apache-2.0 NOTICE.
 *
 * Section 4(d) requires these to be reproduced in distributions, and it is a
 * separate duty from reproducing the licence itself — a NOTICE names
 * contributors and attributions the licence text does not.
 */
const NOTICE_FILES = ['NOTICE', 'NOTICE.txt', 'NOTICE.md']

/** The release platforms PodSteer builds for. */
const PLATFORMS = [
  { GOOS: 'darwin', GOARCH: 'arm64', tags: [] },
  { GOOS: 'windows', GOARCH: 'amd64', tags: [] },
  // Ubuntu ships webkit2gtk 4.1 only; the Linux build opts in, and the tag
  // changes which files — and therefore which imports — are in scope.
  { GOOS: 'linux', GOARCH: 'amd64', tags: ['webkit2_41'] },
]

const MODULE_TEMPLATE = '{{if not .Standard}}{{with .Module}}{{.Path}}@{{.Version}}{{end}}{{end}}'

/** Reads the first file in a directory matching one of the given names. */
function readFirst(dir, names) {
  for (const name of names) {
    const path = join(dir, name)
    if (existsSync(path) && statSync(path).isFile()) {
      return readFileSync(path, 'utf8').trim()
    }
  }
  return ''
}

/** Reads a licence file, falling back to any licence-shaped filename. */
function readLicence(dir) {
  const known = readFirst(dir, LICENCE_FILES)
  if (known) return known
  if (!existsSync(dir)) return ''

  const fallback = readdirSync(dir).find(
    (name) => /^licen[cs]e/i.test(name) && statSync(join(dir, name)).isFile(),
  )
  return fallback ? readFileSync(join(dir, fallback), 'utf8').trim() : ''
}

/**
 * Extracts the copyright notice — the part MIT and BSD specifically require to
 * be reproduced.
 *
 * Lines that BEGIN with the notice are preferred over lines that merely
 * mention the word, because licence files discuss copyright in prose as well
 * as asserting it. yaml.v3 is the case that proved this necessary: its first
 * line matching a loose search is "...copyright staring in 2011 when the
 * project was ported over:", which is a sentence fragment, while the actual
 * notices sit five lines below it.
 *
 * Several are kept when a project asserts several — a file ported from two
 * upstreams carries both, and reproducing one of them is not reproducing the
 * notice.
 */
function copyrightOf(text) {
  const lines = text.split('\n').map((line) => line.trim())
  const hasYear = (line) => /\d{4}/.test(line) || line.includes('©')

  // "Copyright" must be followed by the marks a NOTICE uses — (c), © or the
  // year itself. Matching the bare word is what let a sentence beginning
  // "copyright staring in 2011..." through.
  const asserted = lines.filter((line) =>
    /^(copyright\s*(\(c\)|©|\d{4})|\(c\)\s*\d{4}|©\s*\d{4})/i.test(line),
  )
  if (asserted.length > 0) {
    // Deduplicated: a licence repeated per-file often repeats its notice too.
    return [...new Set(asserted)].slice(0, 3).join(' · ')
  }

  // Nothing asserted it outright; fall back to any mention, which is still
  // better than crediting nobody.
  return lines.find((line) => /copyright/i.test(line) && hasYear(line)) ?? ''
}

/** Go's module cache escapes capitals as `!x`, to stay case-insensitive. */
function escapeModulePath(modulePath) {
  return modulePath.replace(/[A-Z]/g, (character) => `!${character.toLowerCase()}`)
}

/** Runs a command, returning stdout. */
function run(command, args, options = {}) {
  return execFileSync(command, args, {
    maxBuffer: 64 * 1024 * 1024,
    encoding: 'utf8',
    ...options,
  })
}

/**
 * The module being built, as Go itself reports it.
 *
 * Asked rather than assumed. This exclusion used to be the literal string
 * `podsteer@`, which stopped matching the moment the module was renamed to its
 * import path — and the failure mode was not a missing exclusion but a policy
 * violation, because the project then entered its own shipped inventory with
 * no classifiable licence. Nothing obliges you to credit yourself.
 */
function goMainModule(repoRoot) {
  return run('go', ['list', '-m'], { cwd: repoRoot }).trim()
}

/**
 * Lists the modules linked into the binary for one platform.
 *
 * CGO_ENABLED=1 is explicit and required. Cross-GOOS invocations default it to
 * 0, which drops every `import "C"` file from build-constraint evaluation and
 * silently prunes the dependencies reached through them — and Wails is cgo on
 * darwin and linux. `go list` never invokes a C compiler, so no cross
 * toolchain is needed to ask the question.
 */
function goModulesFor(repoRoot, platform, mainModule) {
  const args = ['list', '-deps']
  if (platform.tags.length > 0) args.push('-tags', platform.tags.join(','))
  args.push('-f', MODULE_TEMPLATE, './...')

  const output = run('go', args, {
    cwd: repoRoot,
    env: {
      ...process.env,
      CGO_ENABLED: '1',
      GOOS: platform.GOOS,
      GOARCH: platform.GOARCH,
    },
  })

  return new Set(
    output
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line && line !== '@' && !line.startsWith(`${mainModule}@`)),
  )
}

/** Returns the module cache directory for a `path@version` entry. */
function goModuleDir(modCache, entry) {
  const at = entry.lastIndexOf('@')
  return join(modCache, `${escapeModulePath(entry.slice(0, at))}@${entry.slice(at + 1)}`)
}

/** Builds a package record for one Go module. */
function goPackage(modCache, entry, scope) {
  const at = entry.lastIndexOf('@')
  const name = entry.slice(0, at)
  const version = entry.slice(at + 1)
  const dir = goModuleDir(modCache, entry)

  const text = readLicence(dir)
  const resolved = reconcile({ declared: '', text })

  return {
    name,
    version,
    ecosystem: 'go',
    scope,
    declared: '',
    identifier: resolved.identifier,
    source: resolved.source,
    conflict: resolved.conflict,
    copyright: copyrightOf(text),
    text,
    noticeText: readFirst(dir, NOTICE_FILES),
  }
}

/** Walks an `npm ls --json` tree, collecting every distinct package. */
function walkNpmTree(node, found) {
  for (const [name, child] of Object.entries(node.dependencies ?? {})) {
    if (!found.has(name) && child.path) {
      found.set(name, { version: child.version ?? '', path: child.path, license: child.license })
    }
    walkNpmTree(child, found)
  }
  return found
}

/** Builds a package record for one npm package. */
function npmPackage(name, entry, scope) {
  const text = readLicence(entry.path)
  const declared = normaliseDeclared(entry.license)
  const resolved = reconcile({ declared, text })

  return {
    name,
    version: entry.version,
    ecosystem: 'npm',
    scope,
    declared,
    identifier: resolved.identifier,
    source: resolved.source,
    conflict: resolved.conflict,
    copyright: copyrightOf(text),
    text,
    noticeText: readFirst(entry.path, NOTICE_FILES),
  }
}

/** npm's `license` field has three historical shapes; flatten them. */
function normaliseDeclared(license) {
  if (!license) return ''
  if (typeof license === 'string') return license
  if (Array.isArray(license)) return license.map((entry) => entry.type ?? entry).join(' OR ')
  return license.type ?? ''
}

/**
 * Every bare package specifier statically imported by shipped frontend source.
 *
 * Deliberately syntactic rather than clever: a regex over import and
 * re-export specifiers. It only needs to answer "does shipped source name this
 * package", and a false positive fails loudly rather than silently.
 */
function importedPackages(webRoot) {
  const sourceRoot = join(webRoot, 'src')
  const specifiers = new Set()
  const pattern = /(?:from|import)\s*\(?\s*['"]([^'"]+)['"]/g

  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name)
      if (entry.isDirectory()) {
        // Generated bindings are not a dependency; they are our own output.
        if (entry.name !== 'wailsjs' && entry.name !== 'node_modules') walk(path)
        continue
      }
      if (!/\.(svelte|ts|js|mjs)$/.test(entry.name)) continue

      const source = readFileSync(path, 'utf8')
      for (const match of source.matchAll(pattern)) {
        const specifier = match[1]
        // Relative paths and the project's own $lib/$stores aliases.
        if (specifier.startsWith('.') || specifier.startsWith('$')) continue
        // "@scope/name/deep/path" and "name/deep/path" both reduce to the
        // package that owns them.
        const segments = specifier.split('/')
        specifiers.add(specifier.startsWith('@') ? segments.slice(0, 2).join('/') : segments[0])
      }
    }
  }

  walk(sourceRoot)
  return specifiers
}

/**
 * Collects every dependency, split by whether it is distributed.
 *
 * Throws with an actionable message rather than returning an empty set when a
 * prerequisite is missing. An empty npm set would satisfy every policy check,
 * which is the dangerous direction to fail in.
 */
export function collect(repoRoot) {
  const webRoot = join(repoRoot, 'web')

  if (!existsSync(join(webRoot, 'node_modules'))) {
    throw new Error(
      'web/node_modules is missing, so the shipped npm set cannot be determined. ' +
        'Run `npm --prefix web ci` first.',
    )
  }

  // --- Go ------------------------------------------------------------------
  const modCache = run('go', ['env', 'GOMODCACHE'], { cwd: repoRoot }).trim()

  const mainModule = goMainModule(repoRoot)

  const shippedGo = new Set()
  const perPlatform = {}
  for (const platform of PLATFORMS) {
    const modules = goModulesFor(repoRoot, platform, mainModule)
    perPlatform[`${platform.GOOS}/${platform.GOARCH}`] = modules.size
    for (const entry of modules) shippedGo.add(entry)
  }

  // Everything in the build list that no platform's binary reaches: test
  // helpers, tooling, indirect modules pruned by the compiler.
  const allGoModules = new Set(
    run('go', ['list', '-m', '-f', '{{.Path}}@{{.Version}}', 'all'], { cwd: repoRoot })
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line && !line.startsWith(`${mainModule}@`)),
  )

  // `go list -m all` reports the whole module GRAPH, which includes modules
  // Go never downloads because nothing in any build path imports them. They
  // have no source on this machine, so they cannot be classified — and they
  // do not need to be: an unfetched module is neither shipped nor executed.
  // Counting them as UNKNOWN would fail the policy on fifteen packages that
  // do not participate in anything.
  const unfetchedGo = []
  const buildGo = []
  for (const entry of allGoModules) {
    if (shippedGo.has(entry)) continue
    if (existsSync(goModuleDir(modCache, entry))) buildGo.push(entry)
    else unfetchedGo.push(entry)
  }

  // --- npm -----------------------------------------------------------------
  const shippedTree = walkNpmTree(
    JSON.parse(run('npm', ['ls', '--omit=dev', '--all', '--json', '--long'], { cwd: webRoot })),
    new Map(),
  )
  const fullTree = walkNpmTree(
    JSON.parse(run('npm', ['ls', '--all', '--json', '--long'], { cwd: webRoot })),
    new Map(),
  )

  // --- the import cross-check ---------------------------------------------
  const imported = importedPackages(webRoot)
  const mislabelled = [...imported].filter((name) => !shippedTree.has(name) && fullTree.has(name))
  const unresolved = [...imported].filter((name) => !fullTree.has(name))

  const packages = [
    ...[...shippedGo].sort().map((entry) => goPackage(modCache, entry, 'shipped')),
    ...buildGo.sort().map((entry) => goPackage(modCache, entry, 'build')),
    ...[...shippedTree.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([name, entry]) => npmPackage(name, entry, 'shipped')),
    ...[...fullTree.entries()]
      .filter(([name]) => !shippedTree.has(name))
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([name, entry]) => npmPackage(name, entry, 'build')),
  ]

  return {
    packages,
    shipped: packages.filter((entry) => entry.scope === 'shipped'),
    build: packages.filter((entry) => entry.scope === 'build'),
    perPlatform,
    mislabelled,
    unresolved,
    unfetchedGo,
  }
}

/**
 * The repository root, resolved from THIS module's location rather than the
 * caller's.
 *
 * Callers live at different depths — `build/` and `build/licences/` — so a
 * caller-relative path would be right for one and silently wrong for the
 * other, which is exactly the kind of bug that shows up as "node_modules is
 * missing" two directories away from where it actually is.
 */
export function repoRoot() {
  return resolve(fileURLToPath(new URL('../..', import.meta.url)))
}
