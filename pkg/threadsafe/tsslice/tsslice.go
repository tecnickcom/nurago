/*
Package tsslice reads and writes slices shared across goroutines, taking a
caller-supplied lock at every access.

# How It Works

Each function takes a pointer to the slice and a lock interface from
github.com/tecnickcom/nurago/pkg/threadsafe:

  - write operations ([Set], [SetOK], [Delete], [Append], [Do]) require
    [threadsafe.Locker] and use exclusive `Lock`/`Unlock`.
  - read and pure-transform operations ([Get], [GetOK], [Len], [Snapshot],
    [Filter], [Map], [Reduce], [RDo]) require [threadsafe.RLocker] and use shared
    `RLock`/`RUnlock`.

The helpers delegate functional operations to github.com/tecnickcom/nurago/pkg/sliceutil
while enforcing synchronization around the access. Accessors come in panicking
indexing forms ([Get], [Set]) and bounds-checked, non-panicking forms ([GetOK],
[SetOK]). They work with standard [sync.RWMutex] and any custom lock type that
satisfies the interfaces.

# Usage

	var (
	    mu sync.RWMutex
	    s  = []int{1, 2, 3}
	)

	tsslice.Set(&mu, &s, 0, 10)
	v := tsslice.Get(&mu, &s, 1)
	_ = v

	tsslice.Append(&mu, &s, 4, 5)
	even := tsslice.Filter(&mu, &s, func(_ int, n int) bool { return n%2 == 0 })
	total := tsslice.Reduce(&mu, &s, 0, func(_ int, n int, acc int) int { return acc + n })
	_ = even
	_ = total

# Concurrency

All helpers take the slice by pointer (`*S`) and dereference it only while
holding the lock. This includes [Append], which may reallocate the backing
array and reassign the slice. As a result, concurrent calls that share the same
slice variable and the same lock are safe by default: a reader observes a
consistent slice header even while another goroutine grows the slice.

Pass the address of the shared variable to every helper and route all access
through them using the same lock instance. Reading or writing the shared
variable directly (outside a helper and outside the lock) is still a data race.

Each helper acquires and releases the lock on its own, so a sequence of separate
helper calls is not atomic as a whole. Use [Do] (write) or [RDo] (read) to run a
compound operation (for example a conditional append or a multi-step scan) under
a single lock acquisition:

	var (
	    mu sync.RWMutex
	    s  = []int{1, 2, 3}
	)

	tsslice.Do(&mu, &s, func(sp *[]int) {
	    if len(*sp) < 4 {
	        *sp = append(*sp, 4)
	    }
	})

The predicate/transform callbacks passed to [Filter], [Map], [Reduce], [Do], and
[RDo] run while the lock is held. They must be cheap and non-blocking, and must
not call back into these helpers on the same lock: a write helper invoked from a
read-locked callback deadlocks, recursive read locking is not safe when a writer
is waiting, and any helper invoked under [Do]'s exclusive lock deadlocks
outright. A callback must also not retain the slice it receives (or the pointer
[Do] passes) beyond its own return: once the lock is released, reading or writing
that reference races with other goroutines.

[Filter] and [Map] return new slices, but any reference-typed elements they
contain remain shared with the original: the helpers protect the slice, not the
objects it points to.

# Guarded Wrapper

The free functions above require the caller to pair the right lock with the
right slice at every call site. [Guarded] is an optional higher-level type that
owns both a slice and its [sync.RWMutex], so access can only go through its
methods and a lock can never be mismatched or forgotten. Prefer it when a single
slice is the unit of sharing; keep the free functions when one lock must guard
several containers at once. Transforms to a different element type (Map/Reduce)
are expressed through [Guarded.RDo].

See also: github.com/tecnickcom/nurago/pkg/threadsafe

# When To Use

  - A slice is appended to and read from several goroutines.
  - You want the lock to remain the caller's, so multiple structures can
    share one.
*/
package tsslice

import (
	"slices"

	"github.com/tecnickcom/nurago/pkg/sliceutil"
	"github.com/tecnickcom/nurago/pkg/threadsafe"
)

// Set assigns value v at index k using an exclusive lock.
//
// Set panics if k is out of range, mirroring built-in slice indexing; use
// [SetOK] for a bounds-checked, non-panicking alternative.
func Set[S ~[]E, E any](mux threadsafe.Locker, s *S, k int, v E) {
	mux.Lock()
	defer mux.Unlock()

	(*s)[k] = v
}

