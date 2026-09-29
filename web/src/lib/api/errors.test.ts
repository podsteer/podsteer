import { describe, expect, it } from 'vitest'
import { DISCONNECTED_MESSAGE, toApiError } from './errors'

describe('toApiError', () => {
  it('parses the conflict code the backend sends for a stale resourceVersion', () => {
    const error = toApiError(
      '[conflict] This object changed on the cluster since you opened it. Reload it and re-apply your edit.',
    )

    expect(error.code).toBe('conflict')
    expect(error.isConflict).toBe(true)
    expect(error.message).toBe(
      'This object changed on the cluster since you opened it. Reload it and re-apply your edit.',
    )
  })

  it('is not retryable — retrying a PUT resends the same stale resourceVersion', () => {
    const error = toApiError('[conflict] stale')
    expect(error.isRetryable).toBe(false)
  })

  it('is not confused with read_only or invalid_input', () => {
    expect(toApiError('[conflict] stale').isReadOnly).toBe(false)
    expect(toApiError('[read_only] nope').isConflict).toBe(false)
    expect(toApiError('[invalid_input] bad manifest').isConflict).toBe(false)
  })

  it.each(['Failed to fetch', 'Load failed', 'NetworkError when attempting to fetch resource.'])(
    'reads the engine’s %j as a lost connection, retryable and in words',
    (engine) => {
      const error = toApiError(new TypeError(engine))

      expect(error.code).toBe('disconnected')
      expect(error.isRetryable).toBe(true)
      expect(error.message).toBe(DISCONNECTED_MESSAGE)
      expect(error.message).not.toMatch(/restart/i)
    },
  )

  it('does not mistake a backend message that merely mentions it', () => {
    expect(toApiError('[unreachable] Failed to fetch the API group list').code).toBe('unreachable')
    expect(toApiError('something Failed to fetch something').code).toBe('unknown')
  })
})

