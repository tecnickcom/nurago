/*
Package stringkey derives a stable, compact, non-cryptographic key from multiple
text fields for lookup, deduplication, and idempotency-style identifiers.

# How It Works

[New] builds a [StringKey] from one or more input fields using this pipeline:

 1. Trim leading and trailing Unicode whitespace for each field.
 2. Collapse repeated Unicode whitespace to a single space.
 3. Convert text to lowercase.
 4. Concatenate fields using a tab separator (`\t`) to preserve boundaries.
 5. Apply Unicode NFC normalization (equivalent to NFD then NFC).
 6. Hash the resulting byte sequence with FarmHash64.

The resulting key is stored as an internal uint64 and can be retrieved in
multiple representations:

  - [StringKey.Key]: raw uint64 value.
  - [StringKey.String]: base-36 string (short, variable length).
  - [StringKey.Hex]: fixed 16-character lowercase hexadecimal string.

Two normalization details are worth calling out:

  - Normalization is NFC, not NFKC. Canonically equivalent forms (such as
    precomposed vs decomposed accents) collapse together, but compatibility
    variants do not: ligatures (for example "ﬀ"), full-width vs half-width
    forms, and super/subscripts are left distinct.
  - Lowercasing uses the locale-independent [unicode.ToLower] simple mapping,
    not language-tailored case folding. For example, the Turkish dotless "ı"
    and dotted "İ" are not special-cased.

# Stability

For a fixed input, the key is fully deterministic and does not change across
runs, architectures, or Go versions: FarmHash64 is a fixed algorithm and the
representations are plain integer encodings.

The one dependency to be aware of is the Unicode normalization step, which uses
the Unicode data tables shipped by golang.org/x/text. Keys for pure-ASCII input
and for long-assigned characters are effectively permanent. Only rare or
newly-assigned code points can normalize differently if the golang.org/x/text
Unicode version advances, which would change their keys across such an upgrade.
If you persist keys and must survive Unicode-table upgrades unchanged, pin the
golang.org/x/text version alongside the stored keys.

# Important Limits

This package is not cryptographically secure and must not be used for security
tokens, signatures, or untrusted collision-resistant identifiers.

It is designed for reasonably small input sizes and a moderate number of keys.
According to the birthday bound for a 64-bit hash space (~1.8x10^19):

  - collision probability is about 1% around 6.1x10^8 generated keys;
  - collision probability is about 50% around 5.1x10^9 generated keys.

Choose a larger hash or cryptographic construction when your scale or threat
model requires stronger collision guarantees.

# When To Use

  - A composite lookup key or deduplication key must be built from multiple
    fields.
  - The key needs to be stable across processes and releases.
*/
package stringkey

import (
	"bytes"
	"strconv"
	"unicode"

	farmhash64 "github.com/tecnickcom/farmhash64/go/src"
	"github.com/tecnickcom/nurago/pkg/uhex"
	"golang.org/x/text/unicode/norm"
)

// StringKey stores the encoded key.
type StringKey struct {
	key uint64
}

// New constructs deterministic FarmHash64 key from normalized input strings (whitespace/case/Unicode normalized).
func New(fields ...string) *StringKey {
	// Pre-size the buffer: worst case is every input byte plus one tab
	// separator per field. The hash output must remain byte-stable because it is
	// used as a dedup/idempotency key, so the normalized bytes are built exactly.
	n := len(fields)
	for _, f := range fields {
		n += len(f)
	}

	var b bytes.Buffer

	b.Grow(n)

	for _, v := range fields {
		appendNormalizedField(&b, v)
		b.WriteByte('\t') // separate input strings
	}

	// norm.NFC.Bytes returns its input unchanged, without allocating, when the
	// bytes are already NFC-normalized (always true for ASCII input). NFC alone
	// is equivalent to chaining NFD then NFC: NFC already decomposes internally
	// before recomposing, so the resulting bytes (and hash) are identical.
	nb := norm.NFC.Bytes(b.Bytes())

	return &StringKey{key: farmhash64.FarmHash64(nb)}
}

// appendNormalizedField lowercases field and collapses each run of leading,
// interior, and trailing Unicode whitespace exactly as strings.Fields joined by
// a single space would, writing the result into b. Doing the lowercasing during
// the collapse is byte-equivalent because unicode.ToLower is a per-rune 1:1 map
// and the space/tab separators are ASCII.
func appendNormalizedField(b *bytes.Buffer, field string) {
	pendingSpace := false // a whitespace run was seen after real content
	started := false      // at least one non-space rune has been written

	for _, r := range field {
		if unicode.IsSpace(r) {
			if started {
				pendingSpace = true
			}

			continue
		}

		if pendingSpace {
			b.WriteByte(' ')

			pendingSpace = false
		}

		started = true

		b.WriteRune(unicode.ToLower(r))
	}
}

// Key returns raw uint64 key value.
func (sk *StringKey) Key() uint64 {
	return sk.key
}

// Hex returns fixed 16-character lowercase hexadecimal representation of key, zero-padded.
func (sk *StringKey) Hex() string {
	return string(uhex.Hex64(sk.key))
}

// String returns variable-length base-36 string representation of key.
func (sk *StringKey) String() string {
	return strconv.FormatUint(sk.key, 36)
}
