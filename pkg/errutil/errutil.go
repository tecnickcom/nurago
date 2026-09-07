/*
Package errutil annotates errors with caller location, joins cleanup failures
onto an existing error, and enumerates the errors inside an errors.Join value.

  - Trace annotates an error with runtime caller metadata (file, line, function)
    while preserving the original error with %w wrapping, so errors.Is and
    errors.As continue to work.
  - JoinFnError executes an error-producing function and joins its result into an
    existing error value using errors.Join, for defer/cleanup logic where
    secondary failures must not overwrite the primary error.
  - Errors enumerates the individual errors aggregated within an errors.Join
    value, returning a single-element slice for a plain error and nil for nil.
  - Trace(nil) returns nil, and JoinFnError supports nil and non-nil combinations
    through errors.Join semantics.

# When To Use

  - Errors cross several layers and the origin must survive the trip.
  - You want consistent wrapping rather than ad-hoc fmt.Errorf calls.
*/
package errutil