// SetOK assigns value v at index k using an exclusive lock and reports whether
// k was within range. It never panics: an out-of-range index is a no-op that
// returns false.
func SetOK[S ~[]E, E any](mux threadsafe.Locker, s *S, k int, v E) bool {
	mux.Lock()
	defer mux.Unlock()

	if k < 0 || k >= len(*s) {
		return false
	}

	(*s)[k] = v

	return true
}

// Get returns value at index k under a read lock.
//
// Get panics if k is out of range, mirroring built-in slice indexing; use
// [GetOK] for a bounds-checked, non-panicking alternative.
func Get[S ~[]E, E any](mux threadsafe.RLocker, s *S, k int) E {
	mux.RLock()
	defer mux.RUnlock()

	return (*s)[k]
}

// GetOK returns the value at index k under a read lock and reports whether k
// was within range. It never panics: an out-of-range index returns the zero
// value and false.
func GetOK[S ~[]E, E any](mux threadsafe.RLocker, s *S, k int) (E, bool) {
	mux.RLock()
	defer mux.RUnlock()

	if k < 0 || k >= len(*s) {
		var zero E

		return zero, false
	}

	return (*s)[k], true
}

// Len returns slice length under a read lock.
func Len[S ~[]E, E any](mux threadsafe.RLocker, s *S) int {
	mux.RLock()
	defer mux.RUnlock()

	return len(*s)
}

// Snapshot returns a shallow copy of the slice taken under a read lock. The copy
// is safe to use after the lock is released, but any reference-typed elements
// remain shared with the original slice. It is the free-function counterpart of
// [Guarded.Snapshot].
func Snapshot[S ~[]E, E any](mux threadsafe.RLocker, s *S) S {
	mux.RLock()
	defer mux.RUnlock()

	return slices.Clone(*s)
}

// Append appends one or more values using an exclusive lock.
//
// Append may reallocate the backing array and reassign the slice pointed to by
// s. Because every other helper also takes *S and dereferences it under the
// lock, concurrent access through the helpers remains safe: see the package
// Concurrency notes.
func Append[S ~[]E, E any](mux threadsafe.Locker, s *S, v ...E) {
	mux.Lock()
	defer mux.Unlock()

	*s = append(*s, v...)
}

// Delete removes the element at index k using an exclusive lock, preserving the
// order of the remaining elements. It reports whether k was within range; an
// out-of-range index is a no-op that returns false, so Delete never panics.
func Delete[S ~[]E, E any](mux threadsafe.Locker, s *S, k int) bool {
	mux.Lock()
	defer mux.Unlock()

	if k < 0 || k >= len(*s) {
		return false
	}

	*s = slices.Delete(*s, k, k+1)

	return true
}

// Filter returns a new slice containing elements for which predicate f is true, under a read lock.
func Filter[S ~[]E, E any](mux threadsafe.RLocker, s *S, f func(int, E) bool) S {
	mux.RLock()
	defer mux.RUnlock()

	return sliceutil.Filter(*s, f)
}

// Map transforms each element under a read lock and returns a new slice.
func Map[S ~[]E, E any, U any](mux threadsafe.RLocker, s *S, f func(int, E) U) []U {
	mux.RLock()
	defer mux.RUnlock()

	return sliceutil.Map(*s, f)
}

// Reduce folds slice elements under a read lock starting from init.
func Reduce[S ~[]E, E any, U any](mux threadsafe.RLocker, s *S, init U, f func(int, E, U) U) U {
	mux.RLock()
	defer mux.RUnlock()

	return sliceutil.Reduce(*s, init, f)
}

// Do runs f while holding the exclusive lock, giving it raw access to the slice
// pointer for atomic compound operations (read-modify-write, conditional
// growth). f must not call back into these helpers on the same lock, and must
// not retain the pointer beyond its own return: using it after the lock is
// released races with other goroutines.
func Do[S ~[]E, E any](mux threadsafe.Locker, s *S, f func(*S)) {
	mux.Lock()
	defer mux.Unlock()

	f(s)
}

// RDo runs f while holding the read lock, giving it the current slice value for
// atomic multi-step reads. f must not mutate the slice, call back into these
// helpers on the same lock, or retain the slice beyond its own return: reading
// it after the lock is released races with other goroutines.
func RDo[S ~[]E, E any](mux threadsafe.RLocker, s *S, f func(S)) {
	mux.RLock()
	defer mux.RUnlock()

	f(*s)
}
