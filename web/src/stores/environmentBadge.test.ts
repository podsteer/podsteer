import { describe, expect, it } from 'vitest'
import {
  environmentBadge,
  environmentName,
  sanitiseEnvironmentShort,
  type GroupSettings,
} from './organisation.svelte'

const settings = (patch: Partial<GroupSettings>): GroupSettings => ({
  environment: '',
  customName: '',
  customShort: '',
  colour: '',
  readOnly: false,
  ...patch,
})

describe('the environment mark on a tab', () => {
  it.each([
    ['production', 'PRD'],
    ['staging', 'STG'],
    ['qa', 'QA'],
    ['development', 'DEV'],
    ['other', ''],
    ['', ''],
  ] as const)('%s reads %j', (environment, badge) => {
    expect(environmentBadge(settings({ environment }))).toBe(badge)
  })

  it('uses a custom environment’s own mark, upper-cased', () => {
    expect(environmentBadge(settings({ environment: 'custom', customName: 'Sandbox', customShort: 'sbx' }))).toBe(
      'SBX',
    )
  })

  it('falls back to the custom name’s first three letters', () => {
    expect(environmentBadge(settings({ environment: 'custom', customName: 'Sandbox' }))).toBe('SAN')
  })

  it('names a custom environment by its own name', () => {
    expect(environmentName(settings({ environment: 'custom', customName: 'Sandbox' }))).toBe('Sandbox')
    expect(environmentName(settings({ environment: 'custom' }))).toBe('custom')
    expect(environmentName(settings({ environment: 'qa' }))).toBe('qa')
  })
})

describe('a custom mark as typed', () => {
  it.each([
    ['sbx', 'SBX'],
    ['sandbox', 'SAN'],
    ['s-b x', 'SBX'],
    ['q1', 'Q1'],
    ['', ''],
  ])('%j is kept as %j', (typed, kept) => {
    expect(sanitiseEnvironmentShort(typed)).toBe(kept)
  })
})
