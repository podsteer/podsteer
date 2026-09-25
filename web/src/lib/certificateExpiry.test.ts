import { describe, expect, it } from 'vitest'

import { certificateExpiryLabel } from './certificateExpiry'

const DAY = 86400

describe('a certificate’s expiry, said in words', () => {
  it('counts forward from a positive expiresInSeconds', () => {
    expect(certificateExpiryLabel(5 * DAY)).toBe('expires in 5 days')
  })

  it('uses the singular for exactly one day', () => {
    // "expires in 1 days" is the kind of thing that makes a panel read as
    // machine-generated rather than written for somebody to read.
    expect(certificateExpiryLabel(DAY)).toBe('expires in 1 day')
  })

  it('says "today" rather than "in 0 days"', () => {
    expect(certificateExpiryLabel(DAY - 1)).toBe('expires today')
  })

  it('counts backward once expiresInSeconds has gone negative', () => {
    expect(certificateExpiryLabel(-5 * DAY)).toBe('expired 5 days ago')
  })

  it('uses the singular for exactly one day ago too', () => {
    expect(certificateExpiryLabel(-DAY)).toBe('expired 1 day ago')
  })

  it('says "expired today" rather than "0 days ago"', () => {
    expect(certificateExpiryLabel(-1)).toBe('expired today')
  })
})
