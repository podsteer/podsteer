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
 *   • Go — SHIPPED is the union of `go list -deps` across all three release
 *     platforms. Running it once on the host is the trap: the Windows toast
 *     stack and two others are reached only under GOOS=windows, so a
 *     macOS-generated inventory is missing modules the Windows binary actually
 *     contains. BUILD is the union of `go list -deps -test` minus that — every
 *     module a build or a test on a release platform compiles, and nothing
 *     else. Modules the graph mentions that neither reaches are GRAPH-ONLY and
 *     are counted, not classified. `./scope.mjs` holds that split and
 *     explains why membership replaced the earlier test, which asked whether a
 *     module happened to be in the local module cache — a question about the
 *     machine rather than about the build.
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
import { partitionGoModules } from './scope.mjs'

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
  // No webkit2_41 tag any more: Wails v2 defaulted to webkit2gtk 4.0 and the
  // Linux build had to opt in, which changed which files — and therefore which
  // imports — were in scope. v3 targets 4.1 directly, so there is one Linux
  // build and no tag to keep in step with the Makefile's.
  { GOOS: 'linux', GOARCH: 'amd64', tags: [] },
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
 * Lists the modules the build reaches for one platform.
 *
 * With `includeTests`, `-test` is added and the answer widens to everything a
 * build OR a test compiles. Called both ways per platform: without it the
 * result is what the binary links, which is the shipped set and is unchanged
 * by any of this; with it, the reachable set that build scope is computed
 * from. Nothing else about the invocation differs, so the two answers describe
 * the same tree and can be compared.
 *
 * CGO_ENABLED=1 is explicit and required. Cross-GOOS invocations default it to
 * 0, which drops every `import "C"` file from build-constraint evaluation and
 * silently prunes the dependencies reached through them — and Wails is cgo on
 * darwin and linux. `go list` never invokes a C compiler, so no cross
 * toolchain is needed to ask the question.
 */
function goModulesFor(repoRoot, platform, mainModule, includeTests = false) {
  const args = ['list', '-deps']
  if (includeTests) args.push('-test')
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
 *
 * Being syntactic, it cannot tell an import from a sentence containing the
 * word "from". Captures spanning whitespace are discarded for that reason —
 * see below — which is the one exception to failing loudly, and a safe one:
 * no package specifier has ever contained a space.
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
        if (entry.name !== 'bindings' && entry.name !== 'node_modules') walk(path)
        continue
      }
      if (!/\.(svelte|ts|js|mjs)$/.test(entry.name)) continue

      // TEST FILES ARE NOT SHIPPED SOURCE. They sit beside the components
      // they exercise, which is where component tests belong, but nothing in
      // the application imports them: the bundle is built from what the entry
      // point reaches, and a test file is reached by vitest and by nothing
      // else. Counting them made vitest and @testing-library look like
      // runtime dependencies and demanded they move to "dependencies", which
      // would ship a test runner to every user.
      //
      // Narrow on purpose, in keeping with the rest of this file: only the
      // one suffix, matched exactly, so a source file cannot accidentally
      // excuse itself from the check by being named suggestively.
      if (/\.test\.(ts|js|mjs)$/.test(entry.name)) continue

      const source = readFileSync(path, 'utf8')
      for (const match of source.matchAll(pattern)) {
        const specifier = match[1]
        // Prose, not code. The pattern cannot tell an import from an English
        // sentence, and a Svelte attribute reading label="Amber from" puts
        // the word immediately before a quote — so the capture ran on to the
        // next quote several lines later and was reported as a missing
        // package. A specifier can never contain whitespace, so anything that
        // does is the regex having found a sentence.
        //
        // Deliberately narrow: only what provably cannot be a package name is
        // dropped, because the point of this check is to fail loudly rather
        // than to quietly excuse whatever it does not recognise.
        if (/\s/.test(specifier)) continue
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

  // Two listings per platform, differing only in `-test`: what the binary
  // links, and everything a build or a test compiles. Unioned across platforms
  // before either is used, because a module reached on one platform alone is
  // still reached — that is the go-webview2 lesson, and under v3 it is the
  // Windows toast stack.
  const linkedGo = new Set()
  const reachedGo = new Set()
  const perPlatform = {}
  for (const platform of PLATFORMS) {
    const linked = goModulesFor(repoRoot, platform, mainModule)
    const reached = goModulesFor(repoRoot, platform, mainModule, true)
    // The reported count stays the LINKED one: it answers "how much does the
    // binary for this platform carry", which is what a reader of the report is
    // asking, and it is the number the shipped inventory is built from.
    perPlatform[`${platform.GOOS}/${platform.GOARCH}`] = linked.size
    for (const entry of linked) linkedGo.add(entry)
    for (const entry of reached) reachedGo.add(entry)
  }

  // The whole module GRAPH — requirements of our requirements, most of which
  // no build path imports.
  const graphGo = new Set(
    run('go', ['list', '-m', '-f', '{{.Path}}@{{.Version}}', 'all'], { cwd: repoRoot })
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line && !line.startsWith(`${mainModule}@`)),
  )

  const {
    shipped: shippedGo,
    build: buildGo,
    graphOnly: graphOnlyGo,
  } = partitionGoModules({ graph: graphGo, linked: linkedGo, reached: reachedGo })

  // Everything classified must have source on this machine to classify FROM.
  // The predicate this replaced used cache presence as the classifier itself,
  // so a module missing from a cold cache quietly became unclassified and the
  // gate passed without ever reading its licence. Now the same situation is an
  // error with something to do about it.
  const uncached = [...shippedGo, ...buildGo].filter(
    (entry) => !existsSync(goModuleDir(modCache, entry)),
  )
  if (uncached.length > 0) {
    throw new Error(
      'These Go modules participate in a build or test but are not in the module ' +
        `cache, so their licences cannot be read: ${uncached.join(', ')}. ` +
        'Run `go mod download` and try again.',
    )
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

  const packages = applyNoticeSources(repoRoot, [
    ...shippedGo.map((entry) => goPackage(modCache, entry, 'shipped')),
    ...buildGo.map((entry) => goPackage(modCache, entry, 'build')),
    ...[...shippedTree.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([name, entry]) => npmPackage(name, entry, 'shipped')),
    ...[...fullTree.entries()]
      .filter(([name]) => !shippedTree.has(name))
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([name, entry]) => npmPackage(name, entry, 'build')),
  ])

  return {
    packages,
    shipped: packages.filter((entry) => entry.scope === 'shipped'),
    build: packages.filter((entry) => entry.scope === 'build'),
    perPlatform,
    mislabelled,
    unresolved,
    graphOnlyGo,
  }
}

