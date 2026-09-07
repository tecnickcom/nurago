/*
Package maputil filters, maps, reduces, and inverts Go maps with generic
functions.

It offers a small set of generic, allocation-conscious helpers:
  - [Filter] keeps entries matching a predicate.
  - [Map] transforms key/value pairs into a new map type.
  - [Reduce] folds all entries into a single accumulator value.
  - [Invert] swaps keys and values.

All functions are pure map-to-map/map-to-value transforms: they return new
results and never mutate the input map directly.

# Important Semantics

Go map iteration order is intentionally randomized. Therefore:
  - [Reduce] results are deterministic only when the reducing function is
    order-independent (for example, commutative/associative operations).
  - [Map] and [Invert] follow "last write wins" semantics when multiple input
    entries map to the same output key.

# When To Use

  - You transform, filter, or invert maps regularly.
  - You want the operation expressed once rather than as a loop at each call
    site.
*/
package maputil

// Filter returns new map containing only entries where predicate f returns true; input map is not modified.
func Filter[M ~map[K]V, K comparable, V any](m M, f func(K, V) bool) M {
	r := make(M, len(m))

	for k, v := range m {
		if f(k, v) {
			r[k] = v
		}
	}

	return r
}

// Map transforms each entry of m using f, with last-write-wins semantics for duplicate output keys.
func Map[M ~map[K]V, K, J comparable, V, U any](m M, f func(K, V) (J, U)) map[J]U {
	r := make(map[J]U, len(m))

	for k, v := range m {
		j, u := f(k, v)
		r[j] = u
	}

	return r
}

// Reduce folds m into single value by repeatedly applying f to each entry and accumulator; f should be order-independent for deterministic output.
func Reduce[M ~map[K]V, K comparable, V, U any](m M, init U, f func(K, V, U) U) U {
	r := init

	for k, v := range m {
		r = f(k, v, r)
	}

	return r
}

// Invert returns new map with swapped keys/values, with last-write-wins semantics for duplicate input values.
func Invert[M ~map[K]V, K, V comparable](m M) map[V]K {
	r := make(map[V]K, len(m))

	for k, v := range m {
		r[v] = k
	}

	return r
}
