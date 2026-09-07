/*
Package random provides utility functions for generating random bytes, numeric
identifiers, UID/UUID values, hexadecimal/base36 IDs, and configurable random
strings.

# Randomness Source

By default, [New] uses [crypto/rand.Reader], which is suitable for
security-sensitive randomness. A custom [io.Reader] can be supplied directly to
[New] for testing or specialized environments.

The non-failing helpers ([Rnd.RandUint32], [Rnd.RandUint64] and [Rnd.UUIDv7],
plus [Rnd.RandHex64], [Rnd.RandString64], [Rnd.UID64] and [Rnd.UID128], which
are built on them) fall back to math/rand/v2 if the reader fails, so that their
signatures can stay error-free. [Rnd.RandomBytes] and [Rnd.RandString] never
fall back: they return the reader's error.

With the default [crypto/rand.Reader] the fallback is unreachable, because its
Read cannot return an error. It is reachable for a caller-supplied reader, and
it is silent by default: the entropy source is swapped without an error and
without a signal. Register [WithFallbackHook] to observe it. The fallback draws
from Go's OS-seeded ChaCha8 global source, so the output is not predictable, but
it is no longer the source the caller configured.

A reader that never makes progress (one that keeps returning zero bytes with a
nil error, or whose bytes are never usable) is not retried forever: the helpers
give up and report [ErrReaderNoProgress] rather than hanging.

# What It Provides

  - [Rnd.RandomBytes] for raw random byte slices.
  - [Rnd.RandUint32] and [Rnd.RandUint64] for random integers.
  - [Rnd.RandHex64] for fixed-length 16-char hexadecimal IDs.
  - [Rnd.RandString64] for compact base-36 IDs.
  - [Rnd.RandString] for random strings of length n using a configurable
    byte-to-character map.
  - [Rnd.UID64] for time-aware 64-bit unique identifiers ([TUID64]) with
    [TUID64.Hex] and [TUID64.String] formats.
  - [Rnd.UID128] for time+random 128-bit unique identifiers ([TUID128]) with
    [TUID128.Hex] and [TUID128.String] formats.
  - [Rnd.UUIDv7] for RFC 9562 UUID version 7 values ([UUID]).

# Character Map Customization

[Rnd.RandString] uses a default map containing digits, uppercase/lowercase
letters, and symbols. You can override it with [WithByteToCharMap].

The map is a map of bytes, not of runes: each output position is filled with one
byte drawn from it. Entries must therefore be single-byte (ASCII) values.

  - Multi-byte UTF-8 runes are not supported. A map containing them is not
    rejected, but its runes are split into their constituent bytes, which are then
    drawn independently, so the result is almost always invalid UTF-8.
  - Empty map input restores the default map.
  - Maps longer than 256 bytes are truncated to 256.
  - A single-entry map yields a constant string with no entropy.

# Usage

	r := random.New(nil) // default: crypto/rand.Reader

	id := r.RandHex64()
	short := r.RandString64()
	_ = id
	_ = short

	pwd, err := r.RandString(24)
	if err != nil {
	    return err
	}
	_ = pwd

	uid64 := r.UID64()
	uid128 := r.UID128()
	uuid := r.UUIDv7()
	_ = uid64.Hex()
	_ = uid128.String()
	_ = uuid.String()

	alphaNum := []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	r2 := random.New(nil, random.WithByteToCharMap(alphaNum))
	_, _ = r2.RandString(16)

# When To Use

  - You need UUIDs, hex IDs, or base36 IDs without adding a dedicated
    dependency.
  - Random strings must be drawn from a specified alphabet.
*/
package random

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"strconv"

	"github.com/tecnickcom/nurago/pkg/uhex"
)

var (
	// ErrNegativeLength is returned when a negative length is requested from a
	// size-taking helper such as [Rnd.RandomBytes] or [Rnd.RandString].
	ErrNegativeLength = errors.New("random: negative length")

	// ErrReaderNoProgress is returned when the configured [io.Reader] cannot be
	// made to yield usable entropy: either it keeps returning zero bytes with a
	// nil error, or (in [Rnd.RandString]) every byte it produces is rejected by
	// the rejection sampler. Both are legal [io.Reader] behaviors that would
	// otherwise loop forever, so they are reported instead of retried without
	// bound. It is unreachable with the default [crypto/rand.Reader].
	ErrReaderNoProgress = errors.New("random: reader is not producing usable entropy")

	// ErrInvalidCharMap is returned by [Rnd.RandString] when the character map is
	// empty or longer than 256 bytes. [New] and [WithByteToCharMap] both normalize
	// the map, so this is reachable only through the unusable zero value of [Rnd].
	ErrInvalidCharMap = errors.New("random: character map must hold 1 to 256 bytes")
)

