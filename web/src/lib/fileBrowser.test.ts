import { describe, expect, it } from 'vitest'

import { childOf, crumbsFor, displayName, filterNames, normalise, parentOf } from './fileBrowser'

describe('walking a container path', () => {
  it('breaks a path into crumbs, root first', () => {
    expect(crumbsFor('/var/log/nginx')).toEqual([
      { label: '/', path: '/' },
      { label: 'var', path: '/var' },
      { label: 'log', path: '/var/log' },
      { label: 'nginx', path: '/var/log/nginx' },
    ])
  })

  it('gives the root a crumb of its own', () => {
    // Otherwise there is nothing to click when you are three directories deep.
    expect(crumbsFor('/')).toEqual([{ label: '/', path: '/' }])
  })

  it('does not produce an empty crumb from a trailing slash', () => {
    expect(crumbsFor(normalise('/var/log/'))).toEqual(crumbsFor('/var/log'))
  })

  it('keeps a name with spaces in one crumb', () => {
    const crumbs = crumbsFor('/opt/my application/conf')

    expect(crumbs.map((crumb) => crumb.label)).toEqual(['/', 'opt', 'my application', 'conf'])
    expect(crumbs[2]?.path).toBe('/opt/my application')
  })

  it('CLAMPS Up at the root', () => {
    // `/..` is a path the far end resolves back to `/` anyway, so asking is a
    // request made to learn nothing.
    expect(parentOf('/')).toBe('/')
    expect(parentOf('/var')).toBe('/')
    expect(parentOf('/var/log/nginx')).toBe('/var/log')
  })

  it('never joins into a double slash at the root', () => {
    expect(childOf('/', 'etc')).toBe('/etc')
    expect(childOf('/etc', 'nginx')).toBe('/etc/nginx')
    expect(childOf('/etc/', 'nginx')).toBe('/etc/nginx')
  })

  it('treats a trailing slash as the same directory', () => {
    expect(normalise('/var/log/')).toBe('/var/log')
    expect(normalise('/')).toBe('/')
    expect(normalise('')).toBe('/')
    expect(normalise('   ')).toBe('/')
  })
})

describe('a name that is not valid text', () => {
  it('is marked rather than repaired', () => {
    // It arrives already damaged — Go's JSON encoder replaced the bytes — so
    // nothing here can recover it, and pretending to would be worse than
    // saying so: acting on it would fetch a different file.
    expect(displayName('caf�', false)).toBe('caf?')
    expect(displayName('café', true)).toBe('café')
  })
})

describe('filtering rows already in hand', () => {
  const rows = [{ name: 'access.log' }, { name: 'error.log' }, { name: 'nginx.conf' }]

  it('matches a substring, case-insensitively', () => {
    expect(filterNames(rows, 'LOG').map((row) => row.name)).toEqual(['access.log', 'error.log'])
  })

  it('returns everything for an empty term', () => {
    expect(filterNames(rows, '   ')).toHaveLength(3)
  })
})
