/*
Package sfcache provides a local, thread-safe, fixed-size cache for expensive
lookups with single-flight deduplication.

Concurrent callers asking for the same key share a single lookup: one goroutine
calls the external service and the others wait for its result. Values are cached
for a TTL, the capacity is bounded, and an optional stale-if-error window keeps
serving the last known good value while the upstream is down.

# Usage

The value type is inferred from the lookup function, so [Cache.Lookup] returns
typed values with no assertions:

	cache := sfcache.New(func(ctx context.Context, key string) (*Customer, error) {
	    return fetchCustomer(ctx, key)
	}, sfcache.Config{Size: 256, TTL: 5 * time.Minute})

	customer, err := cache.Lookup(ctx, "customer:123")

Settings that do not depend on the cache types live in [Config]; those that do
live in options ([WithTTLFunc]).

# Caching

Only successful lookups are cached, for [Config.TTL] (a nil value is a value).
Errors are shared with the callers coalesced onto the same lookup but never
cached, so the next call retries; the failed key leaves an already-expired entry
behind until it is reclaimed or overwritten. A lookup that failed with its OWN
context's error publishes nothing at all. Whatever the lookup function returns is
passed through as-is, including a non-nil value alongside a non-nil error.

A [Config.TTL] <= 0 serves no value from the cache and only coalesces, unless
[WithTTLFunc] gives the entry a positive TTL. With stale-if-error enabled the last
value is still retained and can be served after a failed refresh.

Expiration uses the monotonic clock, which on most platforms does not advance while
the system is suspended: TTLs are effectively extended by the suspended time.

Cached values are shared by reference: treat them as read-only.

# Capacity

[Config.Size] bounds the values held, not [Cache.Len]. Len can exceed Size by the
number of lookups in flight, plus the residue of a failed lookup, plus one value
when a stale revive can evict nothing. The excess is reclaimed as those lookups
complete and the next value is stored.

A store may only evict something worth less than what it stores:

  - a failed lookup stores no value, so it reclaims only entries that hold
    nothing worth keeping, and otherwise leaves the cache over capacity;
  - a stale revive may also take a value that is itself being served stale: the
    one no caller has asked for, or else the one closest to its own deadline. When
    it can take nothing it exceeds the capacity by one value, reclaimed by the next
    successful store;
  - only a successful lookup may displace a valid entry, taking the one closest to
    expiring.

A lookup that is merely attempted, and may yet fail, can never cost the cache a
live value.

Every entry is held in one of three queues, in deadline order, so an eviction takes
the head of a queue rather than searching for it. A store costs O(log Size) holding
the exclusive write lock, and one with no victim it may take says so in constant
time. Cache hits take only the read lock.

[Cache.PurgeExpired] is the only linear pass. It sifts entries out one at a time
while few have expired, and past a fraction of the queue rebuilds the heap around
the survivors instead, so its cost is then bounded by what the cache HOLDS rather
than by what it removes. It holds the exclusive write lock throughout: on a cache
whose entries all expire together it is the longest lock this package takes.

The queues hold a copy of each key, so they cost about sizeof(K) + 16 bytes per
entry on top of the value: roughly 27 for a word-sized key, 35 for a string key.

# Single flight and context

The external lookup runs under the context of the caller that started it. A caller
that finds a lookup already in flight for its key waits for it and takes its
result, which under heavy churn may come from a later flight than the one it first
awaited.

[ErrLookupAborted] is returned to a caller whose context ends while it WAITS for an
in-flight lookup, or before its own lookup would start. The caller that RAN the
lookup receives the lookup function's own error instead. No lookup is started with
an already-ended context, while FRESH cached values are served regardless of
context state.

A stale value is not: serving one requires attempting a refresh, so a caller that
arrives with an already-ended context gets [ErrLookupAborted], not the stale value.
A caller whose context dies DURING its own lookup can still be handed one, with a
nil error.

If a lookup fails with the error of the context of the caller that ran it, that
error is not shared: a coalesced waiter retries with its own context. The test is
[errors.Is] against the context's error, so an upstream error that wraps
[context.DeadlineExceeded] or [context.Canceled] is treated as context-induced when
the producing context has also ended. The cost is one extra lookup.

If a waiter's context ends at the same instant the awaited lookup completes, either
outcome may be observed.

The lookup function must honor context cancellation and eventually return: one that
hangs forever pins its key until [Cache.Remove] or [Cache.Reset]. It must not call
[Cache.Lookup] for the same key of the same cache, which self-deadlocks. If it
panics, the panic reaches the caller that ran it and the waiters retry.

[Cache.Remove] and [Cache.Reset] invalidate the lookups in flight: the result is
returned to the caller that ran it but not cached, and the callers coalesced onto it
are released to retry. The orphaned lookup still runs to completion on its own
context.

# Stale-if-error

With [Config.MaxStale] or [Config.MaxStaleOnFailure] set, a failed refresh serves
the last known good value with a NIL error, so callers cannot tell a stale value
from a fresh one. The revived entry stays expired, so every call still attempts a
refresh and the first success replaces it. The stale window takes precedence over
the context-induced retry above. An entry whose last outcome was an error is never
served stale.

Stale protection is best-effort: the value is lost to [Cache.Remove], [Cache.Reset],
a panicking lookup or TTL function, and capacity eviction. [Cache.PurgeExpired] also
loses it, except for a key whose refresh is already in flight, whose value is held by
the flight rather than by an entry.

# Key requirements

A key must be hashable and equal to itself. An interface key holding an unhashable
dynamic type panics, in [Cache.Lookup] and in [Cache.Remove] alike, as any map access
would. A key that is not equal to itself (one that is or contains a NaN) could never
be found in a map again, so [Cache.Lookup] rejects it with [ErrInvalidKey] before any
lookup is attempted.

Example applications in this repository:
  - github.com/tecnickcom/nurago/pkg/awssecretcache
  - github.com/tecnickcom/nurago/pkg/dnscache

# When To Use

  - Many goroutines request the same key at once and the upstream should see
    one request.
  - Values expire on a TTL and the cache must stay bounded in size.
  - You want to keep serving the last good value while the upstream is down.
*/
package sfcache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// initialCapacity bounds how much of [Config.Size] the cache reserves up front: Size
// is a bound, not a reservation.
const initialCapacity = 1024

