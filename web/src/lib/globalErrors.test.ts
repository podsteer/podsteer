import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '$lib/api/errors'
import { installGlobalErrorHandlers, noticeFor } from './globalErrors'

describe('noticeFor', () => {
  const cases: [string, unknown, string | null][] = [
    ['an API error reads as its own message', new ApiError('forbidden', 'Not allowed'), 'Not allowed'],
    ['a cancellation is expected, not a fault', new ApiError('cancelled', 'cancelled'), null],
    ['an AbortError is expected', Object.assign(new Error('aborted'), { name: 'AbortError' }), null],
    ['the ResizeObserver complaint is benign', new Error('ResizeObserver loop completed with undelivered notifications.'), null],
    ['a plain error is named', new Error('x is undefined'), 'Something went wrong in the background: x is undefined'],
    ['a string rejection is named', 'boom', 'Something went wrong in the background: boom'],
    ['a rejection with nothing in it still says something', undefined, 'Something went wrong in the background.'],
    ['a backend envelope goes through the shared translation', new Error('[unreachable] The cluster refused the connection'), 'The cluster refused the connection'],
    ['a cancelled envelope is expected', new Error('[cancelled] stopped'), null],
  ]
  it.each(cases)('%s', (_name, reason, want) => {
    expect(noticeFor(reason)).toBe(want)
  })
})

describe('installGlobalErrorHandlers', () => {
  function fakeTarget() {
    const listeners = new Map<string, (event: never) => void>()
    return {
      listeners,
      addEventListener: (type: string, listener: (event: never) => void) => listeners.set(type, listener),
      removeEventListener: (type: string) => listeners.delete(type),
    }
  }

  it('logs and reports an unhandled rejection, once', () => {
    const target = fakeTarget()
    const report = vi.fn()
    const log = vi.fn()
    installGlobalErrorHandlers({ target, report, log })

    target.listeners.get('unhandledrejection')?.({ reason: new Error('nil map') } as never)

    expect(log).toHaveBeenCalledTimes(1)
    expect(report).toHaveBeenCalledWith('Something went wrong in the background: nil map')
  })

  it('stays silent for a cancellation', () => {
    const target = fakeTarget()
    const report = vi.fn()
    const log = vi.fn()
    installGlobalErrorHandlers({ target, report, log })

    target.listeners.get('unhandledrejection')?.({ reason: new ApiError('cancelled', 'cancelled') } as never)

    expect(report).not.toHaveBeenCalled()
    expect(log).not.toHaveBeenCalled()
  })

  it('reports an uncaught error and is removable', () => {
    const target = fakeTarget()
    const report = vi.fn()
    const uninstall = installGlobalErrorHandlers({ target, report, log: vi.fn() })

    target.listeners.get('error')?.({ error: new Error('bad render'), message: 'bad render' } as never)
    expect(report).toHaveBeenCalledWith('Something went wrong in the background: bad render')

    uninstall()
    expect(target.listeners.size).toBe(0)
  })
})
