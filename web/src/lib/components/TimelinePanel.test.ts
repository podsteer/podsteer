import { cleanup, fireEvent, render } from '@testing-library/svelte'
import { afterEach, describe, expect, it } from 'vitest'

import TimelinePanel from './TimelinePanel.svelte'
import { preferences } from '$stores/preferences.svelte'
import type { TimelineEntry } from '$lib/timeline'

/** Distinct titles, so every entry is its own row rather than one group. */
const entries: TimelineEntry[] = Array.from({ length: 60 }, (_, index) => ({
  id: `e${index}`,
  at: 1_000 + index,
  lastAt: 1_000 + index,
  count: 1,
  kind: 'event',
  severity: 'info',
  title: `Event ${index}`,
  detail: '',
  target: { kind: 'Pod', namespace: 'shop', name: `web-${index}` },
}))

afterEach(cleanup)

describe('the timeline pager', () => {
  it('counts pages from 1, as every other pager does', async () => {
    preferences.pageSize = 25
    const { container, getByLabelText } = render(TimelinePanel, { entries, startedAt: 0, paged: true })

    expect(container.textContent).toContain('1 / 3')
    expect(container.textContent).not.toContain('0 / 3')
    expect(container.textContent).not.toContain('Event 25')

    await fireEvent.click(getByLabelText('Next page'))
    expect(container.textContent).toContain('2 / 3')
    expect(container.textContent).toContain('Event 25')

    await fireEvent.click(getByLabelText('First page'))
    expect(container.textContent).toContain('1 / 3')
    expect(container.textContent).toContain('Event 59')
  })
})
