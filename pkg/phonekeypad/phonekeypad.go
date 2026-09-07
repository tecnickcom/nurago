/*
Package phonekeypad converts alphabetic strings and phone number literals to
their numeric equivalents on a standard 12-key telephony keypad (ITU E.161 /
ITU T.9).

# Keypad Layout

The standard keypad mapping (ITU E.161) used by this package:

	+-----+-----+-----+
	|  1  |  2  |  3  |
	|     | ABC | DEF |
	+-----+-----+-----+
	|  4  |  5  |  6  |
	| GHI | JKL | MNO |
	+-----+-----+-----+
	|  7  |  8  |  9  |
	| PQRS| TUV | WXYZ|
	+-----+-----+-----+
	|     |  0  |     |
	|     |     |     |
	+-----+-----+-----+

# Quick Start

Translate a vanity phone number to its dialable form:

	numStr := phonekeypad.KeypadNumberString("1-800-FLOWERS")
	// numStr == "18003569377"

Or get the digit sequence as a slice for programmatic use:

	digits := phonekeypad.KeypadNumber("1-800-FLOWERS")
	// digits == []int{1, 8, 0, 0, 3, 5, 6, 9, 3, 7, 7}

# When To Use

  - Vanity numbers such as 1-800-FLOWERS must be converted to digits.
  - You index numbers for search in their digit form.
*/
package phonekeypad

import (
	"strings"
)

// KeypadDigit maps a single rune to its ITU E.161 phone-keypad digit.
//
// Digits '0' to '9' map to themselves. Letters 'A' to 'Z' and 'a' to 'z' are
// mapped case-insensitively according to the standard keypad layout:
//
//	'A','B','C'         → 2
//	'D','E','F'         → 3
//	'G','H','I'         → 4
//	'J','K','L'         → 5
//	'M','N','O'         → 6
//	'P','Q','R','S'     → 7
//	'T','U','V'         → 8
//	'W','X','Y','Z'     → 9
//
// Any rune that is not an ASCII digit or ASCII letter (punctuation, whitespace,
// symbols, and non-ASCII letters such as 'É' or full-width digits) returns
// (-1, false). This makes it safe to range over formatted phone strings like
// "(800) 555-1234" and skip characters where ok is false.
func KeypadDigit(r rune) (int, bool) {
	if r >= '0' && r <= '9' {
		return int(r - '0'), true
	}

	if r >= 'a' && r <= 'z' {
		r -= ('a' - 'A')
	}

	return keypadAlphaToDigit(r)
}

// keypadAlphaToDigit maps uppercase ASCII letters to ITU E.161 keypad digits.
func keypadAlphaToDigit(r rune) (int, bool) {
	switch r {
	case 'A', 'B', 'C':
		return 2, true
	case 'D', 'E', 'F':
		return 3, true
	case 'G', 'H', 'I':
		return 4, true
	case 'J', 'K', 'L':
		return 5, true
	case 'M', 'N', 'O':
		return 6, true
	case 'P', 'Q', 'R', 'S':
		return 7, true
	case 'T', 'U', 'V':
		return 8, true
	case 'W', 'X', 'Y', 'Z':
		return 9, true
	}

	return -1, false
}

// KeypadNumber converts a string to a []int of keypad digits.
//
// Each rune in s is passed to [KeypadDigit]. Characters that do not map to a
// keypad digit (separators, punctuation, etc.) are silently skipped, so
// formatted inputs such as "1-800-EXAMPLE" or "(555) 123-4567" are handled
// without pre-sanitisation.
//
// The returned slice has the same length as the number of valid keypad
// characters in s. It is never nil: empty or all-separator input yields a
// non-nil, zero-length slice. Use [KeypadNumberString] when a plain digit
// string is preferred over a slice.
func KeypadNumber(num string) []int {
	seq := make([]int, 0, len(num))

	for _, r := range num {
		v, status := KeypadDigit(r)
		if status {
			seq = append(seq, v)
		}
	}

	return seq
}

// KeypadNumberString converts a string to a keypad digit string.
//
// Each rune in num is mapped via [KeypadDigit] and the resulting digits are
// written into a plain string (e.g. "18003569377"), suitable for storage,
// display, or passing directly to a dialler. Non-keypad characters
// (separators, punctuation, etc.) are skipped just as in [KeypadNumber], so an
// empty or all-punctuation input yields "".
func KeypadNumberString(num string) string {
	var sb strings.Builder

	sb.Grow(len(num))

	for _, r := range num {
		v, status := KeypadDigit(r)
		if status {
			sb.WriteByte(byte('0' + v))
		}
	}

	return sb.String()
}
