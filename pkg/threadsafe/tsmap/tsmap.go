/*
Package tsmap reads and writes maps shared across goroutines, taking a
caller-supplied lock at every call site.

# How It Works

Every function receives both the map and a lock interface from
github.com/tecnickcom/nurago/pkg/threadsafe:

  - write operations ([Set], [Delete], [Do]) require [threadsafe.Locker] and use
    `Lock`/`Unlock`.
  - read and pure-transform operations ([Get], [GetOK], [Len], [Snapshot],
    [Filter], [Map], [Reduce], [Invert], [RDo]) require [threadsafe.RLocker] and
    use `RLock`/`RUnlock`.

[Filter], [Map], [Reduce], and [Invert] delegate to
github.com/tecnickcom/nurago/pkg/maputil under the provided lock. The helpers
work with standard [sync.RWMutex] and any custom lock type that satisfies the
interfaces.

# Usage

	var (
	    mu sync.RWMutex
	    m  = map[string]int{"a": 1, "b": 2}
	)

	tsmap.Set(&mu, m, "c", 3)
	v, ok := tsmap.GetOK(&mu, m, "a")
	_ = v
	_ = ok

	even := tsmap.Filter(&mu, m, func(_ string, n int) bool { return n%2 == 0 })
	total := tsmap.Reduce(&mu, m, 0, func(_ string, n int, acc int) int { return acc + n })
	_ = even
	_ = total

# Determinism

Go map iteration order is randomized, so the transform helpers inherit the
semantics of github.com/tecnickcom/nurago/pkg/maputil: [Reduce] is deterministic
only when its reducing function is order-independent (for example commutative
and associative), and [Map] and [Invert] follow last-write-wins when several
input entries map to the same output key.

# Concurrency

Maps are reference types and no helper reassigns the caller's map variable, so
passing the map by value is safe: every helper serializes access to the shared
map through the provided lock. Route all access through the helpers using the
same lock instance; touching the map directly outside the lock is a data race.

Each helper acquires and releases the lock on its own, so a sequence of separate
helper calls is not atomic as a whole (a [GetOK] followed by a separate [Set] is
a classic check-then-act race). Use [Do] (write) or [RDo] (read) to run a
compound operation under a single lock acquisition:

	tsmap.Do(&mu, m, func(mm map[string]int) {
	    if _, ok := mm["k"]; !ok {
	        mm["k"] = 1
	    }
	})

The predicate/transform callbacks passed to [Filter], [Map], [Reduce], [Invert],
[Do], and [RDo] run while the lock is held. They must be cheap and non-blocking,
and must not call back into these helpers on the same lock: a write helper
invoked from a read-locked callback deadlocks, recursive read locking is not safe
when a writer is waiting, and any helper invoked under [Do]'s exclusive lock
deadlocks outright. A callback must also not retain the map it receives beyond
its own return: once the lock is released, reading or writing it races with other
goroutines.

[Filter], [Map], and [Invert] return new maps, but any reference-typed values
they contain remain shared with the original: the helpers protect the map, not
the objects it points to.

# Guarded Wrapper

The free functions above require the caller to pair the right lock with the
right map at every call site. [Guarded] is an optional higher-level type that
owns both a map and its [sync.RWMutex], so access can only go through its methods
and a lock can never be mismatched or forgotten. Prefer it when a single map is
the unit of sharing; keep the free functions when one lock must guard several
containers at once. Transforms to a different type (Map/Reduce/Invert) are
expressed through [Guarded.RDo].

See also: github.com/tecnickcom/nurago/pkg/threadsafe

# When To Use

  - A map is shared across goroutines and you want synchronization visible in
    the code, not hidden.
  - You need generic helpers (keys, values, filter, get-or-set) that respect
    an external lock.
*/
package tsmap

import (
	"maps"

	"github.com/tecnickcom/nurago/pkg/maputil"
	"github.com/tecnickcom/nurago/pkg/threadsafe"
)

