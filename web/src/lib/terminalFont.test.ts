import { describe, expect, it } from 'vitest'

import { TERMINAL_FONT_FAMILIES, TERMINAL_FONT_STACK } from './terminalFont'

/** The families that carry Private Use Area glyphs — the icons and separators. */
const NERD = TERMINAL_FONT_FAMILIES.filter((family) => /Nerd Font|NF$/.test(family))

/** The families that do not, kept as the fallback for everybody else. */
const PLAIN = ['JetBrains Mono', 'Fira Code', 'Cascadia Code', 'Monaco', 'Menlo', 'Ubuntu Mono']

describe('the terminal font stack', () => {
  it('offers a Nerd Font before any plain family', () => {
    // The whole reason this file exists. A prompt built with Powerlevel10k
    // draws its separators from the Private Use Area; no plain family has a
    // glyph there, so a plain-first stack renders that prompt as boxes.
    const firstNerd = TERMINAL_FONT_FAMILIES.findIndex((family) => NERD.includes(family))
    const firstPlain = TERMINAL_FONT_FAMILIES.findIndex((family) =>
      PLAIN.includes(family as (typeof PLAIN)[number]),
    )

    expect(firstNerd).toBeGreaterThanOrEqual(0)
    expect(firstNerd).toBeLessThan(firstPlain)
  })

  it('leads with the font Powerlevel10k installs', () => {
    // Most likely to actually be on the machine of somebody whose prompt needs
    // one, which is the only thing that makes leading with it useful.
    expect(TERMINAL_FONT_FAMILIES[0]).toBe('MesloLGS NF')
  })

  it('keeps every plain family that was there before', () => {
    // The fallback for everybody who installed nothing. This must not become a
    // stack that only works for people who did.
    for (const family of PLAIN) {
      expect(TERMINAL_FONT_FAMILIES).toContain(family)
    }
  })

  it('puts the symbols-only patch after the families that have letters', () => {
    // Symbols Nerd Font Mono has no letters or digits, so leading with it
    // would hand xterm.js a first family it cannot measure a cell from.
    // Per-glyph fallback still reaches it where it is.
    const symbols = TERMINAL_FONT_FAMILIES.indexOf('Symbols Nerd Font Mono')
    const lastPlain = Math.max(
      ...PLAIN.map((family) =>
        TERMINAL_FONT_FAMILIES.indexOf(family as (typeof TERMINAL_FONT_FAMILIES)[number]),
      ),
    )

    expect(symbols).toBeGreaterThan(lastPlain)
    expect(TERMINAL_FONT_FAMILIES.at(-1)).toBe('monospace')
  })

  it('quotes every family name except the generic keyword', () => {
    // A quoted "monospace" asks for a family called monospace rather than for
    // the generic one — the difference between a guaranteed last resort and a
    // name that may match nothing.
    expect(TERMINAL_FONT_STACK.endsWith(', monospace')).toBe(true)
    expect(TERMINAL_FONT_STACK).toContain('"MesloLGS NF"')
    expect(TERMINAL_FONT_STACK).toContain('"JetBrains Mono"')
    expect(TERMINAL_FONT_STACK).not.toContain('"monospace"')
  })
})
