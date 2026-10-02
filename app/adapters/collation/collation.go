// Package collation supplies the Unicode root collation's character weights
// to the domain's table order (domain.RuneWeight).
//
// The domain splits text into digit runs and characters and compares them —
// see app/domain/textorder.go — but where a character sits in the Unicode
// order is a table the standard library does not carry, and the domain may
// import nothing else. golang.org/x/text does carry it, so this adapter is
// the one place it is read.
//
// WHY NOT x/text's OWN NUMERIC COMPARISON. collate.Numeric mis-orders digit
// runs that are a lone zero — "10.0.0.12" sorted after "10.0.1.1", which is
// exactly the IP column — so only the per-character weights are taken from
// it and the numeric runs are compared by the domain. Checked against the
// webview's Intl.Collator on several thousand random pairs (none differed)
// and by the shared pod fixture in web/src/lib/filter.fixtures.json.
package collation

import (
	"bytes"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// The webview's collator: numeric, sensitivity "base" — case, accents and
// width are not differences.
var (
	mu       sync.Mutex
	collator = collate.New(language.Und, collate.IgnoreCase, collate.IgnoreDiacritics, collate.IgnoreWidth)
	buffer   collate.Buffer
	// learned holds the weights computed so far beyond ASCII, which is
	// precomputed below. Characters are few and repeat endlessly, so this
	// stays small.
	learned = map[rune]uint64{}
	ascii   [utf8.RuneSelf]uint64
)

func init() {
	for r := range rune(utf8.RuneSelf) {
		ascii[r] = compute(r)
	}
}

// Weight is r's primary weight: equal for characters differing only in case,
// accent or width, zero for a character the collation ignores. Safe for
// concurrent use.
func Weight(r rune) uint64 {
	if r >= 0 && r < utf8.RuneSelf {
		return ascii[r]
	}

	mu.Lock()
	defer mu.Unlock()
	if w, ok := learned[r]; ok {
		return w
	}
	w := compute(r)
	learned[r] = w
	return w
}

// compute reads r's primary weights out of its collation key and packs the
// first eight bytes, big-endian and left-aligned, so comparing the integers
// compares the keys. Callers hold mu, or run before anything else can.
func compute(r rune) uint64 {
	var encoded [utf8.UTFMax]byte
	n := utf8.EncodeRune(encoded[:], r)
	key := collator.Key(&buffer, encoded[:n])
	defer buffer.Reset()

	// The primary level ends at the first 0x0000 separator; with case and
	// accents ignored, nothing after it is a difference at base strength.
	if end := bytes.Index(key, []byte{0, 0}); end >= 0 {
		key = key[:end]
	}

	var w uint64
	for i := range 8 {
		w <<= 8
		if i < len(key) {
			w |= uint64(key[i])
		}
	}
	return w
}
