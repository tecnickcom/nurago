/*
Package numtrie provides a generic, digit-indexed [trie] (prefix tree) for
associating values of any type with numerical keys, with built-in support for
partial/prefix matching and alphabetical (vanity) phone-number keys.

Lookup is O(k) in the number of digits k, with prefix traversal and
partial-match support.

# Usage

[Node] is a generic trie node parameterised on the value type. Build the trie
once with [Node.Add], then query it repeatedly with [Node.Get]:

	// Build a routing table.
	root := numtrie.New[Route]()
	root.Add("1",      &defaultUSRoute)
	root.Add("1212",   &newYorkRoute)
	root.Add("44",     &ukRoute)
	root.Add("44207",  &londonRoute)

	// Longest-prefix lookup for an incoming call.
	val, status := root.Get("+1-212-555-0100")
	if val != nil {
		// val == &newYorkRoute (longest matching prefix: "1212")
	}

# Allocation

Each node holds a fixed 10-slot children array (one slot per digit 0 to 9)
rather than a per-node map, so a node is a single allocation. Insertion
allocates one node per new digit position; re-inserting at an existing position
allocates nothing, and lookups ([Node.Get] and [Node.GetExact]) never allocate.

# Match Status Codes

The status int8 returned by [Node.Get] is a compact bit field:

	Bit 7 (sign): set   → no digits matched at all (empty input or no root child)
	Bit 1:        set   → input extends beyond the matched trie path (prefix match)
	Bit 0:        set   → matched node has children (partial match)

The six named constants encode every meaningful combination:

	StatusMatchEmpty         (-127): no digit characters in input
	StatusMatchNo            (-125): first digit not in trie
	StatusMatchFull          (   0): exact match, leaf node
	StatusMatchPartial       (   1): exact match, non-leaf node
	StatusMatchPrefix        (   2): stored key is prefix of input, leaf node
	StatusMatchPartialPrefix (   3): stored key is prefix of input, non-leaf node

When bit 7 is set the status is a standalone sentinel (StatusMatchEmpty or
StatusMatchNo): only bit 7 is significant and the low bits carry no meaning.
[Node.Get] returns a nil value for both of these, even when a root/default value
is present.

A non-negative status does not by itself guarantee a non-nil value: callers must
always nil-check the returned pointer. StatusMatchFull and StatusMatchPrefix (the
two leaf outcomes) always carry a value, but StatusMatchPartial and
StatusMatchPartialPrefix return nil whenever no value was stored on the matched
path.

# Concurrency

A [Node] is not safe for concurrent modification: [Node.Add] mutates the trie in
place. Once the trie is fully built it may be queried concurrently by any number
of goroutines via [Node.Get] and [Node.GetExact], provided no [Node.Add] runs
concurrently.

# When To Use

  - You route on number prefixes, such as dialing codes or bank identifiers.
  - Lookups must return the value for the longest matching prefix, not an
    exact match.
  - Keys may arrive as vanity phone numbers containing letters.

[trie]: https://en.wikipedia.org/wiki/Trie
*/
package numtrie

import (
	"github.com/tecnickcom/nurago/pkg/phonekeypad"
	"github.com/tecnickcom/nurago/pkg/typeutil"
)

// Status codes to be returned when searching for a number in the trie.
const (
	// StatusMatchEmpty indicates that the input string contained no recognizable
	// digit characters and no match was found. [Node.Get] returns a nil value
	// for this status, even when a root/default value is present.
	StatusMatchEmpty int8 = -127 // 0b10000001

	// StatusMatchNo indicates that no match was found because the first digit
	// of the input does not correspond to any child of the trie root. [Node.Get]
	// returns a nil value for this status, even when a root/default value is
	// present.
	StatusMatchNo int8 = -125 // 0b10000011

	// StatusMatchFull indicates an exact match: every digit of the input was
	// consumed and the final trie node is a leaf (no children).
	StatusMatchFull int8 = 0 // 0b00000000

	// StatusMatchPartial indicates that every digit of the input was consumed
	// and the final trie node is not a leaf (it has children). The input is a
	// prefix of at least one longer stored key. The returned value is the last
	// non-nil value found along the path, or nil if no value was stored on it.
	StatusMatchPartial int8 = 1 // 0b00000001

	// StatusMatchPrefix indicates that the trie path was exhausted before all
	// input digits were consumed: a stored key is a prefix of the input. The last
	// non-nil value found along the path is returned (normally the value at the
	// matched leaf).
	StatusMatchPrefix int8 = 2 // 0b00000010

	// StatusMatchPartialPrefix indicates that the trie path was exhausted
	// before all input digits were consumed and the last matched node is not a
	// leaf. The returned value is the last non-nil value found along the path,
	// or nil if no value was stored on it.
	StatusMatchPartialPrefix int8 = 3 // 0b00000011
)

// indexSize is the number of possible children for each trie node. It matches
// the [0, 10) range guaranteed by [github.com/tecnickcom/nurago/pkg/phonekeypad.KeypadDigit],
// whose result indexes the children array directly; the two must stay in sync.
const indexSize = 10 // digits from 0 to 9

