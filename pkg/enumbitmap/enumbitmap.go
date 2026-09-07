/*
Package enumbitmap encodes a set of enumeration values as an integer bitmap and
decodes it back.

Each bit corresponds to a unique enumeration value. The package processes up to
32 bit positions (1<<0 through 1<<31).

Contract:

Enum IDs must be distinct single-bit powers of two in the range 1<<0 through
1<<31. An ID of 0, or any multi-bit value, cannot be represented as a set bit and
is therefore never produced by BitMapToStrings; such IDs make encoding and
decoding asymmetric and should be avoided. The value 0 always decodes to an empty
slice.

Portability:

Values are treated as the low 32 bits of a host int. Bit 31 is the sign bit on
platforms where int is 32 bits wide, so this package is intended for 64-bit
platforms; on a 32-bit build, inputs at or above 1<<31 behave in
implementation-defined ways and should be avoided.

Example with 8 bits:

	00000000 =   0 dec = NONE
	00000001 =   1 dec = FIRST
	00000010 =   2 dec = SECOND
	00000100 =   4 dec = THIRD
	00001000 =   8 dec = FOURTH
	00010000 =  16 dec = FIFTH
	00100000 =  32 dec = SIXTH
	01000000 =  64 dec = SEVENTH
	10000000 = 128 dec = EIGHTH
	00001001 = 1 + 8 = 9 dec = FIRST + FOURTH

# When To Use

  - A set of flags is stored in one integer column or transmitted as one
    number.
  - You want the mapping between names and bit positions kept in one place.
*/
package enumbitmap

import (
	"errors"
	"fmt"
)

const (
	// maxBit is the maximum supported number of bits.
	// It is also the maximum number of items that can be represented with a single integer.
	maxBit = 32
)

var (
	// ErrUnknownBitValues is returned by BitMapToStrings when v has set bits that
	// are not present in the enum map. Any known bits are still returned, so the
	// partial slice remains usable for tolerant parsing. Match it with errors.Is.
	ErrUnknownBitValues = errors.New("enumbitmap: unknown bit values")

	// ErrUnknownStringValues is returned by StringsToBitMap when s contains names
	// that are not present in the enum map. Any known names are still combined into
	// the returned bitmap. Match it with errors.Is.
	ErrUnknownStringValues = errors.New("enumbitmap: unknown string values")
)

// BitMapToStrings expands a bitmap value into its enum names.
//
// Unknown set bits are reported in the returned error as their bit mask values
// (1, 2, 4, …), while known values are still returned, enabling tolerant
// parsing and diagnostics.
//
// Only the lowest 32 bits (masks 1<<0 through 1<<31) are inspected; any bits set
// at position 32 or higher are silently ignored.
//
// When one or more set bits are missing from enum, the returned error wraps
// ErrUnknownBitValues and lists the unknown masks, while the known names are still
// returned.
func BitMapToStrings(enum map[int]string, v int) ([]string, error) {
	if v == 0 {
		return []string{}, nil
	}

	s := make([]string, 0, maxBit)
	errBits := make([]int, 0, maxBit)

	for bit := range maxBit {
		mask := 1 << bit
		if v&mask == 0 {
			continue
		}

		if name, ok := enum[mask]; ok {
			s = append(s, name)
		} else {
			errBits = append(errBits, mask)
		}
	}

	var err error

	if len(errBits) > 0 {
		err = fmt.Errorf("%w: %v", ErrUnknownBitValues, errBits)
	}

	return s, err
}

// StringsToBitMap converts enum names into a combined bitmap value.
//
// Unknown names are aggregated in the returned error, which wraps
// ErrUnknownStringValues, while known values are still included in the bitmap.
//
// The IDs in enum are combined with a bitwise OR and are not validated: for the
// result to round-trip through BitMapToStrings, every ID must be a distinct
// single-bit power of two in the range 1<<0 through 1<<31 (see the package
// Contract). An ID of 0 contributes nothing, and a multi-bit ID sets several bits
// at once.
func StringsToBitMap(enum map[string]int, s []string) (int, error) {
	errStrings := make([]string, 0, len(s))

	var v int

	for _, key := range s {
		id, ok := enum[key]
		if ok {
			v |= id
		} else {
			errStrings = append(errStrings, key)
		}
	}

	var err error

	if len(errStrings) > 0 {
		err = fmt.Errorf("%w: %q", ErrUnknownStringValues, errStrings)
	}

	return v, err
}
