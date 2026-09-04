import { describe, expect, it } from 'vitest'

import { nameConfirmed } from './confirm'

describe('nameConfirmed', () => {
  it('confirms an exact match', () => {
    expect(nameConfirmed('checkout-web', 'checkout-web')).toBe(true)
  })

  it('forgives leading and trailing whitespace from a paste', () => {
    expect(nameConfirmed('  checkout-web  ', 'checkout-web')).toBe(true)
    expect(nameConfirmed('checkout-web\n', 'checkout-web')).toBe(true)
  })

  it('is case-sensitive, because Kubernetes names are', () => {
    // my-app and My-App can coexist as two different objects in the same
    // namespace — a case-insensitive match would let someone confirm the
    // wrong one and believe they had confirmed the right one.
    expect(nameConfirmed('Checkout-Web', 'checkout-web')).toBe(false)
    expect(nameConfirmed('CHECKOUT-WEB', 'checkout-web')).toBe(false)
  })

  it('refuses a partial or mistyped name', () => {
    expect(nameConfirmed('checkout', 'checkout-web')).toBe(false)
    expect(nameConfirmed('checkout-we', 'checkout-web')).toBe(false)
    expect(nameConfirmed('checkout-web ', 'checkout-web-2')).toBe(false)
  })

  it('does not confirm on internal whitespace differences', () => {
    // Only leading/trailing whitespace is forgiven — a space typed in the
    // middle of a name is a mistyped name, not a paste artefact.
    expect(nameConfirmed('checkout web', 'checkout-web')).toBe(false)
  })

  it('refuses an empty confirmation, whatever is expected', () => {
    expect(nameConfirmed('', 'checkout-web')).toBe(false)
    expect(nameConfirmed('   ', 'checkout-web')).toBe(false)
  })
})
