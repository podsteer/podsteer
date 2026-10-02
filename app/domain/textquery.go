package domain

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// TextQuery is the search box's filter language, parsed — a PORT of
// web/src/lib/query.ts, which every other table still runs in the webview.
//
// The pod table is filtered in Go (see QueryPods) and every other list in
// the frontend, so the two must read one search box identically. They are
// held together by web/src/lib/filter.fixtures.json, which the TypeScript
// and the Go tests both run; a change to the grammar is a change to both and
// to that fixture.
//
// Grammar (terms are whitespace-separated and AND together; a quoted
// "phrase with spaces" is one term):
//
//	word            case-insensitive substring of the row's text
//	-term           negates any other term form below
//	re:<pattern>    case-insensitive regex over the row's text
//	/pattern/       same; the closing slash may be omitted only on the LAST term
//	key=value       label selector: labels[key] == value
//	key!=value      label selector: labels[key] != value
//	label:key       label presence
//	cluster:name    case-insensitive substring of the row's cluster
//
// ONE KNOWN DIFFERENCE, and it is the regex dialect: Go's RE2 has no
// lookaround and no backreferences, which JavaScript's RegExp has. A pattern
// using them is reported invalid here (Err) and matches nothing, where the
// webview would have run it.
type TextQuery struct {
	terms []queryTerm
	err   error
}

type termKind int

const (
	termText termKind = iota
	termCluster
	termRegex
	termLabelEquals
	termLabelNotEquals
	termLabelPresence
)

type queryTerm struct {
	kind    termKind
	negated bool
	// value is the substring for text and cluster terms (already lowered),
	// and the label value for the selectors.
	value string
	key   string
	// regex is nil for a pattern that failed to compile — kept as a term,
	// so a query holding it matches nothing rather than everything.
	regex *regexp.Regexp
}

// ParseTextQuery parses what an operator typed. It never fails: an invalid
// pattern is recorded on Err and its term matches nothing, because a typo
// mid-pattern must not blank the field or throw.
func ParseTextQuery(text string) TextQuery {
	tokens := tokenizeQuery(text)
	query := TextQuery{terms: make([]queryTerm, 0, len(tokens))}
	for i, token := range tokens {
		term, err := parseQueryTerm(token, i == len(tokens)-1)
		query.terms = append(query.terms, term)
		// Only the first invalid pattern is kept, as in the webview: one
		// broken regex already explains an empty table.
		if err != nil && query.err == nil {
			query.err = err
		}
	}
	return query
}

// IsEmpty reports whether the query has no terms and so matches everything.
func (q TextQuery) IsEmpty() bool { return len(q.terms) == 0 }

// Err is the first invalid pattern's error, or nil.
func (q TextQuery) Err() error { return q.err }

// isQuerySpace is JavaScript's \s, which is Unicode White_Space plus the
// byte-order mark.
func isQuerySpace(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }

// tokenizeQuery splits on whitespace, keeping a double-quoted span — spaces
// and all, quotes included — as one token.
func tokenizeQuery(text string) []string {
	var tokens []string
	runes := []rune(text)
	n := len(runes)
	for i := 0; i < n; {
		for i < n && isQuerySpace(runes[i]) {
			i++
		}
		if i >= n {
			break
		}

		var token strings.Builder
		for i < n && !isQuerySpace(runes[i]) {
			if runes[i] == '"' {
				token.WriteRune(runes[i])
				i++
				for i < n && runes[i] != '"' {
					token.WriteRune(runes[i])
					i++
				}
				if i < n {
					token.WriteRune(runes[i]) // the closing quote, if there was one
					i++
				}
				continue
			}
			token.WriteRune(runes[i])
			i++
		}
		tokens = append(tokens, token.String())
	}
	return tokens
}

func regexQueryTerm(pattern string, negated bool) (queryTerm, error) {
	compiled, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return queryTerm{kind: termRegex, negated: negated}, fmt.Errorf("invalid regex %q: %w", pattern, err)
	}
	return queryTerm{kind: termRegex, negated: negated, regex: compiled}, nil
}

// parseQueryTerm classifies one token. isLast is consulted only for the
// unclosed /pattern form.
//
// Every test below is on ASCII characters, so byte indexing here agrees with
// the UTF-16 indexing the webview does.
func parseQueryTerm(raw string, isLast bool) (queryTerm, error) {
	body := raw
	negated := false
	if len(body) > 1 && body[0] == '-' {
		negated = true
		body = body[1:]
	}

	// A quoted phrase is always literal, whatever it contains.
	if len(body) >= 2 && body[0] == '"' && body[len(body)-1] == '"' {
		return queryTerm{kind: termText, negated: negated, value: strings.ToLower(body[1 : len(body)-1])}, nil
	}

	if pattern, ok := strings.CutPrefix(body, "re:"); ok {
		return regexQueryTerm(pattern, negated)
	}

	if len(body) > 1 && body[0] == '/' {
		inner := body[1:]
		if pattern, ok := strings.CutSuffix(inner, "/"); ok {
			return regexQueryTerm(pattern, negated)
		}
		if isLast {
			return regexQueryTerm(inner, negated)
		}
		// No closing slash and something follows: a literal path fragment,
		// via the label checks and the plain-text fallback below.
	}

	if value, ok := strings.CutPrefix(body, "cluster:"); ok {
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		return queryTerm{kind: termCluster, negated: negated, value: strings.ToLower(value)}, nil
	}

	// key!=value ahead of key=value, so it is not mistaken for one.
	if at := strings.Index(body, "!="); at > 0 {
		return queryTerm{kind: termLabelNotEquals, negated: negated, key: body[:at], value: body[at+2:]}, nil
	}
	if at := strings.Index(body, "="); at > 0 {
		return queryTerm{kind: termLabelEquals, negated: negated, key: body[:at], value: body[at+1:]}, nil
	}

	if key, ok := strings.CutPrefix(body, "label:"); ok {
		return queryTerm{kind: termLabelPresence, negated: negated, key: key}, nil
	}

	return queryTerm{kind: termText, negated: negated, value: strings.ToLower(body)}, nil
}

// queryRow is one row as the language sees it: the concatenated searchable
// text (and its lower-case form, computed once), the labels, the cluster.
type queryRow struct {
	text    string
	lowered string
	labels  map[string]string
	cluster string
}

// matches reports whether row satisfies every term.
//
// An absent label map is "no labels", so a positive label term never matches
// such a row and a negated one always does.
func (q TextQuery) matches(row queryRow) bool {
	for _, term := range q.terms {
		// An invalid pattern has nothing to run; skipping it would turn
		// "-re:(" into "show everything" and hide the mistake.
		if term.kind == termRegex && term.regex == nil {
			return false
		}
		if term.evaluate(row) == term.negated {
			return false
		}
	}
	return true
}

func (t queryTerm) evaluate(row queryRow) bool {
	switch t.kind {
	case termText:
		return strings.Contains(row.lowered, t.value)
	case termCluster:
		return strings.Contains(strings.ToLower(row.cluster), t.value)
	case termRegex:
		return t.regex.MatchString(row.text)
	case termLabelEquals:
		value, ok := row.labels[t.key]
		return ok && value == t.value
	case termLabelNotEquals:
		value, ok := row.labels[t.key]
		return !ok || value != t.value
	case termLabelPresence:
		_, ok := row.labels[t.key]
		return ok
	default:
		return false
	}
}
