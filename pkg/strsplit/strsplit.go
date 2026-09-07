/*
Package strsplit splits strings into bounded-size chunks without breaking
Unicode characters, keeping human-readable boundaries (spaces, punctuation, and
newlines).

# How It Works

The package exposes two functions:

  - [Chunk]: splits a full text block, prioritizing newline boundaries first,
    trimming whitespace per line, then delegating long lines to [ChunkLine].
  - [ChunkLine]: splits a single line by maximum byte size, ensuring the split
    point is at a rune boundary and preferring the closest separator before the
    limit.

Separator preference order in [ChunkLine]:

 1. Unicode whitespace.
 2. Unicode punctuation (kept with the preceding chunk).
 3. Hard UTF-8-safe cut when no separator exists.

# Sizing Semantics

size is a maximum byte length, not a rune count. Produced chunks never exceed
size bytes, with one unavoidable exception: a single rune wider than size is
emitted whole (splitting it would produce invalid UTF-8), so that one chunk may
exceed size.

# Line And Whitespace Handling

[Chunk] treats only the newline byte "\n" as a line boundary. A carriage return
"\r" is not a boundary on its own; it is removed only when it is adjacent to a
"\n" (or otherwise leading/trailing), because each line is whitespace-trimmed.
Other Unicode line separators (U+2028, U+2029, U+0085, ...) are likewise not
treated as boundaries and may remain inside a chunk. Empty and whitespace-only
lines are dropped, so blank lines between paragraphs never yield empty chunks
and are not preserved.

# Chunk Limit

Both functions support an optional chunk limit n:

  - n > 0: return at most n chunks.
  - n < 0: unlimited chunks.
  - n == 0: return nil.

# Return Values

Both functions return nil only for invalid arguments (size < 1 or n == 0).
Otherwise they return a non-nil slice that may be empty (for example when the
input is empty or contains only whitespace). Produced chunks are always
whitespace-trimmed and never empty.

# Usage

	chunks := strsplit.Chunk(text, 280, -1)    // split full text block
	lineParts := strsplit.ChunkLine(line, 64, 3) // at most 3 chunks

# When To Use

  - Text must fit a field or message size limit measured in bytes.
  - Splitting must not cut through a multi-byte UTF-8 character or an emoji.
  - Chunks should break at spaces, punctuation, or newlines where possible.
*/
package strsplit

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Chunk splits a text block into whitespace-trimmed substrings of at most size
// bytes, breaking on newline ("\n") boundaries first and then on separators
// within each long line. Empty and whitespace-only lines are dropped. It returns
// at most n chunks (n > 0), an unlimited number (n < 0), or nil (n == 0 or
// size < 1). See [ChunkLine] for the per-line separator rules and the
// wider-than-size rune caveat.
func Chunk(s string, size, n int) []string {
	if (size < 1) || (n == 0) {
		return nil
	}

	ret := make([]string, 0, strings.Count(s, "\n")+1)

	// look for newlines first
	for line := range strings.SplitSeq(s, "\n") {
		if limitReached(n, len(ret)) {
			break
		}

		ret = appendLine(ret, strings.TrimSpace(line), size, n)
	}

	return ret
}

// ChunkLine splits a single line into whitespace-trimmed substrings of at most
// size bytes, always cutting on a UTF-8 rune boundary and preferring the closest
// Unicode whitespace (then punctuation, kept with the preceding chunk) before the
// limit. A single rune wider than size is emitted whole, so that chunk may exceed
// size. It returns at most n chunks (n > 0), an unlimited number (n < 0), or nil
// (n == 0 or size < 1); produced chunks are never empty.
func ChunkLine(s string, size, n int) []string {
	if (size < 1) || (n == 0) {
		return nil
	}

	ret := make([]string, 0, len(s)/size+1)

	for (len(s) > size) && !limitReached(n, len(ret)) {
		end := separatorEnd(s, runeSafeEnd(s, size))
		ret = appendChunk(ret, s[:end])
		s = strings.TrimSpace(s[end:])
	}

	if !limitReached(n, len(ret)) {
		ret = appendChunk(ret, s)
	}

	return ret
}

// appendLine appends the already-trimmed line to ret: dropping it when empty,
// keeping it whole when it fits within size, or delegating to [ChunkLine] when it
// is longer than size (honoring the remaining share of the n limit).
func appendLine(ret []string, line string, size, n int) []string {
	if line == "" {
		return ret
	}

	if len(line) <= size {
		return append(ret, line)
	}

	ns := n
	if ns > 0 {
		ns = n - len(ret)
	}

	return append(ret, ChunkLine(line, size, ns)...)
}

// appendChunk appends the whitespace-trimmed s to ret, skipping it when the
// trimmed result is empty so a chunk is never blank.
func appendChunk(ret []string, s string) []string {
	if chunk := strings.TrimSpace(s); chunk != "" {
		return append(ret, chunk)
	}

	return ret
}

// limitReached reports whether the n-chunk limit (only enforced when n > 0) has
// already been met by the count chunks produced so far.
func limitReached(n, count int) bool {
	return (n > 0) && (count >= n)
}

// runeSafeEnd returns a cut index no greater than size that never lands inside a
// multi-byte rune. When the leading rune itself is wider than size it returns
// that rune's full width, guaranteeing forward progress at the cost of a chunk
// that exceeds size. It assumes len(s) > size so that s[size] is addressable.
func runeSafeEnd(s string, size int) int {
	end := size

	// ensure we aren't in the middle of a multi-byte Unicode character
	for (end > 0) && !utf8.RuneStart(s[end]) {
		end--
	}

	// the leading rune is wider than size: hard-cut the whole rune to guarantee progress
	if end == 0 {
		_, end = utf8.DecodeRuneInString(s)
	}

	return end
}

// separatorEnd returns the preferred cut index within s[:end]: the byte index of
// the last Unicode whitespace (cutting before it), else the index just past the
// last Unicode punctuation (keeping it with the preceding chunk), else end when
// neither separator is present.
func separatorEnd(s string, end int) int {
	// try to split by unicode spaces
	if i := strings.LastIndexFunc(s[:end], unicode.IsSpace); i >= 0 {
		return i
	}

	// try to split by punctuation characters, keeping the punctuation with the chunk
	if i := strings.LastIndexFunc(s[:end], unicode.IsPunct); i >= 0 {
		_, offset := utf8.DecodeRuneInString(s[i:])
		return i + offset
	}

	return end
}
