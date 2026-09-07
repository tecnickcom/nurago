/*
Package sliceutil filters, maps, and reduces slices with generic functions, and
summarizes numeric slices with descriptive statistics.

# What It Provides

Functional slice primitives, generic over `S ~[]E`, whose callbacks receive the
element index:

  - [Filter]: returns a new slice containing elements that satisfy a predicate.
  - [Map]: returns a new slice with each element transformed to a new type.
  - [Reduce]: folds a slice into a single value using an accumulator.

Descriptive statistics for numeric slices:

  - [Stats]: computes a [DescStats] summary for any numeric slice type, and
    returns [ErrEmptySlice] for an empty slice.
  - [DescStats] includes count, sum, min/max (+ indexes), range, mode,
    mean/median, entropy, variance, standard deviation, skewness, and excess
    kurtosis.

# Usage

	adults := sliceutil.Filter(users, func(_ int, u User) bool { return u.Age >= 18 })
	names := sliceutil.Map(adults, func(_ int, u User) string { return u.Name })
	total := sliceutil.Reduce([]int{1, 2, 3, 4}, 0, func(_ int, v int, acc int) int {
	    return acc + v
	})
	_ = names
	_ = total

	ds, err := sliceutil.Stats([]int{53, 83, 13, 79})
	if err != nil {
	    return err
	}
	_ = ds.Mean

# When To Use

  - You want map, filter, and reduce over slices without writing loops each
    time.
  - A numeric slice needs mean, median, or percentile values.
*/
package sliceutil

// Filter returns a new slice containing only elements for which f returns true.
func Filter[S ~[]E, E any](s S, f func(int, E) bool) S {
	r := make(S, 0, len(s))

	for k, v := range s {
		if f(k, v) {
			r = append(r, v)
		}
	}

	return r
}

// Map returns a new slice containing f applied to each element of s.
func Map[S ~[]E, E any, U any](s S, f func(int, E) U) []U {
	r := make([]U, len(s))

	for k, v := range s {
		r[k] = f(k, v)
	}

	return r
}

// Reduce folds s into one value by repeatedly applying f to each element and accumulator state.
func Reduce[S ~[]E, E any, U any](s S, init U, f func(int, E, U) U) U {
	r := init

	for k, v := range s {
		r = f(k, v, r)
	}

	return r
}
