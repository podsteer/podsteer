/**
 * What happens to rows whose kinds print different columns.
 *
 * This is the rule both merged views rest on, and the failure it prevents is
 * a quiet one: a Service's CLUSTER-IP landing in the column a Deployment
 * prints READY into, because both happened to be third.
 */
import { describe, expect, it } from 'vitest'
import { mergeTables, type TableSource } from './mergeTables'

function source(key: string, columns: string[], rows: string[][]): TableSource {
  return {
    key,
    columns: columns.map((name) => ({ name, priority: 0 })) as never,
    rows: rows.map((cells, index) => ({
      name: `${key}-${index}`,
      namespace: 'default',
      cells,
    })) as never,
  }
}

describe('mergeTables', () => {
  it('lines cells up by column NAME, not by position', () => {
    // The bug this exists for. Both kinds print three columns; only two of
    // the names agree. Matching by position would put a Service's type in
    // the Deployment's READY column.
    const merged = mergeTables([
      source('deployments', ['Name', 'Ready', 'Age'], [['web', '3/3', '5d']]),
      source('services', ['Name', 'Type', 'Age'], [['web-svc', 'ClusterIP', '5d']]),
    ])

    expect(merged.columns.map((column) => column.name)).toEqual([
      'Name',
      'Ready',
      'Age',
      'Type',
    ])

    const [deployment, service] = merged.rows
    expect(deployment.cells).toEqual(['web', '3/3', '5d', ''])
    expect(service.cells).toEqual(['web-svc', '', '5d', 'ClusterIP'])
  })

  it('leaves a cell empty where a kind prints no such column', () => {
    // An empty cell says "this kind does not print that". Anything else —
    // a dash, a zero, the previous row's value — would say something the
    // server never said.
    const merged = mergeTables([
      source('pods', ['Name', 'Restarts'], [['api-0', '2']]),
      source('configmaps', ['Name'], [['settings']]),
    ])

    expect(merged.rows[1].cells).toEqual(['settings', ''])
  })

  it('keeps every row pointed at the source it came from', () => {
    // Without this a row cannot be opened: the drawer needs the kind, and
    // two kinds can hold objects of the same name in the same namespace.
    const merged = mergeTables([
      source('deployments', ['Name'], [['web']]),
      source('services', ['Name'], [['web']]),
    ])

    expect(merged.rows.map((row) => row.source)).toEqual(['deployments', 'services'])
  })

  it('gives the first source that prints a column its position', () => {
    const merged = mergeTables([
      source('a', ['Name', 'Age'], []),
      source('b', ['Name', 'Status', 'Age'], []),
    ])

    // Age stays third rather than being pushed along by b's Status.
    expect(merged.columns.map((column) => column.name)).toEqual(['Name', 'Age', 'Status'])
  })

  it('keeps rows in the order the sources were given', () => {
    const merged = mergeTables([
      source('second', ['Name'], [['b']]),
      source('first', ['Name'], [['a']]),
    ])

    expect(merged.rows.map((row) => (row.cells ?? [])[0])).toEqual(['b', 'a'])
  })

  it('merges nothing into nothing rather than throwing', () => {
    expect(mergeTables([])).toEqual({ columns: [], rows: [] })
  })

  it('tolerates a row with fewer cells than its kind declared columns', () => {
    // The API server has printed short rows; a missing cell must not shift
    // every later cell one column to the left.
    const short = source('pods', ['Name', 'Ready', 'Age'], [])
    short.rows = [{ name: 'p', namespace: 'default', cells: ['api-0'] } as never]

    const merged = mergeTables([short])
    expect(merged.rows[0].cells ?? []).toEqual(['api-0', '', ''])
  })
})