// bulkPurgeRatio is the fraction of the values queue [Cache.PurgeExpired] sifts out
// one entry at a time before it rebuilds the heap around the survivors instead.
// Sifting is cheaper for a sparse expiry, rebuilding for a bulk one; measured on this
// heap, the two cross between a twelfth and a tenth of the queue.
const bulkPurgeRatio = 12

// ErrNilLookupFunc is returned by [Cache.Lookup] when the cache was constructed
// with a nil lookup function.
var ErrNilLookupFunc = errors.New("sfcache: the lookup function is nil")

// ErrLookupAborted is returned by [Cache.Lookup] when the caller's context ends
// while waiting for an in-flight lookup, or when it has already ended before an
// external lookup would start. It wraps the context error, so errors.Is with
// [context.Canceled] or [context.DeadlineExceeded] keeps working.
var ErrLookupAborted = errors.New("sfcache: lookup aborted by context")

// ErrInvalidKey is returned by [Cache.Lookup] for a key that is not equal to
// itself: one that is or contains a NaN (a float field of a struct key is
// enough). Such a key hashes to a map slot that no subsequent lookup can reach,
// so it can be neither cached nor coalesced.
var ErrInvalidKey = errors.New("sfcache: the key is not equal to itself (NaN) and can never be cached")

// LookupFunc is the generic function signature for external lookup calls.
type LookupFunc[K comparable, V any] func(ctx context.Context, key K) (V, error)

// Cache is a generic, size-bounded single-flight cache with TTL expiration.
//
// It must be used through the pointer returned by [New] and never copied. A copy
// shares the original's maps but gets its own lock and its own copy of the eviction
// queues, so the two diverge on the first store through either handle and corrupt each
// other's queues. go vet's copylocks check rejects the copy.
type Cache[K comparable, V any] struct {
	// keymap holds the COMPLETED entries: values, error residue, and revived stale
	// values. A lookup in flight is not in here; it lives in flights.
	// INVARIANT: every entry in here is filed in exactly one of vic's three queues.
	// [Cache.store] and [Cache.drop] maintain that one entry at a time;
	// [Cache.PurgeExpired] and [Cache.resetLocked] maintain it in bulk.
	keymap map[K]*entry[V]

	// flights holds the lookups in progress, keyed by the key being resolved. Keeping
	// them out of keymap is what bounds the capacity by the values held, and what keeps
	// a lookup in flight from being evicted: it holds no entry.
	// INVARIANT: a key is never in both maps, and a registered flight is never finished.
	flights map[K]*flight

	// lookupFn is the function performing the external lookup call.
	lookupFn LookupFunc[K, V]

	// ttlFn optionally computes a per-entry TTL (see [WithTTLFunc]).
	ttlFn TTLFunc[K, V]

	// mux guards everything above. It is a value rather than a pointer so that go vet's
	// copylocks check rejects an accidental copy of the Cache.
	mux sync.RWMutex

	// ttl is the default time-to-live of an entry (see [Config.TTL]).
	ttl time.Duration

	// maxStale bounds how long past its original expiration a value may be served when
	// a refresh fails (see [Config.MaxStale]). Zero disables it.
	maxStale time.Duration

	// maxStaleOnFailure bounds how long past the first failed refresh a value may be
	// served (see [Config.MaxStaleOnFailure]). Zero disables it.
	maxStaleOnFailure time.Duration

	// vic holds every entry of keymap, filed in one of three queues by how expendable it
	// is, and chooses the victim of an eviction. It has no access to keymap (see
	// [victims]).
	// INVARIANT: keymap and vic are only ever mutated together.
	vic victims[K, V]

	// size is the maximum number of values held (min = 1, see [Config.Size]).
	size int
}

