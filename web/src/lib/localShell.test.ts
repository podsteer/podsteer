import { describe, expect, it } from 'vitest'
import {
  localShellNotice,
  localShellRequest,
  localShellTitle,
  type CodingAgent,
} from './localShell'

const CLAUDE: CodingAgent = { id: 'claude', label: 'Claude Code', path: '/opt/bin/claude' }
const GEMINI: CodingAgent = { id: 'gemini', label: 'Gemini CLI', path: '/usr/local/bin/gemini' }

describe('localShellRequest', () => {
  it('asks for a plain shell when no agent was chosen', () => {
    expect(localShellRequest('', true, [CLAUDE])).toEqual({ agent: null, readOnly: false })
  })

  it('normalises the read-only marker away for a plain shell', () => {
    // The marker means "somebody asked an agent to stay read-only". A shell
    // was asked nothing, so carrying it would be a claim about a session that
    // never made one.
    expect(localShellRequest('  ', true, [CLAUDE]).readOnly).toBe(false)
  })

  it('carries the chosen agent and the read-only default', () => {
    expect(localShellRequest('claude', true, [CLAUDE, GEMINI])).toEqual({
      agent: 'claude',
      readOnly: true,
    })
  })

  it('lets the operator turn the read-only default off', () => {
    expect(localShellRequest('claude', false, [CLAUDE]).readOnly).toBe(false)
  })

  it('falls back to a plain shell for an agent that is no longer there', () => {
    // Detection ran a moment ago; an id missing from the list now means the
    // binary went away. Opening the shell they would otherwise have had beats
    // an error about a tool they did not know had gone.
    expect(localShellRequest('codex', true, [CLAUDE])).toEqual({ agent: null, readOnly: false })
  })
})

describe('localShellTitle', () => {
  it('names a plain shell', () => {
    expect(localShellTitle({ agent: null, readOnly: false }, [])).toBe('Local shell')
  })

  it('names the agent rather than the pane', () => {
    expect(localShellTitle({ agent: 'gemini', readOnly: true }, [CLAUDE, GEMINI])).toBe('Gemini CLI')
  })

  it('falls back to the identifier when the label is unknown', () => {
    expect(localShellTitle({ agent: 'gemini', readOnly: true }, [])).toBe('gemini')
  })
})

describe('localShellNotice', () => {
  it('says the read-only guard does not apply, and why', () => {
    // The load-bearing sentence. This pane deliberately opens on a cluster
    // every other terminal here refuses, and an operator must be able to read
    // why rather than infer it.
    const notice = localShellNotice('prod-eu')

    expect(notice).toContain('read-only')
    expect(notice).toContain("PodSteer's own writes")
    expect(notice).toContain('your own credentials')
  })

  it('names the open context and says the shell is set to it', () => {
    const notice = localShellNotice('prod-eu')

    expect(notice).toContain('prod-eu')
    expect(notice).toContain('set to that context')
  })

  it('no longer tells anybody to pass --context', () => {
    // The instruction was true until the shell started getting a kubeconfig
    // holding only current-context at the front of its KUBECONFIG. Leaving it
    // would teach the wrong model of what this pane does, so its absence is
    // asserted rather than left to a reviewer to notice.
    expect(localShellNotice('prod-eu')).not.toContain('--context')
  })

  it('says the operator\u2019s own kubeconfig is untouched and the selection is session-scoped', () => {
    const notice = localShellNotice('prod-eu')

    expect(notice).toContain('your own kubeconfig is untouched')
    expect(notice).toContain('only as long as this terminal')
  })

  it('says so plainly when no cluster tab is open', () => {
    expect(localShellNotice('')).toContain('no cluster tab')
  })
})
