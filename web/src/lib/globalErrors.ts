/**
 * The net under every `void somethingAsync()`.
 *
 * About a hundred call sites start work and deliberately do not await it, and
 * each of those is a promise whose rejection nothing handles. In a webview
 * that is a line in a console nobody has open: the pane simply stops updating.
 * This is the one place those land — it LOGS the failure, with its stack, and
 * shows a short non-blocking notice, so "it silently stopped" becomes "it said
 * so".
 *
 * WHAT IT IGNORES, because a net that shouts at everything teaches people to
 * ignore it: cancellations (an operator closing a tab aborts what that tab had
 * in flight, and that is the design, not a fault), `AbortError`, and the
 * browser's own benign ResizeObserver complaint.
 *
 * A PURE DECISION AND A THIN INSTALLER, for the reason `$lib/notify` is: the
 * rules are the part worth a test per case, and installing listeners is one
 * line.
 */

import { ApiError, toApiError } from '$lib/api/errors'

/** What to show for a failure, or null when it is not worth interrupting anyone. */
export function noticeFor(reason: unknown): string | null {
  if (reason instanceof ApiError) {
    return reason.code === 'cancelled' ? null : reason.message
  }
  if (isAbort(reason)) return null

  const message = describe(reason)
  if (message === '') return 'Something went wrong in the background.'
  if (/ResizeObserver loop/i.test(message)) return null
  if (/^(cancel+ed|aborted)\b/i.test(message)) return null

  // Anything shaped like a backend envelope reads better through the same
  // translation every other surface uses.
  if (/^\[[a-z_]+]/.test(message)) {
    const api = toApiError(reason)
    return api.code === 'cancelled' ? null : api.message
  }
  return `Something went wrong in the background: ${message}`
}

function isAbort(reason: unknown): boolean {
  return (
    typeof reason === 'object' &&
    reason !== null &&
    'name' in reason &&
    (reason as { name?: unknown }).name === 'AbortError'
  )
}

function describe(reason: unknown): string {
  if (reason instanceof Error) return reason.message.trim()
  if (typeof reason === 'string') return reason.trim()
  return ''
}

interface Target {
  addEventListener(type: string, listener: (event: never) => void): void
  removeEventListener(type: string, listener: (event: never) => void): void
}

/**
 * Installs the handlers and returns the function that removes them.
 *
 * `report` and `log` are parameters so a test can watch them; the application
 * passes nothing and gets the notice store and the console.
 */
export function installGlobalErrorHandlers(options: {
  target?: Target
  report: (message: string) => void
  log?: (...args: unknown[]) => void
}): () => void {
  const target = options.target ?? (window as unknown as Target)
  const log = options.log ?? ((...args: unknown[]) => console.error(...args))

  const onRejection = (event: PromiseRejectionEvent): void => {
    const message = noticeFor(event.reason)
    if (message === null) return
    log('Unhandled promise rejection:', event.reason)
    options.report(message)
  }

  const onError = (event: ErrorEvent): void => {
    const reason = event.error ?? event.message
    const message = noticeFor(reason)
    if (message === null) return
    log('Uncaught error:', reason)
    options.report(message)
  }

  target.addEventListener('unhandledrejection', onRejection as (event: never) => void)
  target.addEventListener('error', onError as (event: never) => void)
  return () => {
    target.removeEventListener('unhandledrejection', onRejection as (event: never) => void)
    target.removeEventListener('error', onError as (event: never) => void)
  }
}
