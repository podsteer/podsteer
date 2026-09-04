import { describe, expect, it } from 'vitest'
import { findAutoscalers } from './autoscalers'
import type { ResourceTable } from './api/client'

/** Builds a table with the given column headers and cell rows, in order. */
function table(headers: string[], rows: { name: string; cells: string[] }[]): ResourceTable {
  return {
    kindId: 'test/v1/things',
    title: 'Things',
    namespaced: true,
    columns: headers.map((name) => ({ name, type: 'string', wide: false, description: '' })),
    rows: rows.map((row) => ({ name: row.name, namespace: 'web', cells: row.cells })),
  } as ResourceTable
}

describe('findAutoscalers — HorizontalPodAutoscaler', () => {
  it('matches an HPA whose REFERENCE names the target exactly', () => {
    const hpas = table(
      ['NAME', 'REFERENCE', 'TARGETS', 'MINPODS', 'MAXPODS', 'REPLICAS', 'AGE'],
      [{ name: 'web-hpa', cells: ['web-hpa', 'Deployment/web', '10%/80%', '2', '10', '3', '4d'] }],
    )

    const found = findAutoscalers(hpas, 'hpa', { kind: 'Deployment', name: 'web' })

    expect(found).toEqual([
      { name: 'web-hpa', kind: 'HorizontalPodAutoscaler', minReplicas: '2', maxReplicas: '10' },
    ])
  })

  it('is case-insensitive about the header names', () => {
    const hpas = table(
      ['name', 'reference', 'minpods', 'maxpods'],
      [{ name: 'web-hpa', cells: ['web-hpa', 'Deployment/web', '2', '10'] }],
    )

    expect(findAutoscalers(hpas, 'hpa', { kind: 'Deployment', name: 'web' })).toHaveLength(1)
  })

  it('does not match a name that merely starts the same', () => {
    // "web-canary" must not be mistaken for "web" — a substring match here
    // would point an operator at the wrong autoscaler entirely.
    const hpas = table(
      ['NAME', 'REFERENCE', 'MINPODS', 'MAXPODS'],
      [{ name: 'canary-hpa', cells: ['canary-hpa', 'Deployment/web-canary', '1', '5'] }],
    )

    expect(findAutoscalers(hpas, 'hpa', { kind: 'Deployment', name: 'web' })).toEqual([])
  })

  it('does not match the right name on the wrong kind', () => {
    // A StatefulSet named "web" must not be caught by an HPA targeting the
    // Deployment of the same name.
    const hpas = table(
      ['NAME', 'REFERENCE', 'MINPODS', 'MAXPODS'],
      [{ name: 'web-hpa', cells: ['web-hpa', 'StatefulSet/web', '1', '5'] }],
    )

    expect(findAutoscalers(hpas, 'hpa', { kind: 'Deployment', name: 'web' })).toEqual([])
  })

  it('returns nothing, never throws, when the REFERENCE column is missing', () => {
    const hpas = table(['NAME', 'MINPODS', 'MAXPODS'], [{ name: 'web-hpa', cells: ['web-hpa', '1', '5'] }])

    expect(() => findAutoscalers(hpas, 'hpa', { kind: 'Deployment', name: 'web' })).not.toThrow()
    expect(findAutoscalers(hpas, 'hpa', { kind: 'Deployment', name: 'web' })).toEqual([])
  })

  it('reports a match without bounds when MINPODS/MAXPODS are missing', () => {
    const hpas = table(['NAME', 'REFERENCE'], [{ name: 'web-hpa', cells: ['web-hpa', 'Deployment/web'] }])

    expect(findAutoscalers(hpas, 'hpa', { kind: 'Deployment', name: 'web' })).toEqual([
      { name: 'web-hpa', kind: 'HorizontalPodAutoscaler', minReplicas: undefined, maxReplicas: undefined },
    ])
  })

  it('ignores a row referencing something else entirely', () => {
    const hpas = table(
      ['NAME', 'REFERENCE', 'MINPODS', 'MAXPODS'],
      [{ name: 'api-hpa', cells: ['api-hpa', 'Deployment/api', '1', '5'] }],
    )

    expect(findAutoscalers(hpas, 'hpa', { kind: 'Deployment', name: 'web' })).toEqual([])
  })
})

