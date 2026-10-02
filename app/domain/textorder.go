package domain

import (
	"cmp"
	"strings"
	"unicode/utf8"
)

// The order a table sorts text in — a port of the webview's
// `new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })`
// (web/src/lib/sort.ts), which every other list still sorts with.
//
// Three properties, each something an operator reads:
//
//   - NUMERIC: a run of digits compares by its value, so pod-2 sorts before
//     pod-10 and 10.0.0.2 before 10.0.0.12. Leading zeros are not a
//     difference: "01" and "1" are the same number.
//   - BASE SENSITIVITY: case and accents are not differences, so "api",
//     "API" and "ápi" are equal and keep the order they arrived in.
//   - THE UNICODE ROOT ORDER for everything else: whitespace, then
//     punctuation and symbols, then digits, then letters.
//
// The algorithm — splitting into digit runs and characters, comparing
// element by element — lives here. The one thing it cannot do with the
// standard library alone is know a character's place in the Unicode order,
// so that is handed in as a RuneWeight; app/adapters/collation provides the
// real one from golang.org/x/text. Checked against the browser's own
// collator on several thousand random pairs and by the shared pod fixture.

// RuneWeight is a character's primary weight in the Unicode root collation:
// equal for characters that differ only in case or accent, ordered as the
// collation orders them, and zero for a character the order ignores
// altogether (a combining mark, a control character).
type RuneWeight func(r rune) uint64

// textElement is one comparable piece of a string: a run of digits, or one
// character's weight.
type textElement struct {
	weight uint64
	// digits is a digit run with its leading zeros removed; meaningful only
	// when numeric is set.
	digits  string
	numeric bool
}

// textKey is a string prepared for comparison. Built once per row, so a
// sort compares integers rather than re-reading the text on every
// comparison — a five-thousand-row sort is some sixty thousand of them.
type textKey []textElement

func newTextKey(text string, weight RuneWeight) textKey {
	key := make(textKey, 0, len(text))
	for i := 0; i < len(text); {
		if c := text[i]; c >= '0' && c <= '9' {
			j := i
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				j++
			}
			run := text[i:j]
			for len(run) > 0 && run[0] == '0' {
				run = run[1:]
			}
			key = append(key, textElement{numeric: true, digits: run})
			i = j
			continue
		}

		r, size := utf8.DecodeRuneInString(text[i:])
		if w := weight(r); w != 0 {
			key = append(key, textElement{weight: w})
		}
		i += size
	}
	return key
}

// compareTextKeys orders two prepared strings.
//
// A digit run against a character compares as the digit '0' does, because
// in the collation every digit sits in one block between punctuation and
// letters.
func compareTextKeys(a, b textKey, digit uint64) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if order := compareTextElements(a[i], b[i], digit); order != 0 {
			return order
		}
	}
	return cmp.Compare(len(a), len(b))
}

func compareTextElements(a, b textElement, digit uint64) int {
	switch {
	case a.numeric && b.numeric:
		if len(a.digits) != len(b.digits) {
			return cmp.Compare(len(a.digits), len(b.digits))
		}
		return strings.Compare(a.digits, b.digits)
	case a.numeric:
		return cmp.Compare(digit, b.weight)
	case b.numeric:
		return cmp.Compare(a.weight, digit)
	default:
		return cmp.Compare(a.weight, b.weight)
	}
}