// Config holds the settings of a [Cache] that do not depend on its key and value
// types; those that do live in an [Option].
//
// The zero Config is valid: a single-entry cache that caches no value and only
// coalesces concurrent lookups.
type Config struct {
	// Size is the maximum number of VALUES the cache holds. A Size <= 0 is clamped
	// to 1. It is not a hard bound on [Cache.Len] (see the capacity section of the
	// package documentation).
	Size int

	// TTL is the default time-to-live of a successfully looked up value.
	// A TTL <= 0 disables value caching while still coalescing duplicate in-flight
	// requests for the same key (unless a per-entry TTL is set via [WithTTLFunc]).
	// A negative TTL behaves exactly like a zero one.
	TTL time.Duration

	// MaxStale enables RFC 5861 stale-if-error semantics: when a refresh of an expired
	// key fails, the last known good value is returned instead (with a nil error), but
	// only until its ORIGINAL expiration plus MaxStale.
	//
	// The window is anchored to the value's expiration, not to the failure, so a key
	// idle for longer than TTL + MaxStale gets no protection at all. Use
	// [Config.MaxStaleOnFailure] to protect rarely fetched keys.
	//
	// A MaxStale <= 0 disables it (default).
	MaxStale time.Duration

	// MaxStaleOnFailure enables failure-anchored stale-if-error: when a refresh of an
	// expired key fails, the last known good value is returned instead (with a nil
	// error) for up to MaxStaleOnFailure measured from that first failure, however long
	// the key had been idle before it. Unlike [Config.MaxStale] it holds for cold keys.
	//
	// The window is anchored once, by the first failed refresh: further failures keep
	// serving the same value until that deadline but never push it back, so a
	// permanently failing upstream cannot make a value immortal.
	//
	// When both are set, the value is served stale until the later of the two
	// deadlines. A MaxStaleOnFailure <= 0 disables it (default).
	MaxStaleOnFailure time.Duration
}