describe('findAutoscalers — KEDA ScaledObject', () => {
  it('matches a ScaledObject whose target columns name the target exactly', () => {
    const scaledObjects = table(
      ['NAME', 'SCALETARGETKIND', 'SCALETARGETNAME', 'MIN', 'MAX', 'READY', 'ACTIVE', 'AGE'],
      [
        {
          name: 'web-so',
          cells: ['web-so', 'apps/v1.Deployment', 'web', '1', '20', 'True', 'True', '4d'],
        },
      ],
    )

    const found = findAutoscalers(scaledObjects, 'keda', { kind: 'Deployment', name: 'web' })

    expect(found).toEqual([{ name: 'web-so', kind: 'ScaledObject', minReplicas: '1', maxReplicas: '20' }])
  })

  it('accepts a bare kind as well as the GroupVersion-qualified one KEDA prints', () => {
    // status.scaleTargetKind is "apps/v1.Deployment" on every current KEDA;
    // an older release or a hand-written row prints just "Deployment". Both
    // name the same target and both must match.
    const scaledObjects = table(
      ['NAME', 'SCALETARGETKIND', 'SCALETARGETNAME', 'MIN', 'MAX'],
      [{ name: 'web-so', cells: ['web-so', 'Deployment', 'web', '2', '8'] }],
    )

    expect(findAutoscalers(scaledObjects, 'keda', { kind: 'Deployment', name: 'web' })).toHaveLength(1)
  })

  it('does not match a near-miss name', () => {
    const scaledObjects = table(
      ['NAME', 'SCALETARGETKIND', 'SCALETARGETNAME', 'MIN', 'MAX'],
      [{ name: 'canary-so', cells: ['canary-so', 'Deployment', 'web-canary', '1', '20'] }],
    )

    expect(findAutoscalers(scaledObjects, 'keda', { kind: 'Deployment', name: 'web' })).toEqual([])
  })

  it('does not match the right name on the wrong kind', () => {
    const scaledObjects = table(
      ['NAME', 'SCALETARGETKIND', 'SCALETARGETNAME', 'MIN', 'MAX'],
      [{ name: 'web-so', cells: ['web-so', 'StatefulSet', 'web', '1', '20'] }],
    )

    expect(findAutoscalers(scaledObjects, 'keda', { kind: 'Deployment', name: 'web' })).toEqual([])
  })

  it('returns nothing, never throws, when the target columns are missing', () => {
    const scaledObjects = table(['NAME', 'MIN', 'MAX'], [{ name: 'web-so', cells: ['web-so', '1', '20'] }])

    expect(() =>
      findAutoscalers(scaledObjects, 'keda', { kind: 'Deployment', name: 'web' }),
    ).not.toThrow()
    expect(findAutoscalers(scaledObjects, 'keda', { kind: 'Deployment', name: 'web' })).toEqual([])
  })

  it('reports a match without bounds when MIN/MAX are missing', () => {
    const scaledObjects = table(
      ['NAME', 'SCALETARGETKIND', 'SCALETARGETNAME'],
      [{ name: 'web-so', cells: ['web-so', 'Deployment', 'web'] }],
    )

    expect(findAutoscalers(scaledObjects, 'keda', { kind: 'Deployment', name: 'web' })).toEqual([
      { name: 'web-so', kind: 'ScaledObject', minReplicas: undefined, maxReplicas: undefined },
    ])
  })
})

describe('findAutoscalers — an empty table', () => {
  it('answers with nothing for either kind', () => {
    const empty = table(['NAME', 'REFERENCE', 'MINPODS', 'MAXPODS'], [])

    expect(findAutoscalers(empty, 'hpa', { kind: 'Deployment', name: 'web' })).toEqual([])
  })
})