// Character sets for random string generation.
const (
	chrDigits     = "0123456789"
	chrUppercase  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	chrLowercase  = "abcdefghijklmnopqrstuvwxyz"
	chrSymbols    = "!#$%&()*+,-./:;<=>?@[]^_{|}~" // (exclude "\"'\\`")
	chrMapDefault = chrDigits + chrUppercase + chrLowercase + chrSymbols

	// byteRange is the number of distinct values a single byte can hold. It is the
	// source of both the rejection limit in [Rnd.RandString] and the maximum useful
	// character-map length: entries beyond the 256th cannot be addressed by a byte,
	// and a map longer than this would drive the rejection limit to zero, rejecting
	// every byte forever. The two must not drift apart, hence the single constant.
	byteRange = 256

	// chrMapMaxLen is the longest character map [WithByteToCharMap] will accept.
	chrMapMaxLen = byteRange

	// maxEmptyReads bounds how many consecutive zero-byte, nil-error reads are
	// tolerated before a reader is declared stuck. [io.Reader] permits such reads
	// ("nothing happened"), and [io.ReadFull] retries them forever, so a reader
	// that never makes progress would hang the caller with no way out.
	maxEmptyReads = 100

	// maxStalledPasses bounds how many consecutive rejection-sampling passes may
	// accept no byte at all before the reader is declared stuck. The worst-case
	// acceptance probability is ~0.504 (a 129-entry map), so a well-behaved reader
	// stalls this many times with probability ~0.496^128, i.e. never.
	maxStalledPasses = 128
)

// Rnd defines the random number generator.
//
// The zero value is not usable: construct one with [New], which installs the
// default reader and character map. Methods on a zero-value Rnd return
// [ErrInvalidCharMap] or panic on the nil reader.
//
// A single Rnd is safe for concurrent use: it is immutable after construction
// and every method allocates its own buffers. [WithByteToCharMap] copies the
// caller's character map, so a caller retaining the original slice cannot mutate
// the generator through it.
type Rnd struct {
	reader   io.Reader
	chrMap   []byte
	fallback func()
}

// New constructs a random generator using the specified reader (or crypto/rand.Reader if nil) with optional customization.
func New(r io.Reader, opts ...Option) *Rnd {
	if r == nil {
		r = rand.Reader
	}

	rnd := &Rnd{
		reader: r,
		chrMap: []byte(chrMapDefault),
	}

	for _, applyOpt := range opts {
		applyOpt(rnd)
	}

	return rnd
}

// readFull fills buf from the reader, and is io.ReadFull except that it refuses
// to spin forever on a reader that makes no progress.
//
// io.ReadFull retries a (0, nil) read indefinitely, which turns a degenerate but
// legal io.Reader into an unkillable 100%-CPU loop inside an otherwise ordinary
// call. Bounding the consecutive empty reads converts that hang into
// ErrReaderNoProgress. All other semantics are preserved: short reads are
// retried, a reader that ends early yields io.ErrUnexpectedEOF, an empty buf
// never touches the reader, and a read that completes the buffer succeeds even
// if it also reports an error.
func readFull(r io.Reader, buf []byte) error {
	var read, empty int

	for read < len(buf) {
		n, err := r.Read(buf[read:])
		read += n

		switch {
		case read >= len(buf):
			return nil
		case err != nil:
			return readError(err, read)
		case n > 0:
			empty = 0
		default:
			empty++

			if empty >= maxEmptyReads {
				return ErrReaderNoProgress
			}
		}
	}

	return nil
}

// readError maps a reader error on a partially filled buffer, mirroring
// io.ReadFull: ending early after some bytes is an unexpected EOF, and any other
// failure is passed through for the caller to wrap with its own context.
func readError(err error, read int) error {
	if errors.Is(err, io.EOF) && read > 0 {
		return io.ErrUnexpectedEOF
	}

	return err
}

// RandomBytes generates a slice of n random bytes, returning error if the reader fails.
//
// The destination is always fully populated: reads are repeated until it is full,
// so a custom [io.Reader] that returns short reads is retried rather than
// silently truncating the randomness, and a reader that ends early yields
// [io.ErrUnexpectedEOF]. A reader that never makes progress yields
// [ErrReaderNoProgress] rather than looping forever. A negative n returns
// [ErrNegativeLength] instead of panicking; n == 0 returns an empty slice and
// does not touch the reader.
//
// Unlike [Rnd.RandUint32] and [Rnd.RandUint64], this never falls back to
// math/rand/v2: a reader failure is surfaced to the caller.
func (r *Rnd) RandomBytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, ErrNegativeLength
	}

	b := make([]byte, n)

	err := readFull(r.reader, b)
	if err != nil {
		return nil, fmt.Errorf("unable to generate %d random bytes: %w", n, err)
	}

	return b, nil
}

