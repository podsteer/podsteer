import { describe, expect, it } from 'vitest'
import { describeQuery, matches, parseQuery, type Row } from './query'

/** A row with the given text and, optionally, labels. */
function row(text: string, labels?: Record<string, string>): Row {
  return labels === undefined ? { text } : { text, labels }
}

/** Shorthand: does `text` (as a query) match `target`? */
function hit(query: string, target: Row): boolean {
  return matches(parseQuery(query), target)
}

describe('plain substring terms', () => {
  it('matches a case-insensitive substring of the text', () => {
    expect(hit('web', row('web-api-7c9'))).toBe(true)
    expect(hit('WEB', row('web-api-7c9'))).toBe(true)
    expect(hit('missing', row('web-api-7c9'))).toBe(false)
  })

  it('negates with a leading dash', () => {
    expect(hit('-web', row('web-api-7c9'))).toBe(false)
    expect(hit('-web', row('db-primary'))).toBe(true)
  })

  it('is the fallback for a lone "-" or "/", which have nothing to negate or close', () => {
    // Neither has a form of its own to complete, so both stay literal
    // characters rather than turning into an empty negation or regex.
    expect(hit('-', row('a - b'))).toBe(true)
    expect(hit('/', row('a / b'))).toBe(true)
  })
})

describe('quoted phrases', () => {
  it('is one term even with spaces inside', () => {
    expect(hit('"api server"', row('the api server pod'))).toBe(true)
    expect(hit('"api server"', row('api and server, unrelated'))).toBe(false)
  })

  it('negates like any other term', () => {
    expect(hit('-"api server"', row('the api server pod'))).toBe(false)
    expect(hit('-"api server"', row('unrelated'))).toBe(true)
  })

  it('is always literal, even when it looks like another form', () => {
    // Quoting is how an operator asks for the characters themselves —
    // "re:foo" in quotes must not be parsed as a regex term.
    expect(hit('"re:foo"', row('re:foo'))).toBe(true)
    expect(hit('"re:foo"', row('foo'))).toBe(false)
  })
})

describe('regex terms', () => {
  it('re:<pattern> matches a case-insensitive regex over the text', () => {
    expect(hit('re:^web-', row('web-api-7c9'))).toBe(true)
    expect(hit('re:^WEB-', row('web-api-7c9'))).toBe(true)
    expect(hit('re:^api-', row('web-api-7c9'))).toBe(false)
  })

  it('/pattern/ is the same form', () => {
    expect(hit('/^web-/', row('web-api-7c9'))).toBe(true)
    expect(hit('/^api-/', row('web-api-7c9'))).toBe(false)
  })

  it('negates like any other term', () => {
    expect(hit('-re:^web-', row('web-api-7c9'))).toBe(false)
    expect(hit('-re:^web-', row('db-primary'))).toBe(true)
  })

  it('/pattern may omit the closing slash only as the last term', () => {
    expect(hit('/^web-', row('web-api-7c9'))).toBe(true)
    // Not the last term, and never closed: falls back to a literal
    // substring search for "/^web-" itself, which matches nothing here.
    expect(hit('/^web- extra', row('web-api-7c9'))).toBe(false)
  })

  it('an invalid pattern records a short query.error instead of throwing', () => {
    expect(() => parseQuery('re:(unterminated')).not.toThrow()
    const query = parseQuery('re:(unterminated')
    expect(query.error).toBeTruthy()
    expect(query.error?.length).toBeLessThan(120)
  })

  it('a term with an invalid pattern matches nothing, negated or not', () => {
    // "Matches nothing" is a property of the TERM, not of its negation —
    // -re:( must not silently become "show everything" just because the
    // pattern could not compile.
    expect(hit('re:(unterminated', row('anything at all'))).toBe(false)
    expect(hit('-re:(unterminated', row('anything at all'))).toBe(false)
  })
})

