/**
 * The filter language behind every table's search field.
 *
 * `parseQuery` turns whatever an operator typed into a small AND-of-terms
 * structure once per keystroke (debounced — see `ClusterSession.setSearch`);
 * `matches` then answers one row at a time, cheaply, against the already-parsed
 * result. Parsing per row would repeat the same regex compile and tokenising
 * work for every pod in the list on every render, for no different an answer.
 *
 * No Svelte import belongs here — this is the same reason `textSearch.ts` is a
 * plain module: the grammar is arguable on its own, in a table-driven test,
 * without a component around it.
 *
 * Grammar (terms are whitespace-separated and AND together; a quoted
 * "phrase with spaces" is one term):
 *
 *   word            case-insensitive substring of `Row.text`
 *   -term           negates any other term form below
 *   re:<pattern>    case-insensitive regex over `Row.text`
 *   /pattern/       same, k9s-style; the closing `/` may be omitted only on
 *                   the LAST term, so filtering starts before it is typed
 *   key=value       label selector: row.labels[key] === value
 *   key!=value      label selector: row.labels[key] !== value
 *   label:key       label presence: key in row.labels
 *   cluster:name    case-insensitive substring of `Row.cluster`, the cluster
 *                   the row came from. Made for the merged All-clusters
 *                   tables; every row belongs to one cluster, so it also
 *                   holds, trivially, on a single cluster's own list
 *
 * Plain words stay plain substring matches — no prefix, no change from
 * before this module existed — because the overwhelming case is an operator
 * pasting part of a name they already have, and `=` almost never appears in
 * that. Reserving `=`/`!=`/`label:`/`re:`/`/…/` as prefixes costs nothing a
 * real search term was likely to type.
 */

/** The minimal shape a table's rows need in order to be filtered. */
export interface Row {
  /** The concatenated searchable cell text the table already builds — the
      same fields the plain-substring filter compared against before this
      module existed. */
  text: string
  /** The row's labels, when its DTO carries them (Pod, Workload). Absent for
      every kind that does not — Nodes, Namespaces, Events, Applications, and
      every server-printed generic table row. */
  labels?: Record<string, string>
  /** The cluster the row came from. Every list in PodSteer belongs to one,
      so callers pass the tab's own cluster for a single-cluster list and the
      row's own for a merged one; absent, a `cluster:` term never matches. */
  cluster?: string
}

/** One parsed term. A discriminated union rather than one bag of optional
    fields, so a switch over `kind` narrows without a manual assertion. */
type Term =
  | { kind: 'text'; negated: boolean; value: string }
  | { kind: 'cluster'; negated: boolean; value: string }
  | { kind: 'regex'; negated: boolean; pattern: string; regex: RegExp | null }
  | { kind: 'labelEquals'; negated: boolean; key: string; value: string }
  | { kind: 'labelNotEquals'; negated: boolean; key: string; value: string }
  | { kind: 'labelPresence'; negated: boolean; key: string }

/** A parsed query: terms to AND together, and an error when one of them
    could not be understood. */
export interface Query {
  terms: Term[]
  /** Set when a `re:`/`/…/` term's pattern failed to compile. Short enough to
      sit in the search field's `title`/aria-description directly. */
  error?: string
}

/**
 * Splits `text` on whitespace, keeping a double-quoted span — spaces and
 * all — as one token including its quote characters, so `parseTerm` can tell
 * a quoted phrase from an unquoted one afterwards.
 *
 * Character-by-character rather than a regex split: a naive `.split(/\s+/)`
 * has no idea it is inside quotes and would cut `"a b"` into two tokens.
 *
 * Exported for the one caller that edits a query rather than parsing it —
 * the All-clusters strip toggling a `cluster:` term (see `$lib/fleet`) — so
 * what it treats as one term is exactly what this grammar does.
 */
export function tokenize(text: string): string[] {
  const tokens: string[] = []
  const n = text.length
  let i = 0

  while (i < n) {
    while (i < n && /\s/.test(text[i])) i++
    if (i >= n) break

    let token = ''
    while (i < n && !/\s/.test(text[i])) {
      if (text[i] === '"') {
        token += text[i]
        i++
        while (i < n && text[i] !== '"') {
          token += text[i]
          i++
        }
        if (i < n) {
          token += text[i] // the closing quote, if the input had one
          i++
        }
      } else {
        token += text[i]
        i++
      }
    }
    tokens.push(token)
  }

  return tokens
}

