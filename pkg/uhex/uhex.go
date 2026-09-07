/*
Package uhex provides fixed-width, lowercase hexadecimal encoders for unsigned
integers and fixed-size byte arrays.

Each input byte is mapped through a 256-entry lookup table to its two lowercase
hexadecimal characters, which are written with a single 16-bit store.

Compared with generic formatting such as [fmt.Sprintf]("%x", v), uhex avoids
reflection and width handling. Compared with [encoding/hex], it focuses on
fixed-width values and exposes helpers that write directly into caller-owned
arrays.

# Output

All encoders produce lowercase hexadecimal.

Integer-based helpers always zero-pad to the full width of the input type:

  - [Hex64] and [Hex64UB] produce 16 bytes.
  - [Hex32] and [Hex32UB] produce 8 bytes.
  - [Hex16] and [Hex16UB] produce 4 bytes.
  - [Hex8] and [Hex8UB] produce 2 bytes.

Byte-array helpers encode each input byte in order, two hex characters per
byte:

  - [Hex64B] and [Hex64BB] encode [8]byte values.
  - [Hex32B] and [Hex32BB] encode [4]byte values.
  - [Hex16B] and [Hex16BB] encode [2]byte values.
  - [Hex8B] and [Hex8BB] encode [1]byte values.

# Allocation Behavior

The slice-returning helpers ([Hex64], [Hex32], [Hex16], [Hex8], [Hex64B],
[Hex32B], [Hex16B], and [Hex8B]) each fill a local array (16, 8, 4, or 2 bytes)
and return a slice over it. Whether that array is allocated on the heap depends
on escape analysis at the call site:

  - If the returned slice does not escape the caller (for example, it is read and
    discarded, or only its bytes are consumed), the array stays on the stack and
    no allocation occurs.
  - If the returned slice escapes (it is stored in a longer-lived structure,
    returned, sent on a channel, or passed to a function the compiler cannot
    inline), the backing array is heap-allocated, costing one allocation of the
    array's width.

For code that must never allocate regardless of escape analysis, prefer the
buffer-writing helpers that accept a destination array pointer. [Hex64UB],
[Hex32UB], [Hex16UB], and [Hex8UB] write integer encodings into caller-owned
buffers. [Hex64BB], [Hex32BB], [Hex16BB], and [Hex8BB] do the same for
fixed-size byte arrays. These never allocate and, for the 32-bit and narrower
widths, are small enough to be inlined into the caller.

# Performance

Because every width is known at compile time, the encoders are fully unrolled:
there are no loops, no length checks, and no argument validation. Each input byte
is translated with a single lookup into a 256-entry table and written with one
16-bit store, so the cost scales linearly with the output width and is
independent of the input value (there are no data-dependent branches).

Relative to the standard library and general-purpose formatting, for the small
fixed widths this package targets:

  - Against [encoding/hex], the buffer-writing helpers are on the order of two to
    three times faster and, like [encoding/hex.Encode], allocate nothing. Part of
    the gain on the integer helpers is structural: [encoding/hex] only encodes
    byte slices, so encoding an integer with it additionally requires serializing
    the value into a scratch buffer first, whereas the integer helpers here read
    the value directly.
  - Against [fmt.Sprintf]("%x", v), the difference is roughly an order of
    magnitude, and uhex avoids the allocations that reflection-based formatting
    incurs.
  - When a []byte or string result is returned rather than written into a
    caller-owned buffer, the single heap allocation for that result dominates the
    total cost, so the advantage over [encoding/hex] narrows accordingly; the
    buffer-writing helpers avoid that allocation entirely.

These are relative characteristics, not guarantees; absolute numbers depend on
the hardware and compiler. The package ships benchmarks (run with
`go test -bench=.`) so the figures can be reproduced on the target platform.

This package is specialized for small, fixed-width values (up to eight bytes).
For arbitrary-length or streaming data, or for decoding, use [encoding/hex],
which is optimized for those cases; uhex offers no advantage there and does not
cover them.

# Naming

Function suffixes follow this pattern:

  - No suffix: encode an unsigned integer and return a []byte.
  - UB: encode an unsigned integer into a caller-provided buffer.
  - B: encode a fixed-size byte array and return a []byte.
  - BB: encode a fixed-size byte array into a caller-provided buffer.

# Usage

	id := uhex.Hex64(traceID)
	sum := uhex.Hex32(checksum)
	tag := string(uhex.Hex8(kind))

	var dst [16]byte
	uhex.Hex64UB(traceID, &dst)
	out := dst[:]

	src := [8]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}
	uhex.Hex64BB(src, &dst)

# When To Use

  - IDs must render as a fixed number of hex digits, zero-padded.
  - The encoding sits on a hot path and should not allocate.
*/
package uhex

import "encoding/binary"

const hexTable = "0123456789abcdef"

// hex16 maps every byte value to its two lowercase ASCII hex characters, packed
// little-endian so that binary.LittleEndian.PutUint16 emits the high nibble
// first. It is written once during package initialization and only read
// afterwards, so it is safe for concurrent use.
//
//nolint:gochecknoglobals // immutable, write-once lookup table for hot-path encoding
var hex16 = buildHex16()

// buildHex16 constructs the byte-to-hex lookup table.
func buildHex16() [256]uint16 {
	var t [256]uint16

	for i := range t {
		t[i] = uint16(hexTable[i>>4]) | uint16(hexTable[i&0xf])<<8
	}

	return t
}

// Hex64 returns the zero-padded, lowercase hexadecimal encoding of n as a 16-byte slice.
func Hex64(n uint64) []byte {
	var dst [16]byte

	Hex64UB(n, &dst)

	return dst[:]
}