describe('label terms', () => {
  it('key=value selects on an exact label value', () => {
    expect(hit('app=web', row('pod-1', { app: 'web' }))).toBe(true)
    expect(hit('app=web', row('pod-1', { app: 'db' }))).toBe(false)
  })

  it('key!=value selects its complement', () => {
    expect(hit('app!=web', row('pod-1', { app: 'db' }))).toBe(true)
    expect(hit('app!=web', row('pod-1', { app: 'web' }))).toBe(false)
  })

  it('label:key selects on presence alone', () => {
    expect(hit('label:app', row('pod-1', { app: 'web' }))).toBe(true)
    expect(hit('label:app', row('pod-1', { tier: 'web' }))).toBe(false)
  })

  it('negates like any other term', () => {
    expect(hit('-app=web', row('pod-1', { app: 'web' }))).toBe(false)
    expect(hit('-app=web', row('pod-1', { app: 'db' }))).toBe(true)
    expect(hit('-label:app', row('pod-1', { app: 'web' }))).toBe(false)
    expect(hit('-label:app', row('pod-1', {}))).toBe(true)
  })

  it('a row with no labels never matches a positive label term', () => {
    expect(hit('app=web', row('pod-1'))).toBe(false)
    expect(hit('label:app', row('pod-1'))).toBe(false)
  })

  it('a row with no labels always matches the negation of one', () => {
    expect(hit('-app=web', row('pod-1'))).toBe(true)
    expect(hit('-label:app', row('pod-1'))).toBe(true)
  })

  it('a row with no labels matches key!=value, since an absent key is never equal to anything', () => {
    expect(hit('app!=web', row('pod-1'))).toBe(true)
  })
})

describe('AND-ing multiple terms', () => {
  it('requires every term to match', () => {
    const target = row('web-api-7c9', { app: 'web', tier: 'frontend' })
    expect(hit('web app=web', target)).toBe(true)
    expect(hit('web app=db', target)).toBe(false)
    expect(hit('web -tier=backend re:api', target)).toBe(true)
  })
})

describe('empty and whitespace-only queries', () => {
  it('an empty query matches everything', () => {
    expect(hit('', row('anything'))).toBe(true)
    expect(hit('', row('anything', {}))).toBe(true)
  })

  it('a whitespace-only query matches everything', () => {
    expect(hit('   ', row('anything'))).toBe(true)
    expect(parseQuery('   ').terms).toHaveLength(0)
  })
})

describe('describeQuery', () => {
  it('summarises an empty query', () => {
    expect(describeQuery(parseQuery(''))).toBe('Matches everything')
  })

  it('names one term', () => {
    expect(describeQuery(parseQuery('web'))).toBe('1 term: substring')
  })

  it('names several terms in order, marking negation', () => {
    expect(describeQuery(parseQuery('web -re:^db app=web'))).toBe(
      '3 terms: substring, not regex, label',
    )
  })

  it('names an invalid regex distinctly from a valid one', () => {
    expect(describeQuery(parseQuery('re:(unterminated'))).toBe('1 term: invalid regex')
  })
})

describe('cluster: terms', () => {
  /** A row that came from a named cluster. */
  function from(cluster: string, text = 'web-0'): Row {
    return { text, cluster }
  }

  it("matches a case-insensitive substring of the row's cluster, never of its text", () => {
    expect(hit('cluster:prod', from('gke_acme_europe-west1_prod'))).toBe(true)
    expect(hit('cluster:PROD', from('prod'))).toBe(true)
    // The text carries the word and the cluster does not: no match.
    expect(hit('cluster:prod', from('staging', 'prod-web-0'))).toBe(false)
  })

  it('keeps everything after the prefix as the name, colons and slashes included', () => {
    const arn = 'arn:aws:eks:eu-west-1:123456789012:cluster/prod'
    expect(hit(`cluster:${arn}`, from(arn))).toBe(true)
    expect(hit('cluster:cluster/prod', from(arn))).toBe(true)
  })

  it('accepts a quoted name, for a context name with a space in it', () => {
    expect(hit('cluster:"my cluster"', from('my cluster'))).toBe(true)
    expect(hit('cluster:"my cluster"', from('my-cluster'))).toBe(false)
  })

  it('never matches a row that belongs to no cluster, and always matches it negated', () => {
    expect(hit('cluster:prod', row('web-0'))).toBe(false)
    expect(hit('-cluster:prod', row('web-0'))).toBe(true)
  })

  it('negates like any other term and ANDs with the rest', () => {
    expect(hit('-cluster:prod', from('prod'))).toBe(false)
    expect(hit('-cluster:prod', from('dev'))).toBe(true)
    expect(hit('cluster:prod web', from('prod'))).toBe(true)
    expect(hit('cluster:prod db', from('prod'))).toBe(false)
  })

  it('is described as a cluster term', () => {
    expect(describeQuery(parseQuery('cluster:prod -cluster:dev'))).toBe(
      '2 terms: cluster, not cluster',
    )
  })
})
