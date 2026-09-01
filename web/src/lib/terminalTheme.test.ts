import { beforeEach, describe, expect, it } from 'vitest'
import { terminalTheme, isLightTheme } from './terminalTheme'

/** Puts real palette values on the document, as app.css does. */
function paint(theme: 'light' | 'dark'): void {
  const root = document.documentElement
  if (theme === 'light') {
    root.setAttribute('data-theme', 'light')
    root.style.setProperty('--surface-container-lowest', '#ffffff')
    root.style.setProperty('--on-surface', '#1d1b20')
    root.style.setProperty('--primary', '#1a73e8')
  } else {
    root.removeAttribute('data-theme')
    root.style.setProperty('--surface-container-lowest', '#0f0d13')
    root.style.setProperty('--on-surface', '#e6e0e9')
    root.style.setProperty('--primary', '#8ab4f8')
  }
}

describe('the terminal palette', () => {
  beforeEach(() => {
    document.documentElement.removeAttribute('data-theme')
    document.documentElement.style.cssText = ''
  })

  it('takes its surface and text from the application, not a literal', () => {
    // The terminal used to carry a hard-coded Tokyo Night palette, which was a
    // black rectangle in the middle of a white pane in light mode — and a
    // DIFFERENT dark from everything around it in dark mode.
    paint('light')
    const light = terminalTheme()

    paint('dark')
    const dark = terminalTheme()

    expect(light.background).toBe('#ffffff')
    expect(dark.background).toBe('#0f0d13')
    expect(light.foreground).toBe('#1d1b20')
    expect(dark.foreground).toBe('#e6e0e9')
  })

  it('follows the theme attribute', () => {
    paint('dark')
    expect(isLightTheme()).toBe(false)

    paint('light')
    expect(isLightTheme()).toBe(true)
  })

  it('uses a different ANSI set per theme', () => {
    // A palette legible on near-black is not legible on near-white. Red has to
    // read as red on both.
    paint('dark')
    const dark = terminalTheme()
    paint('light')
    const light = terminalTheme()

    expect(dark.red).not.toBe(light.red)
    expect(dark.green).not.toBe(light.green)
  })

  it('draws the cursor in the application accent', () => {
    paint('light')
    expect(terminalTheme().cursor).toBe('#1a73e8')
  })

  it('keeps the selection translucent so the text stays readable', () => {
    // xterm paints the selection OVER the glyphs. A solid fill hides the very
    // text somebody selected in order to read.
    paint('dark')
    expect(terminalTheme().selectionBackground).toMatch(/^rgba\(/)
  })

  it('still returns a usable theme when no palette is present', () => {
    // A component constructed before the stylesheet has applied must not get
    // an empty background, which xterm renders as transparent black.
    document.documentElement.style.cssText = ''
    const theme = terminalTheme()

    expect(theme.background).not.toBe('')
    expect(theme.foreground).not.toBe('')
  })
})