// Set stores value v at key k using an exclusive lock. Like a built-in map
// assignment, Set panics if m is nil; use [Guarded], which allocates lazily, to
// avoid pre-initializing the map.
func Set[M ~map[K]V, K comparable, V any](mux threadsafe.Locker, m M, k K, v V) {
	mux.Lock()
	defer mux.Unlock()

	m[k] = v
}

// Delete removes key k from map using an exclusive lock.
func Delete[M ~map[K]V, K comparable, V any](mux threadsafe.Locker, m M, k K) {
	mux.Lock()
	defer mux.Unlock()

	delete(m, k)
}

// Get returns value for key k under a read lock.
func Get[M ~map[K]V, K comparable, V any](mux threadsafe.RLocker, m M, k K) V {
	mux.RLock()
	defer mux.RUnlock()

	return m[k]
}

// GetOK returns value and presence flag for key k under a read lock.
func GetOK[M ~map[K]V, K comparable, V any](mux threadsafe.RLocker, m M, k K) (V, bool) {
	mux.RLock()
	defer mux.RUnlock()

	v, ok := m[k]

	return v, ok
}

// Len returns map length under a read lock.
func Len[M ~map[K]V, K comparable, V any](mux threadsafe.RLocker, m M) int {
	mux.RLock()
	defer mux.RUnlock()

	return len(m)
}

// Snapshot returns a shallow copy of the map taken under a read lock. The copy
// is safe to use after the lock is released, but any reference-typed values
// remain shared with the original map. It is the free-function counterpart of
// [Guarded.Snapshot].
func Snapshot[M ~map[K]V, K comparable, V any](mux threadsafe.RLocker, m M) M {
	mux.RLock()
	defer mux.RUnlock()

	return maps.Clone(m)
}

// Filter returns a new map containing entries for which predicate f is true, under a read lock.
func Filter[M ~map[K]V, K comparable, V any](mux threadsafe.RLocker, m M, f func(K, V) bool) M {
	mux.RLock()
	defer mux.RUnlock()

	return maputil.Filter(m, f)
}

// Map transforms each map entry under a read lock and returns a new mapped result.
func Map[M ~map[K]V, K, J comparable, V, U any](mux threadsafe.RLocker, m M, f func(K, V) (J, U)) map[J]U {
	mux.RLock()
	defer mux.RUnlock()

	return maputil.Map(m, f)
}

// Reduce folds map entries under a read lock starting from init.
func Reduce[M ~map[K]V, K comparable, V, U any](mux threadsafe.RLocker, m M, init U, f func(K, V, U) U) U {
	mux.RLock()
	defer mux.RUnlock()

	return maputil.Reduce(m, init, f)
}

// Invert returns a new map with keys and values swapped, under a read lock.
func Invert[M ~map[K]V, K, V comparable](mux threadsafe.RLocker, m M) map[V]K {
	mux.RLock()
	defer mux.RUnlock()

	return maputil.Invert(m)
}

// Do runs f while holding the exclusive lock, giving it raw access to the map
// for atomic compound operations (check-then-set, bulk mutation). The map is
// passed by value, so it must be non-nil for f to insert entries (a nil map
// panics on assignment, exactly as [Set] does). f must not call back into these
// helpers on the same lock, nor retain the map beyond its own return: using it
// after the lock is released races with other goroutines.
func Do[M ~map[K]V, K comparable, V any](mux threadsafe.Locker, m M, f func(M)) {
	mux.Lock()
	defer mux.Unlock()

	f(m)
}

// RDo runs f while holding the read lock for atomic multi-step reads. f must not
// mutate the map, call back into these helpers on the same lock, or retain the
// map beyond its own return: reading it after the lock is released races with
// other goroutines.
func RDo[M ~map[K]V, K comparable, V any](mux threadsafe.RLocker, m M, f func(M)) {
	mux.RLock()
	defer mux.RUnlock()

	f(m)
}
