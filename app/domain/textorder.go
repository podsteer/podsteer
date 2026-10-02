package domain

import (
	"cmp"
	"strings"
	"unicode"
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
//     punctuation and symbols, then digits, then letters — with the
//     collation's own expansions and contractions (a ligature is its
//     letters, a Hangul syllable its jamo).
//
// The digit runs are this file's: it splits text into runs of decimal digits
// (in any script) and runs of everything else and compares them in turn. What it cannot do
// with the standard library alone is collate the runs of everything else, so
// that is handed in as a CollationKey; app/adapters/collation provides the
// real one from golang.org/x/text. Held to the browser's own collator by the
// shared pod fixture and by thousands of random pairs.

// CollationKey is the Unicode root collation's PRIMARY key of a run of text
// that holds no decimal digit: a byte string whose lexicographic order is the
// collation's order at base strength, built from whole collation weights —
// so a key that is a prefix of another ends exactly where a weight does. A
// run of nothing but ignorable characters has an empty key.
type CollationKey func(text string) []byte

// textElement is one comparable piece of a string: a run of digits, or the
// collation key of a run of anything else.
type textElement struct {
	// key is the collation key of a non-digit run.
	key string
	// digits is a digit run with its leading zeros removed; meaningful only
	// when numeric is set.
	digits  string
	numeric bool
}

// textKey is a string prepared for comparison. Built once per row, so a
// sort compares bytes rather than re-collating the text on every
// comparison — a five-thousand-row sort is some sixty thousand of them.
type textKey []textElement

// digitValue is a decimal digit's value, in any script — "٣" is 3, as
// "3" is — and false for anything else. Unicode lays every script's digits
// out as runs of ten consecutive code points from zero, so the value is the
// distance from the start of the run, modulo ten (some scripts' runs sit
// back to back).
func digitValue(r rune) (byte, bool) {
	if r >= '0' && r <= '9' {
		return byte(r - '0'), true
	}
	if r < utf8.RuneSelf || !unicode.IsDigit(r) {
		return 0, false
	}
	start := r
	for unicode.IsDigit(start - 1) {
		start--
	}
	return byte((r - start) % 10), true
}

func newTextKey(text string, collation CollationKey) textKey {
	key := make(textKey, 0, 4)
	var digits []byte
	textStart := -1

	flushText := func(end int) {
		if textStart < 0 {
			return
		}
		if collated := collation(text[textStart:end]); len(collated) > 0 {
			key = append(key, textElement{key: string(collated)})
		}
		textStart = -1
	}
	flushDigits := func() {
		if digits == nil {
			return
		}
		run := strings.TrimLeft(string(digits), "0")
		key = append(key, textElement{numeric: true, digits: run})
		digits = nil
	}

	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if value, ok := digitValue(r); ok {
			flushText(i)
			digits = append(digits, '0'+value)
		} else {
			flushDigits()
			if textStart < 0 {
				textStart = i
			}
		}
		i += size
	}
	flushText(len(text))
	flushDigits()
	return key
}

// compareTextKeys orders two prepared strings.
//
// THE TEXT RUNS DO NOT LINE UP ONE FOR ONE. "ab" is one run and "a1b" is
// three, and the collation compares "ab" with "a1b" character by character:
// a = a, then b against the digit. So a text run is compared with the other
// side's as far as the shorter goes, and whatever is left of the longer is
// compared with what follows the shorter — a digit run, which sorts as the
// digit '0' does (every digit sits in one block between punctuation and
// letters), or the end of the string, which sorts first.
func compareTextKeys(a, b textKey, digit string) int {
	var restA, restB string // what is left of a partly compared text run
	i, j := 0, 0
	for {
		if restA == "" && i < len(a) && !a[i].numeric {
			restA = a[i].key
			i++
		}
		if restB == "" && j < len(b) && !b[j].numeric {
			restB = b[j].key
			j++
		}

		switch {
		case restA != "" && restB != "":
			n := min(len(restA), len(restB))
			if order := strings.Compare(restA[:n], restB[:n]); order != 0 {
				return order
			}
			restA, restB = restA[n:], restB[n:]
			continue
		case restA != "":
			if j == len(b) {
				return 1
			}
			// A digit run against text: as the digit '0'.
			if order := compareWeightPrefix(restA, digit); order != 0 {
				return order
			}
			restA = restA[min(len(digit), len(restA)):]
			j++
			continue
		case restB != "":
			if i == len(a) {
				return -1
			}
			if order := compareWeightPrefix(restB, digit); order != 0 {
				return -order
			}
			restB = restB[min(len(digit), len(restB)):]
			i++
			continue
		}

		// Both sides are at an element boundary with no text pending.
		switch {
		case i == len(a) && j == len(b):
			return 0
		case i == len(a):
			return -1
		case j == len(b):
			return 1
		}
		// Both are digit runs: by value, which for runs without leading
		// zeros is by length, then digit by digit.
		if order := cmp.Compare(len(a[i].digits), len(b[j].digits)); order != 0 {
			return order
		}
		if order := strings.Compare(a[i].digits, b[j].digits); order != 0 {
			return order
		}
		i++
		j++
	}
}

// compareWeightPrefix compares text's next weight with a digit's.
func compareWeightPrefix(text, digit string) int {
	n := min(len(text), len(digit))
	if order := strings.Compare(text[:n], digit[:n]); order != 0 {
		return order
	}
	return cmp.Compare(len(text[:n]), len(digit))
}
