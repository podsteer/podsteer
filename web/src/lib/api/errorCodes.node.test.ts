import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

import { API_ERROR_CODES } from './errors'

/**
 * The two ends of the error protocol, checked against each other.
 *
 * THE COMMENT AT THE TOP OF errors.ts ALREADY SAYS "the two must be changed
 * together". It said so while seven codes the backend emits were missing from
 * this end — which is what a rule with nothing enforcing it is worth.
 *
 * WHAT THE DRIFT COST, so nobody restores it for tidiness: an unlisted code is
 * narrowed to `unknown` by asApiErrorCode, `unknown` is in RETRYABLE, and
 * ErrorBanner therefore printed the literal word "unknown" under an otherwise
 * precise message and offered a Retry button. The seven included a cluster too
 * old for in-place resize, a container with no probe tool, and a settings file
 * written by a newer PodSteer — three refusals whose own Go doc comments say
 * in as many words that retrying cannot help.
 *
 * Read from the Go source rather than generated, because a generated list
 * would be regenerated silently by whoever added the code and would prove
 * nothing. This fails, and the failure names what is missing.
 */
const HERE = dirname(fileURLToPath(import.meta.url))
const GO_ERRORS = join(HERE, '..', '..', '..', '..', 'app', 'adapters', 'wails', 'errors.go')

/** Every `CodeSomething ErrorCode = "value"` the Go side declares. */
function goCodes(): string[] {
  const source = readFileSync(GO_ERRORS, 'utf8')
  return [...source.matchAll(/\bErrorCode\s*=\s*"([a-z_]+)"/g)].map((match) => match[1] as string)
}

describe('the error codes the two sides agree on', () => {
  it('finds the Go declarations at all', () => {
    // A regex that matched nothing would make every assertion below pass.
    expect(goCodes().length).toBeGreaterThan(20)
  })

  it('knows every code the backend can produce', () => {
    const missing = goCodes().filter((code) => !API_ERROR_CODES.includes(code as never))

    expect(
      missing,
      'these codes exist in app/adapters/wails/errors.go and not in API_ERROR_CODES, so they ' +
        'narrow to "unknown" and are offered a Retry',
    ).toEqual([])
  })

  it('lists no code the backend cannot produce', () => {
    // The other direction matters less but is still a lie: a code handled here
    // and raised nowhere is dead branching somebody will maintain.
    const declared = goCodes()
    const orphaned = API_ERROR_CODES.filter(
      (code) => code !== 'unknown' && !declared.includes(code),
    )

    expect(orphaned, 'these are handled here and raised by nothing in Go').toEqual([])
  })
})
