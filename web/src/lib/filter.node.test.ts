/**
 * The pod table's filter and sort, pinned by a fixture Go reads too.
 *
 * THE POD LIST IS FILTERED, SORTED AND PAGED IN GO (domain.QueryPods), and
 * the rules it applies are this frontend's: the query language in $lib/query,
 * the status chips in $lib/podStatusFilters, the collation in $lib/sort and
 * the custom columns' text in $lib/customColumns. Two implementations of one
 * rule drift unless something holds them together, and this is that thing:
 * `filter.fixtures.json` is read by this test AND by
 * app/domain/podquery_test.go, and both must produce its `expected` rows.
 *
 * The pipeline below is the one the session ran over its pods before the
 * table was paged in Go — search (with the tab's own cluster for `cluster:`),
 * then chips, then sort — kept here as the executable statement of what Go
 * was ported FROM. Every primitive it calls is still production code: the
 * other lists filter and sort with exactly these functions.
 *
 * Regenerating `expected` (UPDATE_POD_FIXTURE=1 npm test) makes THIS the
 * reference again; do it only for a deliberate change of the rules, and then
 * make the Go test pass, never the other way round.
 */

import { describe, expect, it } from 'vitest'
import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import fixtureJson from './filter.fixtures.json'
import type { Pod } from './api/client'
import { matches, parseQuery, type Row } from './query'
import { matchesPodStatusChips, POD_STATUS_CHIPS } from './podStatusFilters'
import { parseQuantity, sortRows, type SortAccessors, type SortState } from './sort'
import { customSearchText, customSortAccessor, type CustomColumnSpec } from './customColumns'
import { podStatusLabel } from './format'

interface FixtureCase {
  name: string
  search: string
  chips: string[]
  sort: SortState | null
  columns: CustomColumnSpec[]
  expected?: string[]
  chipCounts?: Record<string, number>
}

interface Fixture {
  cluster: string
  pods: Pod[]
  cases: FixtureCase[]
}

/** Where the fixture is written back from, when regenerating — relative to
    the web/ directory vitest runs in. */
const FIXTURE_PATH = resolve('src/lib/filter.fixtures.json')
const fixture = structuredClone(fixtureJson) as unknown as Fixture

/** The pod table's sort accessors as the session declared them. */
const POD_SORT: SortAccessors<Pod> = {
  status: (pod) => podStatusLabel(pod),
  name: (pod) => pod.name,
  namespace: (pod) => pod.namespace,
  cpu: (pod) => parseQuantity(pod.cpu),
  memory: (pod) => parseQuantity(pod.memory),
  ready: (pod) => pod.readyContainers,
  restarts: (pod) => pod.restarts,
  controlledBy: (pod) => pod.controlledBy,
  node: (pod) => pod.nodeName,
  qos: (pod) => pod.qosClass,
  ip: (pod) => pod.podIp,
  age: (pod) => pod.ageSeconds,
}

function labelRecord(labels: Pod['labels']): Record<string, string> | undefined {
  if (!labels) return undefined
  const record: Record<string, string> = {}
  for (const [key, value] of Object.entries(labels)) if (value !== undefined) record[key] = value
  return record
}

/** Search (the tab's own cluster for `cluster:`), then chips, then sort. */
function run(pods: Pod[], cluster: string, c: FixtureCase): { rows: Pod[]; chipCounts: Record<string, number> } {
  const query = parseQuery(c.search)
  const searched =
    query.terms.length === 0
      ? pods
      : pods.filter((pod) =>
          matches(query, {
            text: [pod.name, pod.namespace, pod.nodeName, pod.phase, ...customSearchText(pod, c.columns)]
              .filter((field): field is string => Boolean(field))
              .join(' '),
            labels: labelRecord(pod.labels),
            cluster,
          } satisfies Row),
        )

  const chipCounts: Record<string, number> = {}
  for (const chip of POD_STATUS_CHIPS) chipCounts[chip.id] = searched.filter(chip.predicate).length

  const visible = searched.filter((pod) => matchesPodStatusChips(pod, c.chips))
  const custom = c.sort ? customSortAccessor<Pod>(c.sort.columnId) : null
  const accessors = c.sort && custom ? { ...POD_SORT, [c.sort.columnId]: custom } : POD_SORT
  return { rows: sortRows(visible, c.sort, accessors), chipCounts }
}

const key = (pod: Pod): string => `${pod.namespace}/${pod.name}`

describe('the pod query fixture shared with Go', () => {
  if (process.env.UPDATE_POD_FIXTURE) {
    it('regenerates expected', () => {
      for (const c of fixture.cases) {
        const { rows, chipCounts } = run(fixture.pods, fixture.cluster, c)
        c.expected = rows.map(key)
        c.chipCounts = chipCounts
      }
      writeFileSync(FIXTURE_PATH, JSON.stringify(fixture, null, 2) + '\n')
    })
    return
  }

  it('has a case for every chip and every built-in sort column', () => {
    const chips = new Set(fixture.cases.flatMap((c) => c.chips))
    for (const chip of POD_STATUS_CHIPS) expect(chips).toContain(chip.id)
    const sorted = new Set(fixture.cases.map((c) => c.sort?.columnId))
    for (const column of Object.keys(POD_SORT)) expect(sorted).toContain(column)
  })

  for (const c of fixture.cases) {
    it(c.name, () => {
      const { rows, chipCounts } = run(fixture.pods, fixture.cluster, c)
      expect(rows.map(key)).toEqual(c.expected)
      expect(chipCounts).toEqual(c.chipCounts)
    })
  }
})
