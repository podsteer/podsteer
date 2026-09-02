/**
 * The terminal's colours, taken from the application's own palette.
 *
 * WHY THIS IS NOT A LITERAL. The terminal used to carry a hard-coded Tokyo
 * Night palette — a dark background nailed into the component. That was wrong
 * in light mode, where a black rectangle sat in the middle of a white pane, and
 * it was wrong in dark mode too: it was a *different* dark from every surface
 * around it, so the pane read as an embedded foreign object rather than as part
 * of the application.
 *
 * Everything here is read from the CSS custom properties in `app.css` at the
 * moment it is asked for, so the terminal follows a theme change for the same
 * reason the rest of the interface does, and a palette edit reaches it without
 * anybody remembering this file exists.
 */

/** Reads one custom property, falling back when the document has none. */
function token(styles: CSSStyleDeclaration, name: string, fallback: string): string {
  const value = styles.getPropertyValue(name).trim()
  return value === '' ? fallback : value
}

/**
 * The ANSI sixteen, and why they are not the palette.
 *
 * The interface's own colours are chosen to sit quietly against a surface;
 * ANSI colours are chosen to be told apart from each other at a glance, which
 * is a different job. A shell that prints an error in red needs a red that
 * reads as red, not `--error` at whatever contrast the theme wanted for a
 * banner.
 *
 * So the SURFACE, the FOREGROUND, the CURSOR and the SELECTION come from the
 * application — those are what make the pane look like part of it — and the
 * sixteen stay a deliberate, legible set. They are tuned per theme because a
 * palette that works on a near-black surface is unreadable on a near-white
 * one.
 */
const ANSI_DARK = {
  black: '#3c3a41',
  red: '#f2837f',
  green: '#7dc98a',
  yellow: '#e0a45c',
  blue: '#8ab4f8',
  magenta: '#d7a3f0',
  cyan: '#6fd0e3',
  white: '#d8d3dd',
  brightBlack: '#6f6a77',
  brightRed: '#ff9f9b',
  brightGreen: '#95e3a2',
  brightYellow: '#f2bd76',
  brightBlue: '#a8c9ff',
  brightMagenta: '#e9bcff',
  brightCyan: '#8ce3f5',
  brightWhite: '#f5f0f7',
} as const

const ANSI_LIGHT = {
  black: '#1f1d24',
  red: '#c5372f',
  green: '#1f7a3f',
  yellow: '#8a5a00',
  blue: '#1a73e8',
  magenta: '#8b3fb8',
  cyan: '#0b6b7d',
  white: '#5f5a67',
  brightBlack: '#8c8794',
  brightRed: '#e04b41',
  brightGreen: '#2a9553',
  brightYellow: '#a86f0a',
  brightBlue: '#3d8bf2',
  brightMagenta: '#a355cc',
  brightCyan: '#0f849b',
  brightWhite: '#1f1d24',
} as const

/** Whether the document is currently rendering the light theme. */
export function isLightTheme(): boolean {
  return document.documentElement.getAttribute('data-theme') === 'light'
}

/**
 * Builds the xterm theme for whichever palette is live.
 *
 * `surface-container-lowest` rather than `surface`: it is the recessed end of
 * the ramp, which is what a terminal should read as — a well the output sits
 * in — and in light mode it is plain white, where anything darker would look
 * like a disabled field.
 */
export function terminalTheme(): Record<string, string> {
  const styles = getComputedStyle(document.documentElement)
  const light = isLightTheme()

  const background = token(styles, '--surface-container-lowest', light ? '#ffffff' : '#0f0d13')
  const foreground = token(styles, '--on-surface', light ? '#1d1b20' : '#e6e0e9')
  const primary = token(styles, '--primary', light ? '#1a73e8' : '#8ab4f8')

  return {
    background,
    foreground,
    cursor: primary,
    // The character under the block cursor, which must contrast with the
    // cursor rather than with the background.
    cursorAccent: background,
    // The selection is a TRANSLUCENT wash, not a solid fill: xterm draws it
    // over the glyphs, and a solid primary would hide the text somebody is
    // selecting in order to read.
    selectionBackground: light ? 'rgba(26, 115, 232, 0.22)' : 'rgba(138, 180, 248, 0.30)',
    ...(light ? ANSI_LIGHT : ANSI_DARK),
  }
}

/**
 * Calls back whenever the theme changes.
 *
 * The theme is a `data-theme` attribute on the root element, so the change is
 * observable without the application telling anyone about it — which matters
 * because the terminal is mounted deep inside a drawer and should not need
 * wiring to a store to stay the right colour.
 *
 * Returns the unsubscribe.
 */
export function onThemeChange(handle: () => void): () => void {
  const observer = new MutationObserver(handle)
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-theme'],
  })
  return () => observer.disconnect()
}
