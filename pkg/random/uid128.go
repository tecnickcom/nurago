package random

import (
	"strconv"
	"time"

	"github.com/tecnickcom/nurago/pkg/uhex"
)

// TUID128 is a time-ordered 128-bit identifier: the high 64 bits (t) are the
// generation time in Unix nanoseconds and the low 64 bits (r) are random.
type TUID128 struct {
	t uint64
	r uint64
}

// UID128 generates a 128-bit unique identifier with high 64 bits from current time and low 64 bits random.
func (r *Rnd) UID128() TUID128 {
	return TUID128{
		t: uint64(time.Now().UnixNano()),
		r: r.RandUint64(),
	}
}

// Format writes the UID128 as its 32-character hexadecimal form into dst.
//
// The output is the high 64 bits (time) followed by the low 64 bits (random),
// each as 16 lowercase hexadecimal digits, preserving time-order.
//
// Format writes directly into the caller-provided array and allocates nothing,
// so prefer it in performance-critical code. For a string use [TUID128.Hex]; for
// a byte slice use [TUID128.Byte].
func (u TUID128) Format(dst *[32]byte) {
	uhex.Hex64UB(u.t, (*[16]byte)(dst[0:16]))
	uhex.Hex64UB(u.r, (*[16]byte)(dst[16:32]))
}

// Byte returns the UID128 as its 32-character hexadecimal form in a byte slice.
//
// Byte fills a local [32]byte array and returns a slice over it, so whether it
// allocates depends on escape analysis at the call site: if the returned slice
// does not escape the caller (it is read and discarded, or only its bytes are
// consumed), the array stays on the stack and no allocation occurs; if it escapes
// (stored, returned, sent on a channel, or passed to a function the compiler
// cannot inline), the array is heap-allocated. Use [TUID128.Hex] for a string, or
// [TUID128.Format] to write into a pre-allocated buffer and never allocate
// regardless of escape analysis.
func (u TUID128) Byte() []byte {
	var b [32]byte

	u.Format(&b)

	return b[:]
}

// Hex returns the UID128 as a 32-character hexadecimal string, preserving time-order.
//
// It encodes both halves into a single stack buffer with the table-driven uhex
// helpers and converts once, so it costs a single allocation for the returned
// string. Use [TUID128.Format] for an allocation-free path.
func (u TUID128) Hex() string {
	return string(u.Byte())
}

// String returns the UID128 as a variable-length base-36 string (concatenated high and low parts).
//
// This form is intended for display only and is NOT guaranteed to be unique or
// round-trippable: because both parts are variable-length and concatenated
// without a separator, two distinct TUID128 values can map to the same string.
// Callers that need a unique, collision-free, round-trippable representation
// should use [TUID128.Hex], which is fixed-width.
func (u TUID128) String() string {
	// Append both base-36 halves into a single stack buffer and convert once,
	// rather than formatting two strings and concatenating them. A uint64 is at
	// most 13 digits in base 36, so 26 bytes always suffice for the two halves.
	// The output is byte-identical to FormatUint(t)+FormatUint(r).
	var b [26]byte

	buf := strconv.AppendUint(b[:0], u.t, 36)
	buf = strconv.AppendUint(buf, u.r, 36)

	return string(buf)
}