/** Compiles a regex term, catching an invalid pattern instead of letting it
    throw out of `parseQuery`. An invalid pattern keeps its term (`regex:
    null`) rather than being dropped, so `matches` still has something to
    fail consistently on — see the comment there. */
function regexTerm(pattern: string, negated: boolean): { term: Term; error?: string } {
  try {
    return { term: { kind: 'regex', negated, pattern, regex: new RegExp(pattern, 'i') } }
  } catch (cause) {
    const reason = cause instanceof Error ? cause.message : String(cause)
    return {
      term: { kind: 'regex', negated, pattern, regex: null },
      error: `Invalid regex "${pattern}": ${reason}`,
    }
  }
}

/**
 * Classifies one token into a `Term`.
 *
 * `isLast` is only consulted for the unclosed `/pattern` form — the one place
 * position in the input changes the grammar, so that typing `/foo` starts
 * filtering as a regex immediately rather than waiting for a second `/` that
 * has not been typed yet.
 */
function parseTerm(raw: string, isLast: boolean): { term: Term; error?: string } {
  let body = raw
  let negated = false
  if (body.length > 1 && body[0] === '-') {
    negated = true
    body = body.slice(1)
  }

  // A quoted phrase is always literal, whatever it contains — somebody who
  // typed "re:whatever" in quotes wants those literal characters, not a
  // regex, and quoting is precisely how they say so.
  if (body.length >= 2 && body[0] === '"' && body[body.length - 1] === '"') {
    return { term: { kind: 'text', negated, value: body.slice(1, -1) } }
  }

  if (body.startsWith('re:')) {
    return regexTerm(body.slice(3), negated)
  }

  if (body.startsWith('/') && body.length > 1) {
    const inner = body.slice(1)
    if (inner.endsWith('/')) {
      return regexTerm(inner.slice(0, -1), negated)
    }
    if (isLast) {
      return regexTerm(inner, negated)
    }
    // No closing slash and something follows: not a regex form after all —
    // falls through to the label checks and then the plain-text fallback
    // below, so e.g. "/var/log" typed ahead of another term reads as a
    // literal path fragment rather than an error.
  }

  // cluster:name. Ahead of the label forms because a kubeconfig context name
  // is free-form — an EKS ARN carries colons and slashes, a GKE one
  // underscores — and nothing after the prefix may be read as anything but
  // the name. Quotes around it are stripped, so a name with a space can be
  // given the same way a phrase is.
  if (body.startsWith('cluster:')) {
    let value = body.slice('cluster:'.length)
    if (value.length >= 2 && value[0] === '"' && value[value.length - 1] === '"') {
      value = value.slice(1, -1)
    }
    return { term: { kind: 'cluster', negated, value } }
  }

  // key!=value, checked ahead of key=value so it is not mistaken for one.
  const notEqualsAt = body.indexOf('!=')
  if (notEqualsAt > 0) {
    return {
      term: {
        kind: 'labelNotEquals',
        negated,
        key: body.slice(0, notEqualsAt),
        value: body.slice(notEqualsAt + 2),
      },
    }
  }

  // key=value. No reserved prefix needed ahead of it: "=" is not a character
  // a plain search term has reason to contain, so seeing one is already the
  // signal that this is a label selector rather than a name fragment.
  const equalsAt = body.indexOf('=')
  if (equalsAt > 0) {
    return {
      term: {
        kind: 'labelEquals',
        negated,
        key: body.slice(0, equalsAt),
        value: body.slice(equalsAt + 1),
      },
    }
  }

  if (body.startsWith('label:')) {
    return { term: { kind: 'labelPresence', negated, key: body.slice('label:'.length) } }
  }

  // Plain substring: the fallback every other form did not claim, and the
  // overwhelming common case. See the module comment for why this stays the
  // simplest form rather than growing a prefix of its own.
  return { term: { kind: 'text', negated, value: body } }
}