// RandUint32 returns a random 32-bit value, falling back to math/rand/v2 if the reader fails.
//
// The fallback is silent by default; register [WithFallbackHook] to observe it.
func (r *Rnd) RandUint32() uint32 {
	b, err := r.RandomBytes(4)
	if err != nil {
		r.notifyFallback()

		return mrand.Uint32() //nolint:gosec
	}

	return binary.LittleEndian.Uint32(b)
}

// RandUint64 returns a random 64-bit value, falling back to math/rand/v2 if the reader fails.
//
// The fallback is silent by default; register [WithFallbackHook] to observe it.
func (r *Rnd) RandUint64() uint64 {
	b, err := r.RandomBytes(8)
	if err != nil {
		r.notifyFallback()

		return mrand.Uint64() //nolint:gosec
	}

	return binary.LittleEndian.Uint64(b)
}

// RandHex64 returns a random 64-bit value as a 16-character hexadecimal string.
func (r *Rnd) RandHex64() string {
	return string(uhex.Hex64(r.RandUint64()))
}

// RandString64 returns a random 64-bit value as a variable-length base-36 string.
//
// It encodes a single value, so it is unambiguous, but it is variable-length;
// callers that need a fixed-width hexadecimal form should use [Rnd.RandHex64].
func (r *Rnd) RandString64() string {
	return strconv.FormatUint(r.RandUint64(), 36)
}

// RandString returns an n-byte random string using the configured character map, suitable for passwords.
//
// Entries are selected with rejection sampling so each character map entry is
// drawn uniformly, even when 256 is not a multiple of the map length. A negative
// n returns [ErrNegativeLength] instead of panicking; n == 0 returns an empty
// string.
//
// Selection is byte-oriented: each of the n output positions holds one byte from
// the map, so n is a length in bytes and the map must contain single-byte (ASCII)
// entries. Multi-byte UTF-8 runes in a custom map (see [WithByteToCharMap]) are
// not supported and are not rejected: their bytes are drawn independently of one
// another, so the returned string is almost always invalid UTF-8. With the
// default map, n bytes is n characters.
func (r *Rnd) RandString(n int) (string, error) {
	if n < 0 {
		return "", ErrNegativeLength
	}

	cmlen := len(r.chrMap)

	// Guard the map length here rather than trusting the normalization in
	// [WithByteToCharMap]: cmlen == 0 would divide by zero below, and cmlen >
	// byteRange would drive limit to 0, rejecting every byte forever.
	if cmlen < 1 || cmlen > byteRange {
		return "", ErrInvalidCharMap
	}

	b := make([]byte, n)

	// limit is the largest multiple of cmlen not exceeding byteRange: random bytes
	// at or above it are rejected to avoid modulo bias.
	limit := byteRange - (byteRange % cmlen)

	// Read entropy into the not-yet-finalized tail of b and map accepted bytes in
	// place towards the front. The write cursor (filled) never runs ahead of the
	// read cursor (j), so reusing b as the entropy scratch is safe: each byte is
	// read into a local before its slot may be overwritten. This keeps the whole
	// operation at one make plus the final string copy, regardless of how many
	// bytes rejection sampling discards, instead of allocating a fresh buffer on
	// every refill pass.
	//
	// A pass that accepts nothing makes no progress. That is expected once in a
	// while (the tail byte can be rejected repeatedly), but a reader that only
	// ever yields rejected bytes would loop forever, so consecutive stalled passes
	// are bounded.
	filled, stalled := 0, 0

	for filled < n {
		start := filled

		err := readFull(r.reader, b[start:])
		if err != nil {
			return "", fmt.Errorf("unable to generate %d random bytes: %w", n-start, err)
		}

		filled = r.mapAccepted(b, start, limit, cmlen)

		if filled > start {
			stalled = 0
			continue
		}

		stalled++

		if stalled >= maxStalledPasses {
			return "", fmt.Errorf("unable to generate %d random bytes: %w", n-start, ErrReaderNoProgress)
		}
	}

	return string(b), nil
}

// mapAccepted maps the entropy in b[start:] onto the character map, writing the
// accepted characters in place from b[start] forwards and returning the new write
// cursor. Bytes at or above limit are rejected to avoid modulo bias.
//
// The write cursor never runs ahead of the read cursor j, and each byte is copied
// into a local before its slot can be overwritten, so reusing b as the entropy
// scratch cannot corrupt the output or reuse a stale byte.
func (r *Rnd) mapAccepted(b []byte, start, limit, cmlen int) int {
	filled := start

	for j := start; j < len(b); j++ {
		v := int(b[j])
		if v >= limit {
			continue
		}

		b[filled] = r.chrMap[v%cmlen]
		filled++
	}

	return filled
}

// notifyFallback reports that the configured reader failed and math/rand/v2 was
// used instead. It is a no-op unless [WithFallbackHook] is set.
func (r *Rnd) notifyFallback() {
	if r.fallback != nil {
		r.fallback()
	}
}
