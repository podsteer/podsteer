/**
 * Structured errors for calls into the Go backend.
 *
 * Wails serialises a Go error to a plain string and rejects the promise with
 * it, so there is no structured error object on the wire. The Go side works
 * around this by prefixing every failure with a machine-readable code —
 * `[forbidden] your account is not allowed …` — and this module parses it back
 * out. See app/adapters/wails/errors.go for the producing end; the two must be
 * changed together.
 */

/** Every code the backend can produce. Mirrors ErrorCode in Go. */
export const API_ERROR_CODES = [
  'no_active_cluster',
  'cluster_not_found',
  'unreachable',
  'unauthenticated',
  'forbidden',
  'not_found',
  'kubeconfig_unavailable',
  'credential_plugin_missing',
  'read_only',
  'cancelled',
  'invalid_input',
  'disruption_budget',
  'internal',
] as const

/** A classification of a backend failure, or `unknown` when unparseable. */
export type ApiErrorCode = (typeof API_ERROR_CODES)[number] | 'unknown'

/** Matches the `[code] message` envelope the backend produces. */
const CODE_ENVELOPE = /^\[([a-z_]+)]\s*([\s\S]*)$/

/**
 * Codes worth offering a retry for.
 *
 * A network blip or an expired token can succeed on a second attempt once the
 * operator has fixed something; an RBAC denial or a deleted resource cannot,
 * and offering a retry button there just wastes their time.
 */
const RETRYABLE: ReadonlySet<ApiErrorCode> = new Set<ApiErrorCode>([
  'unreachable',
  'unauthenticated',
  'cancelled',
  'internal',
  'unknown',
  // A PodDisruptionBudget refusal is not permanent: the budget's own
  // disruptions-allowed count moves as other pods finish rolling, so the
  // same eviction can succeed a minute later with nothing else changed.
  'disruption_budget',
])

/** An error returned by a PodSteer backend call. */
export class ApiError extends Error {
  /** The backend's classification of the failure. */
  readonly code: ApiErrorCode

  constructor(code: ApiErrorCode, message: string, options?: ErrorOptions) {
    super(message, options)
    this.name = 'ApiError'
    this.code = code
  }

  /** Whether retrying the same call could plausibly succeed. */
  get isRetryable(): boolean {
    return RETRYABLE.has(this.code)
  }

  /** Whether the failure is simply that no cluster is connected yet. */
  get isNotConnected(): boolean {
    return this.code === 'no_active_cluster'
  }

  /**
   * Whether the failure is PodSteer's own read-only guard refusing a write.
   *
   * Reaching this from the frontend means a write control slipped past the
   * disabling this same code is supposed to apply — the backend check is a
   * second line of defence, not the first. See app/ports/errors.go's
   * ErrReadOnly.
   */
  get isReadOnly(): boolean {
    return this.code === 'read_only'
  }
}

/**
 * Normalises anything thrown by a binding call into an ApiError.
 *
 * Rejection values arrive as strings from Wails, but a bug in the frontend can
 * just as easily throw a TypeError through the same call site — so this
 * accepts `unknown` and always produces something renderable rather than
 * letting `undefined` reach the UI.
 */
export function toApiError(cause: unknown): ApiError {
  if (cause instanceof ApiError) {
    return cause
  }

  const raw = extractMessage(cause)
  const match = CODE_ENVELOPE.exec(raw)

  if (!match) {
    return new ApiError('unknown', raw || 'An unexpected error occurred.', { cause })
  }

  const [, code, message] = match
  return new ApiError(asApiErrorCode(code), message || raw, { cause })
}

/** Pulls a human-readable string out of an arbitrary thrown value. */
function extractMessage(cause: unknown): string {
  if (typeof cause === 'string') return cause.trim()
  if (cause instanceof Error) return cause.message.trim()
  if (cause == null) return ''

  try {
    return String(cause).trim()
  } catch {
    return ''
  }
}

/** Narrows a parsed code to a known one, or `unknown`. */
function asApiErrorCode(code: string): ApiErrorCode {
  return (API_ERROR_CODES as readonly string[]).includes(code)
    ? (code as ApiErrorCode)
    : 'unknown'
}
