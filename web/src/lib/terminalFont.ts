/**
 * The terminal's font stack.
 *
 * Out here rather than inline in the xterm constructor for the reason
 * `terminalTheme.ts` is: the ORDER carries a decision, and a decision that only
 * exists as one long string literal inside a component is one nobody can argue
 * with in a test.
 *
 * WHY NERD FONTS COME FIRST. A prompt built with Powerlevel10k, Starship or
 * oh-my-posh draws its separators and icons from the Private Use Area, and none
 * of the plain families below has a glyph there. On a stack of only plain
 * families those characters resolve to nothing and a prompt renders as a row of
 * boxes — which is exactly what happened in this pane before, in a terminal
 * that is otherwise faithful. Putting the Nerd names first means an operator
 * who has one gets it for everything, which is also the only way the icons line
 * up with the text beside them.
 *
 * MesloLGS NF leads because it is the font Powerlevel10k's own configuration
 * wizard installs, so it is the one most likely to be sitting on a machine
 * whose prompt needs it.
 *
 * WHY THE PLAIN FAMILIES STAY. They are the fallback for everybody else, and
 * they are unchanged. This must not become a stack that only works for people
 * who installed something.
 *
 * WHY THE SYMBOLS-ONLY PATCH IS LAST. `Symbols Nerd Font Mono` contains no
 * letters or digits at all, so leading with it would hand xterm.js a first
 * family it cannot measure a cell from. Where it is, per-glyph fallback still
 * reaches it for exactly the characters nothing above could draw — which gives
 * an operator who patched only the symbols their icons in their own text font.
 *
 * NOTHING HERE IS BUNDLED. PodSteer ships no font and downloads none; this
 * names families a machine may already have, the same rule the local terminal
 * follows for kubectl and coding agents.
 */
export const TERMINAL_FONT_FAMILIES = [
  'MesloLGS NF',
  'JetBrainsMono Nerd Font',
  'FiraCode Nerd Font',
  'Hack Nerd Font',
  'CaskaydiaCove Nerd Font',
  'JetBrains Mono',
  'Fira Code',
  'Cascadia Code',
  'Monaco',
  'Menlo',
  'Ubuntu Mono',
  'Symbols Nerd Font Mono',
  'monospace',
] as const

/**
 * The stack as CSS wants it.
 *
 * Every multi-word family is quoted and the generic keyword is not, because a
 * quoted `"monospace"` is a request for a family called monospace rather than
 * for the generic one — the difference between a guaranteed last resort and a
 * name that may match nothing at all.
 */
export const TERMINAL_FONT_STACK = TERMINAL_FONT_FAMILIES.map((family) =>
  family === 'monospace' ? family : `"${family}"`,
).join(', ')