// New constructs a single-flight cache with the given lookup function and
// configuration. If lookupFn is nil, a default one is used that always fails with
// [ErrNilLookupFunc].
func New[K comparable, V any](lookupFn LookupFunc[K, V], cfg Config, opts ...Option[K, V]) *Cache[K, V] {
	if lookupFn == nil {
		lookupFn = func(_ context.Context, _ K) (V, error) {
			var zero V

			return zero, ErrNilLookupFunc
		}
	}

	size := cfg.Size
	if size <= 0 {
		size = 1
	}

	c := &Cache[K, V]{
		lookupFn:          lookupFn,
		ttl:               cfg.TTL,
		maxStale:          cfg.MaxStale,
		maxStaleOnFailure: cfg.MaxStaleOnFailure,
		size:              size,
		keymap:            make(map[K]*entry[V], min(size, initialCapacity)),
		flights:           make(map[K]*flight),
		vic: victims[K, V]{
			maxStale:          cfg.MaxStale,
			maxStaleOnFailure: cfg.MaxStaleOnFailure,
		},
	}
	// NOTE: neither the queues nor the map are preallocated to Size. They grow as they
	// are used, so an effectively unbounded cache (a Size near math.MaxInt) costs
	// nothing up front.

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Len returns the current number of entries, including the expired ones not yet
// reclaimed and the keys being resolved. It can therefore exceed [Config.Size], which
// bounds only the values held.
func (c *Cache[K, V]) Len() int {
	c.mux.RLock()
	defer c.mux.RUnlock()

	return len(c.keymap) + len(c.flights)
}

// Reset clears all entries, including the lookups in flight, whose results will
// not be cached. Callers waiting on one are released and retry with a fresh
// lookup.
func (c *Cache[K, V]) Reset() {
	// Finish the invalidated flights only after the lock is released: waking the parked
	// callers is O(waiters) work, and deregistering them under the lock is what makes
	// the state they wake to terminal.
	for _, fl := range c.resetLocked() {
		fl.finish()
	}
}

// resetLocked swaps in fresh maps and returns the flights it invalidated, for the
// caller to finish once the lock is released.
func (c *Cache[K, V]) resetLocked() map[K]*flight {
	// Build the replacements BEFORE taking the lock: only the swap has to be exclusive.
	// c.size is fixed at construction, so reading it here is safe.
	keymap := make(map[K]*entry[V], min(c.size, initialCapacity))
	empty := make(map[K]*flight)

	c.mux.Lock()
	defer c.mux.Unlock()

	flights := c.flights

	c.keymap = keymap
	c.flights = empty

	c.vic.reset()

	return flights
}

// Remove deletes the entry for the key. If a lookup for it is in flight, its
// result will not be cached, and the callers waiting on it are released and retry
// with a fresh lookup.
func (c *Cache[K, V]) Remove(key K) {
	// Finish the invalidated flight after the lock is released: see [Cache.Reset].
	if fl := c.removeLocked(key); fl != nil {
		fl.finish()
	}
}

// removeLocked drops the entry and deregisters the flight for the key, returning
// the invalidated flight (if any) for the caller to finish once the lock is
// released.
func (c *Cache[K, V]) removeLocked(key K) *flight {
	c.mux.Lock()
	defer c.mux.Unlock()

	c.drop(key)

	fl, ok := c.flights[key]
	if !ok {
		return nil
	}

	delete(c.flights, key)

	return fl
}

// PurgeExpired removes every expired entry and returns how many it removed.
// Lookups in flight are not affected.
//
// NOTE: this forfeits stale-if-error protection for every key it purges, not only for
// those a failed refresh already revived: any value retained to be served stale rides on
// an expired entry. Calling PurgeExpired on a timer voids the protection that
// [Config.MaxStale] and [Config.MaxStaleOnFailure] provide, before any outage happens.
//
// NOTE: it is the longest hold of the exclusive write lock this package takes, blocking
// every other caller for the whole pass. Calling it is rarely necessary: an expired
// entry is reclaimed for free by the next store that needs its room.
func (c *Cache[K, V]) PurgeExpired() int {
	c.mux.Lock()
	defer c.mux.Unlock()

	purged := 0
	forget := func(key K, _ *entry[V]) {
		if _, ok := c.keymap[key]; !ok {
			return // a queue naming a key the map does not hold must not be counted
		}

		delete(c.keymap, key)

		purged++
	}

	// Error residue and revived values are stored ALREADY expired, so every one of them
	// goes: take both queues wholesale.
	c.vic.residue.drain(forget)
	c.vic.stale.drain(forget)

	c.purgeValues(time.Now(), forget)

	return purged
}

// purgeValues removes the expired values: it sifts them out of the head of the queue
// one at a time, and falls back to rebuilding the heap around the survivors once more
// than [bulkPurgeRatio] of them have gone.
//
// The values are ordered by expiration, so the expired ones are exactly the head of the
// queue and the valid ones are never looked at.
// NOTE: this is not thread-safe, it should be called within a mutex lock.
func (c *Cache[K, V]) purgeValues(now time.Time, forget func(key K, item *entry[V])) {
	expired := func(item *entry[V]) bool { return item.expired(now) }

	budget := (c.vic.values.len() / bulkPurgeRatio) + 1

	for range budget {
		key, item, ok := c.vic.values.top()
		if !ok || !expired(item) {
			return // the head is valid, so nothing behind it has expired either
		}

		forget(key, item)
		c.vic.values.remove(item)
	}

	// The budget ran out. If it ran out exactly ON the last expired entry there is
	// nothing left to remove, and the pass below would walk every survivor to find that
	// out.
	if _, item, ok := c.vic.values.top(); !ok || !expired(item) {
		return
	}

	// Too many have expired to sift out one by one: keep the survivors and rebuild the
	// heap around them, in one linear pass.
	c.vic.values.partition(expired, forget)
}