/**
 * Fills in notices for packages that publish none of their own.
 *
 * MIT AND BSD REQUIRE THE NOTICE TO BE REPRODUCED, and a package that omits
 * the file from its tarball does not thereby remove the duty — it only means
 * the text has to come from where the project actually keeps it. The rule here
 * is that it comes from a SIBLING ALREADY IN THIS INVENTORY: same project, same
 * licence, same holder, and a file we can point at rather than prose somebody
 * typed out. `build/licences/notice-sources.json` records which, and why.
 *
 * The sibling may be in ANOTHER ECOSYSTEM, named by `inheritsFromEcosystem`.
 * That is not a loosening: one repository routinely publishes both a Go module
 * and an npm package under one licence held by one project, and Wails is
 * exactly that — `@wailsio/runtime` is built out of the same tree as
 * `github.com/wailsapp/wails/v3` and ships no licence file of its own. Refusing
 * to look across the boundary would force one of the two alternatives this
 * mechanism exists to avoid: prose typed out from memory, or a false claim that
 * no notice exists. The default is still the target's own ecosystem, so an
 * entry meaning a sibling package says so by omission.
 *
 * Three things make it fail rather than fudge:
 *
 *   - a sibling that is not in the tree, so the entry cannot silently do
 *     nothing after a dependency is dropped;
 *   - a sibling with no licence text of its own, which would inherit nothing;
 *   - a package that has SINCE started shipping its own licence, so the
 *     exception is removed rather than quietly shadowing the real file.
 */
function applyNoticeSources(root, packages) {
  const path = join(root, 'build', 'licences', 'notice-sources.json')
  if (!existsSync(path)) return packages

  const { sources = [] } = JSON.parse(readFileSync(path, 'utf8'))
  const byName = new Map(packages.map((entry) => [`${entry.ecosystem}:${entry.name}`, entry]))

  for (const source of sources) {
    const key = `${source.ecosystem}:${source.package}`
    const target = byName.get(key)
    if (!target) continue

    if (target.text) {
      throw new Error(
        `${key} now ships its own licence; remove its entry from notice-sources.json ` +
          `rather than shadowing the real file`,
      )
    }

    const donorEcosystem = source.inheritsFromEcosystem ?? source.ecosystem
    const donorKey = `${donorEcosystem}:${source.inheritsFrom}`
    const donor = byName.get(donorKey)
    if (!donor) {
      throw new Error(
        `${key} inherits its notice from ${donorKey}, which is not in the ` +
          `dependency tree — the entry is stale`,
      )
    }
    if (!donor.text) {
      throw new Error(`${key} inherits its notice from ${donorKey}, which has none`)
    }

    target.text = donor.text
    target.copyright = donor.copyright
    // Recorded on the entry so the inventory says where the text came from
    // rather than implying the package shipped it. Qualified by ecosystem when
    // the donor is in another one, so the Credits pane cannot read
    // "@wailsio/runtime inherits from github.com/wailsapp/wails/v3" as a
    // package name it could look up beside it.
    target.noticeFrom = donorEcosystem === target.ecosystem ? source.inheritsFrom : donorKey
  }

  return packages
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