// Node is a generic numerical-indexed trie node that stores a value of type T.
//
// Each node holds up to 10 children, one per digit 0 to 9. Non-digit characters
// in keys are skipped during traversal, making the trie tolerant of formatted
// numbers (e.g. "+1-800-555-0100") and vanity letter sequences.
//
// The zero value is not usable; create a root node with [New].
//
// A Node is not safe for concurrent modification: [Node.Add] mutates the trie in
// place. Once the trie is fully built it may be queried concurrently by any
// number of goroutines via [Node.Get] and [Node.GetExact], provided no
// [Node.Add] runs concurrently.
type Node[T any] struct {
	value       *T
	numChildren int
	children    [indexSize]*Node[T]
}

// New constructs an empty root Node[T] for building a numerical prefix trie.
func New[T any]() *Node[T] {
	return &Node[T]{}
}

// Add stores val at the trie position defined by num.
// Non-digit characters are skipped and letters are mapped to keypad digits.
// It returns true when the key was new, or false when an existing value was
// overwritten.
//
// Storing a value at the empty key (Add("", v)) sets a default returned by
// [Node.Get] as the longest-prefix fallback for any input that matches at least
// one digit.
//
// A nil val is rejected as a no-op: the trie is left unchanged and Add returns
// false, since the trie cannot store or distinguish a nil value from an absent
// one.
//
// The trie stores the supplied pointer, not a copy of the pointed-to value:
// mutating that value after Add is visible through the trie, and a pointer
// stored under multiple keys is shared by all of them.
func (t *Node[T]) Add(num string, val *T) bool {
	if val == nil {
		return false
	}

	node := t

	for _, v := range num {
		i, ok := phonekeypad.KeypadDigit(v)
		if !ok {
			continue
		}

		if node.children[i] == nil {
			node.children[i] = New[T]()
			node.numChildren++
		}

		node = node.children[i]
	}

	isnew := (node.value == nil)

	node.value = val

	return isnew
}

// Get retrieves the longest-prefix match for num.
// Non-digit characters are skipped. The returned status indicates exact, partial,
// prefix, or no-match outcomes as described by the StatusMatch* constants.
//
// The status int8 returned by [Node.Get] is a compact bit field:
//
//	Bit 7 (sign): set   → no digits matched at all (empty input or no root child)
//	Bit 1:        set   → input extends beyond the matched trie path (prefix match)
//	Bit 0:        set   → matched node has children (partial match)
//
// The six named constants encode every meaningful combination:
//
//	StatusMatchEmpty         (-127): no digit characters in input
//	StatusMatchNo            (-125): first digit not in trie
//	StatusMatchFull          (   0): exact match, leaf node
//	StatusMatchPartial       (   1): exact match, non-leaf node
//	StatusMatchPrefix        (   2): stored key is prefix of input, leaf node
//	StatusMatchPartialPrefix (   3): stored key is prefix of input, non-leaf node
//
// For the two negative sentinels (StatusMatchEmpty and StatusMatchNo) the
// returned value is nil, even when a root/default value is present. A
// non-negative status does not by itself guarantee a non-nil value:
// StatusMatchFull and StatusMatchPrefix always carry a value, but
// StatusMatchPartial and StatusMatchPartialPrefix return nil when no value was
// stored on the matched path, so callers must always nil-check the returned
// pointer.
func (t *Node[T]) Get(num string) (*T, int8) {
	var match, digit int

	node := t
	val := node.value // the root node value is the empty-prefix (default) match

	for _, v := range num {
		i, ok := phonekeypad.KeypadDigit(v)
		if !ok {
			// ignore non-digit characters
			continue
		}

		digit++

		if node.children[i] == nil {
			// no child for this digit: stop. val already holds the last non-nil
			// value seen on the path, including the current node.
			break
		}

		// move to the next child node
		node = node.children[i]

		if node.value != nil {
			// remember the last non-nil value found
			val = node.value
		}

		match++
	}

	status := node.matchStatus(match, digit)

	if match == 0 {
		// Pure no-match (empty input or first digit absent from the trie):
		// report no value, even when a root/default value is present.
		return nil, status
	}

	return val, status
}

// matchStatus derives the Get status code for a traversal that ended on the
// receiver node. match is the number of digits matched along the trie path.
// digit is match, or match+1 when the input carried a further digit whose child
// was absent (the prefix case); only whether digit exceeds match is significant,
// so digit is not the total input-digit count.
func (t *Node[T]) matchStatus(match, digit int) int8 {
	if match == 0 {
		// No digit was matched: return the named no-match constants directly,
		// so the status is well-defined even when the trie is empty.
		if digit == 0 {
			return StatusMatchEmpty
		}

		return StatusMatchNo
	}

	return typeutil.BoolToNum[int8](digit > match)<<1 |
		typeutil.BoolToNum[int8](t.numChildren > 0)
}

// GetExact retrieves the value stored at the exact trie position defined by num.
// Non-digit characters are skipped and letters are mapped to keypad digits,
// mirroring [Node.Add]. Unlike [Node.Get], it performs no longest-prefix
// fallback: it returns nil when the position does not exist in the trie or
// when no value was stored exactly at that position.
func (t *Node[T]) GetExact(num string) *T {
	node := t

	for _, v := range num {
		i, ok := phonekeypad.KeypadDigit(v)
		if !ok {
			// ignore non-digit characters
			continue
		}

		if node.children[i] == nil {
			return nil
		}

		node = node.children[i]
	}

	return node.value
}