/** Parses a search field's text into a `Query`. Never throws — an invalid
    regex is recorded on `query.error` rather than escaping as an exception,
    because a typo mid-pattern must not blank the field or crash the table
    while it is still being typed. */
export function parseQuery(text: string): Query {
  const tokens = tokenize(text)
  const terms: Term[] = []
  let error: string | undefined

  tokens.forEach((token, index) => {
    const parsed = parseTerm(token, index === tokens.length - 1)
    terms.push(parsed.term)
    // Only the first invalid pattern is kept. One broken regex already
    // explains an empty table; stacking a second and third message behind it
    // would read as a wall of text instead of one thing to fix.
    if (parsed.error && !error) error = parsed.error
  })

  return error === undefined ? { terms } : { terms, error }
}

/** The un-negated predicate for one term against one row. `matches` applies
    negation and the invalid-regex short-circuit around this. */
function evaluate(term: Term, row: Row): boolean {
  switch (term.kind) {
    case 'text':
      return row.text.toLowerCase().includes(term.value.toLowerCase())
    case 'cluster':
      // A row with no cluster matches no cluster term and every negated one
      // — the same reading an absent label map gets. A substring rather than
      // an exact name, because the name is whatever the kubeconfig context
      // is called and "prod" is what somebody types, not
      // "gke_acme_europe-west1_prod"; the status strip's chips insert the
      // full name for the exact case.
      return (row.cluster ?? '').toLowerCase().includes(term.value.toLowerCase())
    case 'regex':
      // regex is only null when the pattern failed to compile, and matches()
      // never reaches this branch in that case — see the guard there.
      return term.regex !== null && term.regex.test(row.text)
    case 'labelEquals':
      // `?? {}` is what makes a labelless row behave as "no labels at all"
      // rather than needing its own branch: an absent key is never equal to
      // a specific value, so this is naturally false without saying so twice.
      return (row.labels ?? {})[term.key] === term.value
    case 'labelNotEquals':
      // The mirror of labelEquals, and by the same `?? {}` a labelless row
      // naturally satisfies it — an absent key is never equal to anything,
      // which is exactly what "!=" asks.
      return (row.labels ?? {})[term.key] !== term.value
    case 'labelPresence':
      return term.key in (row.labels ?? {})
  }
}

/**
 * Whether `row` satisfies every term of `query` — the AND the grammar
 * promises.
 *
 * A row's `labels` being `undefined` is treated as "has no labels" rather
 * than as "labels unknown": a positive label term (`key=value`, `label:key`)
 * never matches such a row, and a negated one always does, which is the
 * ordinary reading of "this pod carries no such label".
 */
export function matches(query: Query, row: Row): boolean {
  for (const term of query.terms) {
    // An invalid pattern has no RegExp to run at all. Skipping the term
    // would silently turn "-re:(" into "show everything"; letting `evaluate`
    // return false and then negating it would do the same for a negated
    // invalid pattern. Both would hide the mistake instead of surfacing it —
    // the field's error state (query.error) is what is supposed to explain
    // an empty table, not a table that quietly stopped filtering.
    if (term.kind === 'regex' && term.regex === null) return false

    const positive = evaluate(term, row)
    const satisfied = term.negated ? !positive : positive
    if (!satisfied) return false
  }
  return true
}

/** A one-line human summary of `query`, for the search field's tooltip and
    aria-description — e.g. "3 terms: substring, regex, label". */
export function describeQuery(query: Query): string {
  if (query.terms.length === 0) return 'Matches everything'

  const parts = query.terms.map((term) => {
    const name = categoryName(term)
    return term.negated ? `not ${name}` : name
  })

  const count = query.terms.length
  return `${count} term${count === 1 ? '' : 's'}: ${parts.join(', ')}`
}

function categoryName(term: Term): string {
  switch (term.kind) {
    case 'text':
      return 'substring'
    case 'cluster':
      return 'cluster'
    case 'regex':
      return term.regex === null ? 'invalid regex' : 'regex'
    case 'labelEquals':
    case 'labelNotEquals':
    case 'labelPresence':
      return 'label'
  }
}
