/**
 * The command palette's query grammar.
 *
 * Free text is a plain fuzzy search — see `$lib/palette/rank` — over
 * whichever result groups are showing. Three PILLS narrow it to one
 * dimension, so "kind:pods nginx" reads as "search Pods for nginx" rather
 * than five letters fuzzy-matched against everything the palette knows at
 * once:
 *
 *   kind:<name>     scope the object search to one kind — the trigger for
 *                   the single on-demand ListTable read $stores/palette
 *                   makes; see that module's own comment for why it is
 *                   exactly one.
 *   ns:<name>       scope to one namespace.
 *   cluster:<name>  scope to one open cluster tab.
 *
 * A leading `>` — k9s' and VS Code's own convention for "commands only" —
 * restricts the result to the Commands group, for an operator who already
 * knows they want an ACTION and does not want to wade through kinds and
 * namespaces that happen to share the same letters.
 *
 * Pills are recognised as whole whitespace-separated tokens, never as a
 * substring of a longer word — "backing:store" is not read as an `ns:` pill,
 * because its key is not one of the three this grammar knows. The key is
 * matched case-insensitively; the VALUE keeps whatever case was typed,
 * because matching it is `$lib/palette/rank`'s job and that is already
 * case-insensitive.
 */

/** One parsed palette query. */
export interface ParsedPaletteQuery {
  /** True when the query opened with '>' — restricts results to Commands. */
  commandsOnly: boolean
  /** The `kind:` pill's value. `undefined` means no such pill was typed;
      an empty string means the pill is there with nothing after it yet. */
  kind?: string
  /** The `ns:` pill's value, on the same terms as `kind`. */
  namespace?: string
  /** The `cluster:` pill's value, on the same terms as `kind`. */
  cluster?: string
  /** Whatever free text is left after '>' and every pill are removed, with
      internal whitespace collapsed to single spaces and the ends trimmed. */
  text: string
}

/** Matches a whole token against one of the three known pill keys. Anchored
    at both ends so a token has to BE a pill, not merely contain one. */
const PILL_PATTERN = /^(kind|ns|cluster):(.*)$/i

/**
 * Parses one palette query. Never throws — an operator mid-keystroke can
 * type anything, and the worst a malformed pill should do is fall back to
 * being read as ordinary free text.
 */
export function parsePaletteQuery(input: string): ParsedPaletteQuery {
  let rest = input.trimStart()

  let commandsOnly = false
  if (rest.startsWith('>')) {
    commandsOnly = true
    rest = rest.slice(1)
  }

  const words: string[] = []
  let kind: string | undefined
  let namespace: string | undefined
  let cluster: string | undefined

  for (const token of rest.split(/\s+/).filter(Boolean)) {
    const match = PILL_PATTERN.exec(token)
    if (!match) {
      words.push(token)
      continue
    }

    const [, key, value] = match
    switch (key.toLowerCase()) {
      case 'kind':
        kind = value
        break
      case 'ns':
        namespace = value
        break
      case 'cluster':
        cluster = value
        break
    }
  }

  return { commandsOnly, kind, namespace, cluster, text: words.join(' ') }
}
