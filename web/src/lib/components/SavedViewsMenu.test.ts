import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SavedViewsMenu from './SavedViewsMenu.svelte'
import { preferences } from '$stores/preferences.svelte'
import type { ViewState } from '$lib/savedViews'

const current: ViewState = {
  kindId: 'core/v1/pods',
  namespace: 'kube-system',
  search: 're:crash',
  statusFilters: ['crashing'],
}

const onapply = vi.fn()

async function open(props: Partial<{ current: ViewState }> = {}) {
  const rendered = render(SavedViewsMenu, {
    current,
    kindTitle: (kindId: string) => (kindId === 'core/v1/pods' ? 'Pods' : ''),
    allNamespaces: '__all__',
    onapply,
    ...props,
  })
  await fireEvent.click(rendered.getByRole('button', { name: 'Saved views' }))
  return rendered
}

describe('the saved-views menu', () => {
  beforeEach(() => {
    preferences.savedViews = []
    onapply.mockReset()
  })

  afterEach(cleanup)

  it('saves what is on screen under the typed name', async () => {
    const { getByLabelText, getByRole } = await open()

    await fireEvent.input(getByLabelText('Name this view'), {
      target: { value: 'Crashing pods' },
    })
    await fireEvent.click(getByRole('button', { name: 'Save' }))

    expect(preferences.savedViews).toEqual([
      { id: 'crashing-pods', name: 'Crashing pods', ...current },
    ])
  })

  it('says Replace, not Save, for a name already in use', async () => {
    // Overwriting is the right behaviour and a silent one would be a
    // surprise, so the button says which of the two is about to happen.
    preferences.saveView('Crashing pods', current)
    const { getByLabelText, getByRole, queryByRole } = await open()

    await fireEvent.input(getByLabelText('Name this view'), {
      target: { value: 'crashing pods' },
    })

    expect(getByRole('button', { name: 'Replace' })).toBeTruthy()
    expect(queryByRole('button', { name: 'Save' })).toBeNull()
  })

  it('will not save an unnamed view', async () => {
    const { getByRole } = await open()
    expect((getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('applies a view through the callback rather than reaching into the session', async () => {
    preferences.saveView('Everything', {
      kindId: 'apps/v1/deployments',
      namespace: '__all__',
      search: '',
      statusFilters: [],
    })
    const { getByText } = await open()

    await fireEvent.click(getByText('Everything'))

    expect(onapply).toHaveBeenCalledWith(
      expect.objectContaining({ kindId: 'apps/v1/deployments', name: 'Everything' }),
    )
  })

  it('says what each view selects, so a name nobody remembers is still readable', async () => {
    preferences.saveView('Crashing pods', current)
    const { getByText } = await open()

    expect(getByText('Pods · kube-system · re:crash · crashing')).toBeTruthy()
  })

  it('deletes a view without applying it', async () => {
    preferences.saveView('Crashing pods', current)
    const { getByRole } = await open()

    await fireEvent.click(getByRole('button', { name: 'Delete Crashing pods' }))

    expect(preferences.savedViews).toEqual([])
    expect(onapply).not.toHaveBeenCalled()
  })

  it('pre-fills the name of the view that is on screen', async () => {
    // So adjusting a filter and pressing Save corrects the view somebody is
    // plainly working on rather than making a near-duplicate to tidy up.
    preferences.saveView('Crashing pods', current)
    const { getByLabelText } = await open()

    await vi.waitFor(() => {
      expect((getByLabelText('Name this view') as HTMLInputElement).value).toBe('Crashing pods')
    })
  })

  it('leaves the name empty when nothing matches what is on screen', async () => {
    preferences.saveView('Crashing pods', current)
    const { getByLabelText } = await open({ current: { ...current, search: 'something else' } })

    expect((getByLabelText('Name this view') as HTMLInputElement).value).toBe('')
  })
})
