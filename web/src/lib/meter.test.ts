import { describe, expect, it } from 'vitest'

import { meterTitle } from './meter'

const unmeasured = (available: boolean | undefined) =>
  meterTitle(false, available, '', false, '', 0, false, '', 0)

describe('meter titles for something nothing measured', () => {
  it('does not tell a metered cluster it has no metrics source', () => {
    // THE BUG THIS GUARDS SENT PEOPLE TO ARGUE WITH AN ADMINISTRATOR. Rows
    // that measure ONE object — a pod, a node — pass `undefined`, because a
    // single object reporting nothing says nothing about the cluster. That
    // is falsy, so a pod twenty seconds old on a perfectly metered cluster
    // was told its cluster had no metrics source, while the overview pane,
    // which actually knows, said the opposite two clicks away.
    expect(unmeasured(undefined)).not.toContain('no metrics source')
  })

  it('says so when the cluster genuinely serves none', () => {
    expect(unmeasured(false)).toContain('no metrics source')
  })

  it('blames the objects, not the cluster, when the cluster does serve metrics', () => {
    expect(unmeasured(true)).toContain('no running pod has been measured')
  })
})
