/**
 * What the drain preview may claim about which node.
 *
 * The dialog PLANS before it asks — "Will evict N pods" is fetched, not
 * guessed — which makes the pod count the one number the confirm button is
 * about. It follows that the count must never belong to a different node
 * than the name above it.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, cleanup } from '@testing-library/svelte'

const planDrain = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    planDrain: (...args: unknown[]) => planDrain(...args),
    drainNode: vi.fn(),
  }
})

import DrainDialog from './DrainDialog.svelte'

/** A plan that would evict `count` pods, and nothing else. */
const plan = (count: number) => ({
  evict: Array.from({ length: count }, (_, index) => ({ namespace: 'shop', name: `web-${index}` })),
  skipped: [],
  refused: [],
  runnable: true,
})

const props = (nodeName: string) => ({
  open: true,
  clusterId: 'dev',
  ctx: 'dev',
  nodeName,
  onclose: () => {},
  ondrained: () => {},
  onerror: () => {},
})

const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

beforeEach(() => {
  planDrain.mockReset()
})

afterEach(cleanup)

describe('the drain preview', () => {
  it('drops the previous node’s counts the moment it is pointed at another', async () => {
    // THE BUG THIS GUARDS. The reset explained itself in terms of a previous
    // node's report or error and left `plan` alone, so the first node's
    // eviction count stood under the second node's name until the second
    // node's own preview came back — which, on a cluster that is answering
    // slowly, is exactly when somebody is most likely to read it.
    planDrain.mockResolvedValueOnce(plan(12))
    const { container, rerender } = render(DrainDialog, props('ip-10-0-1-9'))
    await settle()

    expect(container.textContent).toContain('Will evict 12 pods')

    // The second node's preview is still in flight.
    planDrain.mockReturnValueOnce(new Promise(() => {}))
    await rerender(props('ip-10-0-2-4'))
    await settle()

    expect(container.textContent).not.toContain('Will evict 12 pods')
    expect(container.textContent).toContain('Checking what this would do')
  })

  it('asks for the preview once when it opens, with the options it is showing', async () => {
    // Pins the ordering rather than reproducing its failure: the reset is
    // declared BEFORE the effect that loads the preview, so the options are
    // at their defaults by the time it is asked for. Declared after — as it
    // was — it reset them underneath a request already in flight and fired a
    // second one, which a fresh mount cannot show because the defaults are
    // what a fresh mount already has.
    planDrain.mockResolvedValue(plan(1))

    render(DrainDialog, props('ip-10-0-1-9'))
    await settle()

    expect(planDrain).toHaveBeenCalledTimes(1)
    expect(planDrain).toHaveBeenCalledWith('dev', 'ip-10-0-1-9', false, false)
  })
})
