/**
 * Splitting Go modules into what ships, what merely builds, and what the
 * module graph only mentions.
 *
 * Separate from the collector, and pure, for one reason: this is the decision
 * a licence auditor would question, so it has to be testable without a Go
 * toolchain, a module cache, or a network. It takes three sets of
 * `path@version` strings and returns three sorted lists. It runs nothing.
 *
 *   • `graph`   — every module `go list -m all` reports.
 *   • `linked`  — the union of `go list -deps` across the release platforms:
 *                 what the binaries actually contain. This is SHIPPED.
 *   • `reached` — the union of `go list -deps -test`: everything a build or a
 *                 test on any release platform compiles.
 *
 * BUILD SCOPE IS MEMBERSHIP, NOT CACHE PRESENCE. The predicate this replaced
 * asked whether a module happened to be in the local module cache, which
 * answers "did this machine download it", not "does this participate". Those
 * differ: a module reaches the cache because some unrelated `go install`
 * pulled it in, and a module is absent from it because the cache is cold. So
 * the same tree classified differently on two machines, and in CI the verdict
 * turned on which job last wrote the shared cache.
 *
 * Modules in the graph that nothing reaches are GRAPH-ONLY: reported by count,
 * never classified. They are requirements of our requirements that no build
 * path imports — a claim about somebody else's go.mod, not about PodSteer —
 * and crediting them would describe an inventory we do not distribute.
 *
 * Test-only modules are deliberately BUILD scope rather than a fourth one.
 * They are never distributed, so they carry exactly the obligations a compiler
 * does, which is what build scope already means.
 *
 * The two invariants below throw. Both describe a world where the inputs
 * cannot all be true at once, and a partition computed from contradictory
 * inputs is worse than no partition: it would be wrong quietly.
 */

/** Accepts a Set, an Array, or anything iterable of `path@version`. */
function toSet(entries) {
  return entries instanceof Set ? entries : new Set(entries)
}

/** Sorted, deduplicated, and stable regardless of how the caller iterated. */
function sorted(entries) {
  return [...entries].sort()
}

/**
 * Partitions the module graph into `{ shipped, build, graphOnly }`.
 *
 * @param {object} sets
 * @param {Iterable<string>} sets.graph   every module `go list -m all` reports
 * @param {Iterable<string>} sets.linked  union of `go list -deps` (shipped)
 * @param {Iterable<string>} sets.reached union of `go list -deps -test`
 * @returns {{ shipped: string[], build: string[], graphOnly: string[] }}
 */
export function partitionGoModules({ graph, linked, reached }) {
  const graphSet = toSet(graph)
  const linkedSet = toSet(linked)
  const reachedSet = toSet(reached)

  // `-test` only ever ADDS packages to `-deps`, so linked ⊆ reached holds by
  // construction. If it does not, the two listings saw different worlds —
  // different platform, different tags, an edit between the calls — and
  // nothing computed from them is trustworthy.
  const unreached = sorted([...linkedSet].filter((entry) => !reachedSet.has(entry)))
  if (unreached.length > 0) {
    throw new Error(
      'Go modules are linked into the binary but absent from the -test listing, ' +
        'so the two `go list` runs disagreed about the same tree: ' +
        `${unreached.join(', ')}`,
    )
  }

  const shipped = sorted(linkedSet)
  const build = sorted([...reachedSet].filter((entry) => !linkedSet.has(entry)))

  // Anything compiled is required by something, so the graph must contain it.
  // A miss here means the graph listing is stale or was taken from a different
  // module, and the modules it omits would silently escape the policy.
  const ungraphed = sorted([...shipped, ...build].filter((entry) => !graphSet.has(entry)))
  if (ungraphed.length > 0) {
    throw new Error(
      'Go modules participate in a build or test but are absent from the module ' +
        'graph, so the graph listing does not describe this module: ' +
        `${ungraphed.join(', ')}`,
    )
  }

  // Shipped ∪ build is exactly `reached` once the invariants hold, so
  // "in the graph and in neither" is "in the graph and not reached".
  const graphOnly = sorted([...graphSet].filter((entry) => !reachedSet.has(entry)))

  return { shipped, build, graphOnly }
}
