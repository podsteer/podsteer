// Package collation supplies the Unicode root collation's primary keys to
// the domain's table order (domain.CollationKey).
//
// The domain splits text into digit runs and the runs between them and
// compares them — see app/domain/textorder.go — but collating those runs
// needs the Unicode collation tables, which the standard library does not
// carry and the domain may import nothing else to get. golang.org/x/text
// does carry them, so this adapter is the one place they are read.
//
// WHY NOT x/text's OWN NUMERIC COMPARISON. collate.Numeric mis-orders digit
// runs that are a lone zero — "10.0.0.12" sorted after "10.0.1.1", which is
// exactly the IP column — so the digits are compared by the domain and only
// the runs between them are collated here. Held to the webview's
// Intl.Collator by the shared pod fixture in web/src/lib/filter.fixtures.json.
//
// WHOLE RUNS, VARIABLE LENGTH. A run is collated as a run, not character by
// character, so expansions and contractions are the collation's own — a
// ligature compares as its letters, a Hangul syllable as its jamo — and the
// key is as long as the weights it holds, never cut to a fixed width.
package collation

import (
	"bytes"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// newCollator is the webview's collator: base strength — case, accents and
// width are not differences.
func newCollator() *collate.Collator {
	return collate.New(language.Und, collate.IgnoreCase, collate.IgnoreDiacritics, collate.IgnoreWidth)
}

type worker struct {
	collator *collate.Collator
	buffer   collate.Buffer
}

// workers holds collators for concurrent use: a Collator and its Buffer are
// not safe to share, and two page queries can sort at once.
var workers = sync.Pool{New: func() any { return &worker{collator: newCollator()} }}

// ascii holds each ASCII character's primary key. The root collation has no
// contractions among ASCII characters, so an ASCII run's key is these joined
// — no collator, no lock, no allocation beyond the result.
var ascii [utf8.RuneSelf][]byte

func init() {
	w := &worker{collator: newCollator()}
	for c := range utf8.RuneSelf {
		ascii[c] = w.primary(string(rune(c)))
	}
}

// Key is the primary collation key of text, a run holding no decimal digit.
// Safe for concurrent use.
func Key(text string) []byte {
	if isASCII(text) {
		size := 0
		for i := range len(text) {
			size += len(ascii[text[i]])
		}
		key := make([]byte, 0, size)
		for i := range len(text) {
			key = append(key, ascii[text[i]]...)
		}
		return key
	}

	w := workers.Get().(*worker)
	defer workers.Put(w)
	return w.primary(text)
}

// primary collates text and keeps the primary level: everything before the
// first 0x0000 separator. With case, accents and width ignored, nothing after
// it is a difference at base strength.
func (w *worker) primary(text string) []byte {
	defer w.buffer.Reset()
	key := w.collator.KeyFromString(&w.buffer, text)
	if end := bytes.Index(key, []byte{0, 0}); end >= 0 {
		key = key[:end]
	}
	return bytes.Clone(key)
}

func isASCII(text string) bool {
	for i := range len(text) {
		if text[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
