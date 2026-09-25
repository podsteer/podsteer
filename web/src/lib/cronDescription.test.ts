import { describe, expect, it } from 'vitest'

import { describeCron } from './cronDescription'

describe('describeCron', () => {
  it.each([
    ['*/5 * * * *', 'Every 5 minutes'],
    ['* * * * *', 'Every minute'],
    ['0 * * * *', 'Every hour'],
    ['15 * * * *', 'Every hour at 15 minutes past'],
    ['0 */6 * * *', 'Every 6 hours'],
    ['30 */2 * * *', 'Every 2 hours, at 30 minutes past'],
    ['0 2 * * *', 'At 02:00, every day'],
    ['30 9 * * 1-5', 'At 09:30, on Monday to Friday'],
    ['0 9 * * MON,WED,FRI', 'At 09:00, on Monday, Wednesday and Friday'],
    ['0 0 1 * *', 'At 00:00, on the 1st of the month'],
    ['0 0 1,15 * *', 'At 00:00, on the 1st and 15th of the month'],
    ['0 0 * JAN *', 'At 00:00, every day, in January'],
    ['0 8,20 * * *', 'At 08:00 and 20:00, every day'],
    ['*/10 9-17 * * *', 'Every 10 minutes between 09:00 and 17:59'],
    ['0 0 * * 0', 'At 00:00, on Sunday'],
    ['0 0 * * 7', 'At 00:00, on Sunday'],
    ['@hourly', 'Every hour'],
    ['@daily', 'Every day at 00:00'],
    ['@every 1h30m', 'Every 1h30m'],
  ])('%s → %s', (expression, expected) => {
    expect(describeCron(expression)).toBe(expected)
  })

  it('says "or" when both day fields are set, because cron fires on either', () => {
    expect(describeCron('0 0 1 * 1')).toBe('At 00:00, on the 1st of the month or on Monday')
  })

  it.each(['', '0 0 * *', '61 * * * *', '0 25 * * *', '0 0 * * 8', 'nonsense', '@fortnightly', '5-10/2 3-4 * * *'])(
    'declines to describe %j rather than guess',
    (expression) => {
      expect(describeCron(expression)).toBeNull()
    },
  )
})
