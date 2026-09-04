import { describe, expect, it } from 'vitest'

import {
  LAST_APPLIED_ANNOTATION,
  annotationKeysOf,
  customCell,
  customColumnId,
  customSearchText,
  customSortAccessor,
  customValue,
  isCustomColumnId,
  isValidKey,
  keysOnScreen,
  normaliseSpecs,
  parseCustomColumnId,
  toColumns,
  type CustomColumnSpec,
  type MetadataRow,
} from './customColumns'
import { sortRows } from './sort'

const team: CustomColumnSpec = { source: 'label', key: 'team' }
const owner: CustomColumnSpec = { source: 'annotation', key: 'acme.io/owner' }

function row(labels?: Record<string, string>, annotations?: Record<string, string>): MetadataRow {
  return { labels, annotations }
}

describe('column ids', () => {
  it('round-trip a spec through the id the width and visibility persist under', () => {
    expect(customColumnId(team)).toBe('label:team')
    expect(parseCustomColumnId('label:team')).toEqual(team)
    expect(parseCustomColumnId('annotation:acme.io/owner')).toEqual(owner)
  })

  it('leave built-in column ids alone', () => {
    // The built-in ids are plain words, and the generic table's are
    // positional — neither carries a source prefix.
    expect(parseCustomColumnId('name')).toBeNull()
    expect(parseCustomColumnId('c3')).toBeNull()
    expect(parseCustomColumnId('gitops')).toBeNull()
    expect(isCustomColumnId('label:team')).toBe(true)
    expect(isCustomColumnId('status')).toBe(false)
  })

  it('split on the first colon only, since a key may carry its own', () => {
    expect(parseCustomColumnId('annotation:a:b')).toEqual({ source: 'annotation', key: 'a:b' })
  })

  it('refuse an unknown source or an invalid key', () => {
    expect(parseCustomColumnId('spec:team')).toBeNull()
    expect(parseCustomColumnId('label:')).toBeNull()
    expect(parseCustomColumnId(`annotation:${LAST_APPLIED_ANNOTATION}`)).toBeNull()
  })
})

describe('key validation', () => {
  it('accepts a qualified key and refuses blanks, whitespace and commas', () => {
    expect(isValidKey('label', 'app.kubernetes.io/name')).toBe(true)
    expect(isValidKey('label', '')).toBe(false)
    expect(isValidKey('label', 'with space')).toBe(false)
    expect(isValidKey('label', 'a,b')).toBe(false)
  })

  it('refuses the last-applied manifest as an annotation column', () => {
    // The backend refuses to project it and the watch store strips it, so a
    // column of it would be blank or a whole manifest by luck of the path.
    expect(isValidKey('annotation', LAST_APPLIED_ANNOTATION)).toBe(false)
    // As a LABEL key it is merely a key nothing carries, which is allowed.
    expect(isValidKey('label', LAST_APPLIED_ANNOTATION)).toBe(true)
  })
})

describe('normaliseSpecs', () => {
  it('keeps well-formed specs in order and trims their keys', () => {
    expect(normaliseSpecs([{ source: 'label', key: ' team ' }, owner])).toEqual([team, owner])
  })

  it('drops anything storage may have handed back that is not a spec', () => {
    expect(
      normaliseSpecs([
        null,
        'label:team',
        { source: 'spec', key: 'replicas' },
        { source: 'label' },
        { source: 'annotation', key: LAST_APPLIED_ANNOTATION },
        { source: 'label', key: 'bad key' },
        team,
      ]),
    ).toEqual([team])
    expect(normaliseSpecs(undefined)).toEqual([])
    expect(normaliseSpecs({ source: 'label', key: 'team' })).toEqual([])
  })

  it('keeps the first of a duplicated column', () => {
    expect(normaliseSpecs([team, owner, { source: 'label', key: 'team' }])).toEqual([team, owner])
  })
})

describe('annotationKeysOf', () => {
  it('names only the annotation keys, sorted and unique, for the list request', () => {
    const specs: CustomColumnSpec[] = [
      { source: 'annotation', key: 'z' },
      team,
      { source: 'annotation', key: 'a' },
      { source: 'annotation', key: 'z' },
    ]
    expect(annotationKeysOf(specs)).toEqual(['a', 'z'])
  })

  it('asks for nothing when every column is a label', () => {
    expect(annotationKeysOf([team])).toEqual([])
  })
})

describe('cell values', () => {
  it('read the key from the spec’s own source and nowhere else', () => {
    const both = row({ team: 'payments' }, { 'acme.io/owner': 'alice', team: 'not-this' })
    expect(customValue(both, team)).toBe('payments')
    expect(customValue(both, owner)).toBe('alice')
  })

  it('render an absent value as a dash and an absent map as a dash', () => {
    expect(customCell(row({}, {}), team)).toBe('—')
    expect(customCell(row(), owner)).toBe('—')
    expect(customValue(row(), team)).toBe('')
  })
})

describe('toColumns', () => {
  it('produces one DataTable column per spec, headed by the key, in order', () => {
    const columns = toColumns([owner, team])
    expect(columns.map((column) => column.id)).toEqual(['annotation:acme.io/owner', 'label:team'])
    expect(columns.map((column) => column.label)).toEqual(['acme.io/owner', 'team'])
    expect(columns.every((column) => column.width > 0)).toBe(true)
  })
})

describe('sorting', () => {
  const rows = [
    row({ team: 'payments' }),
    row({}),
    row({ team: 'billing' }),
    row({ team: 'auth' }),
  ]

  it('sorts a custom column by its value with absent values last, both ways', () => {
    const accessor = customSortAccessor<MetadataRow>('label:team')
    expect(accessor).not.toBeNull()
    const accessors = { 'label:team': accessor! }

    const ascending = sortRows(rows, { columnId: 'label:team', direction: 'asc' }, accessors)
    expect(ascending.map((entry) => entry.labels?.team ?? null)).toEqual(['auth', 'billing', 'payments', null])

    const descending = sortRows(rows, { columnId: 'label:team', direction: 'desc' }, accessors)
    expect(descending.map((entry) => entry.labels?.team ?? null)).toEqual(['payments', 'billing', 'auth', null])
  })

  it('has no accessor for a built-in column, which keeps its own', () => {
    expect(customSortAccessor('name')).toBeNull()
  })
})

describe('search text', () => {
  it('contributes each custom column’s value, skipping absent ones', () => {
    const target = row({ team: 'payments' }, {})
    expect(customSearchText(target, [team, owner])).toEqual(['payments'])
    expect(customSearchText(target, [])).toEqual([])
  })
})

describe('keysOnScreen', () => {
  it('collects every key the rows carry, sorted and unique, per source', () => {
    const keys = keysOnScreen([
      row({ team: 'a', app: 'x' }, { 'acme.io/owner': 'alice' }),
      row({ team: 'b' }, { 'acme.io/owner': 'bob', 'acme.io/cost': 'c' }),
      row(),
    ])
    expect(keys).toEqual({
      labels: ['app', 'team'],
      annotations: ['acme.io/cost', 'acme.io/owner'],
    })
  })

  it('never offers the last-applied manifest even if a row somehow carries it', () => {
    const keys = keysOnScreen([row({}, { [LAST_APPLIED_ANNOTATION]: '{}', team: 't' })])
    expect(keys.annotations).toEqual(['team'])
  })
})
