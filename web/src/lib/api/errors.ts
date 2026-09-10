/**
 * Structured errors for calls into the Go backend.
 *
 * Wails carries a Go error across as its `Error()` STRING and nothing else —
 * v2 rejected with the bare string, v3 rejects with a `RuntimeError` whose
 * message is that same string — so there is still no structured error object
 * on the wire. The Go side works around this by prefixing every failure with a
 * machine-readable code — `[forbidden] your account is not allowed …` — and
 * this module parses it back out. See app/adapters/wails/errors.go for the
 * producing end; the two must be changed together.
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
  // The kubeconfig asks for client-go's legacy `auth-provider`, which
  // PodSteer does not register by decision (ADR 10). Not the same as the code
  // above it: one wants a binary installed, this one wants the context
  // converted to an exec plugin, and the backend's message says which.
  'legacy_auth_provider',
  'read_only',
  'cancelled',
  'invalid_input',
  'disruption_budget',
  'conflict',
  'ephemeral_unsupported',
  'tar_missing',
  'command_failed',
  'transfer_limit',
  // A Helm release that decompressed past the ceiling and was REFUSED rather
  // than truncated, and a Secret that would not verify as the release it was
  // asked for. Neither is a fault in the cluster, the credentials or the
  // network, and neither is retryable — see app/adapters/wails/errors.go.
  'helm_payload_too_large',
  'helm_payload_unreadable',
  // An admission controller — Pod Security enforcing `restricted`, or a
  // validating webhook — declined a pod PodSteer tried to create. NOT
  // `forbidden`: the account was allowed and the object was refused, and the
  // message is the API server's own words, which name the field to change.
  'pod_rejected',
  // The container has no shell to list a directory with, and the container
  // printed something that is not a listing. Neither is a fault in the
  // cluster or the credentials — the first is what a distroless image IS.
  'shell_missing',
  'listing_unreadable',
  // A cloud CLI PodSteer offers to drive — see decision 12. None of these is
  // a fault in a cluster, in credentials or in the network, and none is
  // retryable in the sense the transport codes are: a missing binary needs
  // installing, a declined one needs signing in to, and both are answered by
  // the CLI's own words rather than by anything PodSteer knows.
  'vendor_cli_missing',
  'vendor_cli_declined',
  'vendor_cli_timed_out',
  'vendor_cli_unreadable',
  // EIGHT CODES THAT WERE MISSING FROM THIS LIST, and the gap was not
  // cosmetic: an unlisted code narrows to `unknown`, `unknown` is retryable,
  // so ErrorBanner printed the literal word "unknown" beneath an otherwise
  // precise message and offered a Retry. Every one of these is a refusal a
  // second press cannot change, and three say exactly that in their own Go
  // doc comments. errorCodes.node.test.ts now reads the Go source and fails
  // when the two drift, because "the two must be changed together" at the top
  // of this file was true and enforced by nothing.
  //
  // A cluster older than the pods/resize subresource; a container with no nc,
  // curl or wget; a Service whose shape cannot be probed at all.
  'resize_unsupported',
  'probe_tool_missing',
  'probe_unavailable',
  // A drain PodSteer will not perform as asked.
  'drain_refused',
  // A node-shell or debug pod that will never run. The code exists because
  // the answer used to be "an unexpected error occurred".
  'pod_did_not_start',
  // The settings file: not writable, written by a newer PodSteer, or
  // unreadable. Retrying a refusal that is working as designed is not a
  // remedy.
  'settings_read_only',
  'settings_from_future',
  'settings_unavailable',
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

  /**
   * Whether the failure is an apply whose resourceVersion the cluster no
   * longer recognises — the object changed since the manifest was read.
   *
   * A distinct code rather than folded into invalid_input, because the
   * recovery is specific: reload the object and re-apply the edit against
   * it, never retry the same request, which would resend the same stale
   * resourceVersion. See app/ports/errors.go's ErrConflict.
   */
  get isConflict(): boolean {
    return this.code === 'conflict'
  }
}

/**
 * Normalises anything thrown by a binding call into an ApiError.
 *
 * Rejection values arrive from Wails as an Error carrying the Go message, but
 * a bug in the frontend can just as easily throw a TypeError through the same
 * call site — and a v2-era bare string is still accepted, since the shape is
 * the framework's to choose and this parser should not be what breaks if it
 * changes again. So this accepts `unknown` and always produces something
 * renderable rather than letting `undefined` reach the UI.
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
