/**
 * Turns ANSI SGR colour codes in a log line into plain, styled text runs.
 *
 * No dependency, and no HTML: this returns DATA — an array of runs, each
 * carrying the colour and weight to draw it in — never a string of markup.
 * The caller builds DOM or Svelte spans from it and lets the framework
 * escape the text, the same rule `splitOnMatches` and `logFormat` follow.
 * Handing an unescaped string to `{@html}` for a log line an operator does
 * not control is exactly how `<script>` in a container's stdout would run
 * in the application's own window; this module never produces one.
 *
 * Deliberately narrow: the 8 standard and 8 bright foreground colours, bold
 * and reset. A terminal emulator supports cursor movement, 256-colour and
 * true-colour SGR codes, backgrounds, underline, blink and more — none of
 * which this is one, and all of which are silently swallowed rather than
 * misread as text. A tool's coloured log output almost always sticks to the
 * portable 16, and everything else is exactly the kind of escape sequence
 * that has no business surviving into a log pane's DOM.
 */

/** One run of text, with the styling active when it was written. */
export interface AnsiSpan {
  text: string
  /** One of the 16 portable ANSI colour names, or `undefined` for the
      default foreground. */
  color?: AnsiColor
  bold?: boolean
}

export type AnsiColor =
  | 'black'
  | 'red'
  | 'green'
  | 'yellow'
  | 'blue'
  | 'magenta'
  | 'cyan'
  | 'white'
  | 'bright-black'
  | 'bright-red'
  | 'bright-green'
  | 'bright-yellow'
  | 'bright-blue'
  | 'bright-magenta'
  | 'bright-cyan'
  | 'bright-white'

const STANDARD_COLORS: AnsiColor[] = [
  'black',
  'red',
  'green',
  'yellow',
  'blue',
  'magenta',
  'cyan',
  'white',
]

const BRIGHT_COLORS: AnsiColor[] = [
  'bright-black',
  'bright-red',
  'bright-green',
  'bright-yellow',
  'bright-blue',
  'bright-magenta',
  'bright-cyan',
  'bright-white',
]

const ESC = '\x1b'

/**
 * Finds the final byte of a CSI sequence — the first character in the
 * 0x40–0x7E range at or after `start` — per ECMA-48. Malformed input (an
 * escape with no final byte before the string ends) resolves to the end of
 * the string, which callers treat as "not an SGR sequence" rather than loop
 * forever looking for one.
 */
function findCSIFinal(input: string, start: number): number {
  for (let i = start; i < input.length; i++) {
    const code = input.charCodeAt(i)
    if (code >= 0x40 && code <= 0x7e) return i
  }
  return input.length
}

/** Parses one SGR code's effect on the running style. Anything not listed —
    24-bit colour, backgrounds, underline, blink, 256-colour indices — is
    consumed without changing `state`, which is what "strips everything
    else" means: the code disappears, but so does whatever it would have
    drawn, rather than being misread as one of the 16 it is not. */
function applySGR(code: number, state: { color?: AnsiColor; bold: boolean }): void {
  if (code === 0) {
    state.color = undefined
    state.bold = false
  } else if (code === 1) {
    state.bold = true
  } else if (code === 22) {
    state.bold = false
  } else if (code === 39) {
    state.color = undefined
  } else if (code >= 30 && code <= 37) {
    state.color = STANDARD_COLORS[code - 30]
  } else if (code >= 90 && code <= 97) {
    state.color = BRIGHT_COLORS[code - 90]
  }
  // Everything else: recognised as a code, applied as nothing.
}

/** Appends `text` as a run, merging into the previous run when the style has
    not changed — so a line broken across several escape codes that end up
    drawing the same colour becomes one run rather than several identical
    ones sitting side by side. */
function pushRun(
  spans: AnsiSpan[],
  text: string,
  color: AnsiColor | undefined,
  bold: boolean,
): void {
  if (text === '') return
  const last = spans[spans.length - 1]
  if (last && last.color === color && last.bold === bold) {
    last.text += text
    return
  }
  spans.push({ text, color, bold })
}

/**
 * Converts `input` into styled runs, stripping every ANSI escape sequence
 * from the text — SGR colour codes are interpreted into `color`/`bold`,
 * anything else (cursor movement, OSC hyperlinks, 256-colour codes) is
 * removed without effect.
 *
 * Always returns at least one run, mirroring `splitOnMatches`'s own
 * contract, so a caller never has to special-case "no escapes found" or an
 * empty line.
 */
export function ansiToSpans(input: string): AnsiSpan[] {
  const spans: AnsiSpan[] = []
  const state: { color?: AnsiColor; bold: boolean } = { color: undefined, bold: false }

  let i = 0
  while (i < input.length) {
    const escAt = input.indexOf(ESC, i)
    if (escAt === -1) {
      pushRun(spans, input.slice(i), state.color, state.bold)
      break
    }
    if (escAt > i) pushRun(spans, input.slice(i, escAt), state.color, state.bold)

    if (input[escAt + 1] === '[') {
      // CSI: ESC [ params final-byte
      const final = findCSIFinal(input, escAt + 2)
      if (final < input.length && input[final] === 'm') {
        const params = input.slice(escAt + 2, final)
        const codes = params.length === 0 ? [0] : params.split(';').map((p) => Number(p) || 0)
        for (const code of codes) applySGR(code, state)
      }
      // Any other final byte (cursor movement, erase, etc.): stripped, no
      // style change.
      i = final < input.length ? final + 1 : input.length
      continue
    }

    if (input[escAt + 1] === ']') {
      // OSC: ESC ] ... terminated by BEL (\x07) or ST (ESC \). Used for
      // terminal hyperlinks and window titles — never colour, but common
      // enough in real tool output that it needs stripping rather than
      // being read as literal text.
      const bel = input.indexOf('\x07', escAt + 2)
      const st = input.indexOf(ESC + '\\', escAt + 2)
      const candidates = [bel, st].filter((n) => n !== -1)
      const end = candidates.length > 0 ? Math.min(...candidates) : input.length
      const consumed = end === st ? end + 2 : end + 1
      i = Math.min(consumed, input.length)
      continue
    }

    // An escape this parser does not recognise (a lone ESC, or a two-
    // character sequence that is neither CSI nor OSC): drop just the ESC
    // byte and carry on, rather than losing the rest of the line to a
    // sequence it cannot make sense of.
    i = escAt + 1
  }

  if (spans.length === 0) return [{ text: '', color: undefined, bold: false }]
  return spans
}
