/**
 * Which namespace the RBAC page reviews. A rules review is per namespace, so
 * the page picks ONE from the filter's set — and on All it asks with '' (the
 * backend's own default) until somebody picks, rather than flipping to
 * another namespace the moment the namespace list arrives.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render } from '@testing-library/svelte'

const subjectRules = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return { ...actual, subjectRules: (...args: unknown[]) => subjectRules(...args) }
})

import RBACView from './RBACView.svelte'

function session(selected: string[], listed: string[]) {
  return {
    cluster: { id: 'dev' },
    isAllNamespaces: selected.length === 0,
    scope: { namespaces: selected, all: selected.length === 0 },
    singleNamespace: selected.length === 1 ? selected[0] : '',
    namespaces: listed.map((name) => ({ name })),
    refreshNamespaces: async () => {},
    manualRefreshes: 0,
    selectedKindId: 'podsteer/rbac',
    query: { terms: [] },
    search: '',
    sort: null,
    toggleSort: () => {},
    pageStart: 0,
    standaloneCount: 0,
    hasTable: true,
  } as never
}

beforeEach(() => {
  subjectRules.mockReset()
  subjectRules.mockResolvedValue({ namespace: 'default', status: 'answered', refusal: '', resourceRules: [], nonResourceRules: [] })
})
afterEach(cleanup)

describe('the namespace the RBAC page reviews', () => {
  it("asks with the backend's own default on All, and does not re-ask when the namespace list arrives", async () => {
    const { rerender } = render(RBACView, { session: session([], []) })
    await vi.waitFor(() => expect(subjectRules).toHaveBeenCalledTimes(1))
    expect(subjectRules).toHaveBeenLastCalledWith('dev', '')

    await rerender({ session: session([], ['alpha', 'default', 'shop']) })
    expect(subjectRules).toHaveBeenCalledTimes(1)
  })

  it('reviews the first of a named set, and says it is per namespace', async () => {
    const { container } = render(RBACView, { session: session(['keda', 'shop'], ['keda', 'shop']) })
    await vi.waitFor(() => expect(subjectRules).toHaveBeenCalledWith('dev', 'keda'))
    expect(container.textContent).toContain('Permissions are reviewed per namespace')
  })
})
