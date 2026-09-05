/**
 * The Wails runtime, stubbed for unit tests.
 *
 * `vitest.config.ts` already states the rule this file enforces: the backend
 * is a binding surface that does not exist outside a webview, so anything
 * reaching for it belongs in a Go test. Under Wails v2 that rule held by
 * accident — the bridge the webview injects was simply absent, so a call
 * failed on the spot. v3's runtime has a real HTTP transport, so the same
 * call instead opens a socket to a development server that is not running,
 * once per test file that imports anything reaching a binding. The tests
 * still passed; they just took a connection refusal the long way round, and
 * printed an aggregate error for every attempt.
 *
 * Aliasing the package to this module makes the stated rule true: a unit test
 * cannot perform inter-process communication at all, and one that tries fails
 * with a sentence saying what to do instead rather than with a network error
 * nobody reads as a design problem.
 */

/** Thrown by every stubbed entry point. */
function unavailable(what: string): Error {
  return new Error(
    `${what} reached the Wails runtime in a unit test. The binding surface ` +
      `does not exist here: stub the API client, or move the assertion to a Go test.`,
  )
}

export function Call(): Promise<never> {
  return Promise.reject(unavailable('A bound method'))
}

/**
 * The generated bindings import this for its type as well as its value, so it
 * has to be constructible; a test that awaits one gets the same refusal.
 */
export class CancellablePromise<T> extends Promise<T> {
  cancel(): Promise<void> {
    return Promise.resolve()
  }
}

export function Create(): never {
  throw unavailable('An event creator')
}

export const Events = {
  On: () => () => {},
  Emit: () => Promise.reject(unavailable('An event emit')),
  Off: () => {},
}

export const Window = {
  IsFullscreen: () => Promise.reject(unavailable('A window query')),
  Name: () => Promise.reject(unavailable('A window query')),
}

export const Browser = {
  OpenURL: () => Promise.reject(unavailable('A browser open')),
}
