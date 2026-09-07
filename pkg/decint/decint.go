/*
Package decint provides utility functions to parse and represent decimal values
as fixed-point integers with a defined precision.

Values with at most six decimal places are stored as integers (scaled by 1e6)
to preserve deterministic behavior in comparisons, serialization, and transport.

The package provides bidirectional conversion between float64/string and scaled
int64 or uint64, fixed six-decimal output formatting, explicit parse errors for
invalid numeric strings, safe range constants (MaxInt and MaxFloat) for boundary
checks, and unsigned conversion helpers that clamp non-positive values to zero.

Implementation note:

  - float-to-integer conversion scales by 1e6 and rounds to the nearest
    integer (half away from zero), so extra fractional digits beyond the
    supported precision are rounded rather than truncated.

Safe range:

  - Values are safe up to MaxFloat = 2^33 = 8_589_934_592 with six exact
    decimal places. This is the largest magnitude at which a float64 still
    resolves a 1e-6 step (its ULP stays below 1e-6); beyond it the sixth
    decimal digit is no longer representable, so it is excluded from the safe
    range rather than rounded.

# When To Use

  - Money or quantities must not accumulate binary floating-point error.
  - Values are stored as integers and formatted for display with a fixed
    number of decimals.
*/
package decint

import "errors"

const (
	// precision of the float-to-integer conversion (max 6 decimal digits).
	precision float64 = 1e+06

	// scale is the integer twin of precision, used for exact integer formatting.
	scale = 1_000_000

	// MaxInt is the maximum scaled integer that preserves six exact decimals
	// (2^33 * 1e6). It is below 2^53, so it is still an exact float64.
	MaxInt = 8_589_934_592_000_000

	// MaxFloat is the maximum value that preserves six exact decimals in a
	// float64 (2^33). Above it the float64 ULP exceeds 1e-6 and the sixth
	// decimal digit can no longer be represented.
	MaxFloat = 8_589_934_592
)

var (
	// ErrInvalidNumber indicates the input string is not a valid finite number.
	ErrInvalidNumber = errors.New("invalid decimal number")

	// ErrOutOfRange indicates the value is outside the safe range.
	ErrOutOfRange = errors.New("value out of safe range")
)