// Hex64UB writes the zero-padded, lowercase hexadecimal encoding of n into dst.
func Hex64UB(n uint64, dst *[16]byte) {
	binary.LittleEndian.PutUint16(dst[0:2], hex16[byte(n>>56)])
	binary.LittleEndian.PutUint16(dst[2:4], hex16[byte(n>>48)])
	binary.LittleEndian.PutUint16(dst[4:6], hex16[byte(n>>40)])
	binary.LittleEndian.PutUint16(dst[6:8], hex16[byte(n>>32)])
	binary.LittleEndian.PutUint16(dst[8:10], hex16[byte(n>>24)])
	binary.LittleEndian.PutUint16(dst[10:12], hex16[byte(n>>16)])
	binary.LittleEndian.PutUint16(dst[12:14], hex16[byte(n>>8)])
	binary.LittleEndian.PutUint16(dst[14:16], hex16[byte(n)])
}

// Hex64B returns the lowercase hexadecimal encoding of src as a 16-byte slice.
func Hex64B(src [8]byte) []byte {
	var dst [16]byte

	Hex64BB(src, &dst)

	return dst[:]
}

// Hex64BB writes the lowercase hexadecimal encoding of src into dst.
func Hex64BB(src [8]byte, dst *[16]byte) {
	binary.LittleEndian.PutUint16(dst[0:2], hex16[src[0]])
	binary.LittleEndian.PutUint16(dst[2:4], hex16[src[1]])
	binary.LittleEndian.PutUint16(dst[4:6], hex16[src[2]])
	binary.LittleEndian.PutUint16(dst[6:8], hex16[src[3]])
	binary.LittleEndian.PutUint16(dst[8:10], hex16[src[4]])
	binary.LittleEndian.PutUint16(dst[10:12], hex16[src[5]])
	binary.LittleEndian.PutUint16(dst[12:14], hex16[src[6]])
	binary.LittleEndian.PutUint16(dst[14:16], hex16[src[7]])
}

// Hex32 returns the zero-padded, lowercase hexadecimal encoding of n as an 8-byte slice.
func Hex32(n uint32) []byte {
	var dst [8]byte

	Hex32UB(n, &dst)

	return dst[:]
}

// Hex32UB writes the zero-padded, lowercase hexadecimal encoding of n into dst.
func Hex32UB(n uint32, dst *[8]byte) {
	binary.LittleEndian.PutUint16(dst[0:2], hex16[byte(n>>24)])
	binary.LittleEndian.PutUint16(dst[2:4], hex16[byte(n>>16)])
	binary.LittleEndian.PutUint16(dst[4:6], hex16[byte(n>>8)])
	binary.LittleEndian.PutUint16(dst[6:8], hex16[byte(n)])
}

// Hex32B returns the lowercase hexadecimal encoding of src as an 8-byte slice.
func Hex32B(src [4]byte) []byte {
	var dst [8]byte

	Hex32BB(src, &dst)

	return dst[:]
}

// Hex32BB writes the lowercase hexadecimal encoding of src into dst.
func Hex32BB(src [4]byte, dst *[8]byte) {
	binary.LittleEndian.PutUint16(dst[0:2], hex16[src[0]])
	binary.LittleEndian.PutUint16(dst[2:4], hex16[src[1]])
	binary.LittleEndian.PutUint16(dst[4:6], hex16[src[2]])
	binary.LittleEndian.PutUint16(dst[6:8], hex16[src[3]])
}

// Hex16 returns the zero-padded, lowercase hexadecimal encoding of n as a 4-byte slice.
func Hex16(n uint16) []byte {
	var dst [4]byte

	Hex16UB(n, &dst)

	return dst[:]
}

// Hex16UB writes the zero-padded, lowercase hexadecimal encoding of n into dst.
func Hex16UB(n uint16, dst *[4]byte) {
	binary.LittleEndian.PutUint16(dst[0:2], hex16[byte(n>>8)])
	binary.LittleEndian.PutUint16(dst[2:4], hex16[byte(n)])
}

// Hex16B returns the lowercase hexadecimal encoding of src as a 4-byte slice.
func Hex16B(src [2]byte) []byte {
	var dst [4]byte

	Hex16BB(src, &dst)

	return dst[:]
}

// Hex16BB writes the lowercase hexadecimal encoding of src into dst.
func Hex16BB(src [2]byte, dst *[4]byte) {
	binary.LittleEndian.PutUint16(dst[0:2], hex16[src[0]])
	binary.LittleEndian.PutUint16(dst[2:4], hex16[src[1]])
}

// Hex8 returns the zero-padded, lowercase hexadecimal encoding of n as a 2-byte slice.
func Hex8(n uint8) []byte {
	var dst [2]byte

	Hex8UB(n, &dst)

	return dst[:]
}

// Hex8UB writes the zero-padded, lowercase hexadecimal encoding of n into dst.
func Hex8UB(n uint8, dst *[2]byte) {
	binary.LittleEndian.PutUint16(dst[0:2], hex16[n])
}

// Hex8B returns the lowercase hexadecimal encoding of src as a 2-byte slice.
func Hex8B(src [1]byte) []byte {
	var dst [2]byte

	Hex8BB(src, &dst)

	return dst[:]
}

// Hex8BB writes the lowercase hexadecimal encoding of src into dst.
func Hex8BB(src [1]byte, dst *[2]byte) {
	binary.LittleEndian.PutUint16(dst[0:2], hex16[src[0]])
}
