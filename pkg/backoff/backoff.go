/*
Package backoff computes successive retry delays with exponential growth, a
bounded maximum, and random jitter.

[Schedule] is a per-call delay calculator: given a base delay, a growth factor,
a jitter ceiling, and a maximum, each call to [Schedule.Next] returns the next
delay and advances the progression. It performs no timing, scheduling,
goroutines, or I/O; callers own their timers and loops and ask the Schedule for
the next duration.

[AddJitter] exposes the jitter step on its own for callers that pace work at a
fixed interval rather than backing off exponentially.

# Jitter strategies

By default a Schedule adds a fixed random ceiling ([JitterAdditive]); its
desynchronizing effect shrinks as the delay grows. [JitterFull] and
[JitterEqual] instead scale the jitter with the delay, decorrelating concurrent
clients at large delays. Select one via Config.Strategy.

# Bounds and overflow

Both the internal progression and the jitter addition are clamped so no delay
can overflow into a negative duration, regardless of factor, attempt count, or
configured maximum. The growth is capped below the int64 limit (~146 years),
and the jitter addition saturates at [math.MaxInt64] rather than wrapping.
Negative or NaN inputs are floored to zero.

Delays are computed in float64, so integer-nanosecond precision is exact only up
to ~2^53 ns (~104 days); beyond that a result may differ by a few nanoseconds.

# Usage

	s := backoff.New(backoff.Config{
	    Base:     100 * time.Millisecond,
	    Factor:   2,
	    Jitter:   50 * time.Millisecond,
	    MaxDelay: 30 * time.Second,
	})

	for {
	    // ... attempt work ...
	    time.Sleep(s.Next()) // 100ms, 200ms, 400ms, ... capped at 30s, each +[0,50ms)
	}

# Concurrency

A [Schedule] is stateful and must not be used concurrently; construct one per
retry sequence. [AddJitter] is a stateless function and is safe for concurrent
use.

# When To Use

  - You already have a retry loop and only need the delay schedule.
  - Concurrent clients retry against the same dependency and must not
    synchronize into a thundering herd.
  - You want the jitter strategy to be an explicit choice rather than a
    hidden default.
*/
package backoff

import (
	"math"
	"math/rand/v2"
	"time"
)

// maxSafeDelay caps the internal exponential state well below math.MaxInt64
// nanoseconds (~146 years). Keeping the progression at or below this bound
// guarantees the float64-to-int64 conversion in [Schedule.Next] and the jitter
// addition can never overflow into a negative duration at high attempt counts.
const maxSafeDelay = time.Duration(math.MaxInt64 / 2)

// JitterStrategy selects how random jitter is derived for each backoff delay.
//
// The additive strategy adds a fixed ceiling, whose desynchronizing effect
// shrinks as the delay grows; the full and equal strategies scale the jitter
// with the delay itself and therefore decorrelate concurrent clients better
// at large delays.
type JitterStrategy int

const (
	// JitterAdditive adds a fixed random duration in [0, Config.Jitter) on top of
	// the clamped exponential delay. It is the default. Because the ceiling is
	// fixed, its decorrelating effect shrinks as the delay grows.
	JitterAdditive JitterStrategy = iota

	// JitterFull replaces the delay with a uniform random value in [0, delay),
	// where delay is the clamped exponential value ("full jitter"). It
	// decorrelates retries best because the spread scales with the delay.
	// Config.Jitter is ignored.
	JitterFull

	// JitterEqual keeps half of the clamped exponential delay and randomizes the
	// rest, yielding a wait in [delay/2, delay) (integer division, so the floor is
	// floor(delay/2)). It trades some decorrelation for a guaranteed minimum wait.
	// Config.Jitter is ignored.
	JitterEqual
)

// Valid reports whether s is a defined [JitterStrategy].
func (s JitterStrategy) Valid() bool {
	return s >= JitterAdditive && s <= JitterEqual
}

// Config defines the parameters of an exponential backoff [Schedule].
//
// The zero value is not meaningful; construct a fully specified Config. [New]
// does not validate these (callers that retry typically validate via their own
// options): out-of-range values degrade gracefully rather than panic, as
// documented on [New].
type Config struct {
	// Base is the pre-jitter delay returned by the first [Schedule.Next] call,
	// before any growth is applied. Should be > 0.
	Base time.Duration

	// Factor multiplies the pre-jitter delay after each [Schedule.Next] call.
	// Use >= 1 for non-decreasing backoff; 1 keeps the delay constant at Base.
	Factor float64

	// Jitter is the exclusive upper bound of the random duration added to each
	// returned delay to desynchronize concurrent clients. <= 0 disables jitter.
	// Only [JitterAdditive] uses this; the other strategies scale jitter with the
	// delay and ignore it.
	Jitter time.Duration

	// MaxDelay caps the pre-jitter delay. <= 0 applies only the internal safety
	// cap (maxSafeDelay).
	MaxDelay time.Duration

	// Strategy selects how jitter is derived from each delay (default
	// [JitterAdditive]). See [JitterStrategy].
	Strategy JitterStrategy
}

// Schedule generates successive exponential-backoff delays with clamping and
// jitter. It is a pure per-call calculator that performs no timing, scheduling,
// or I/O.
//
// A Schedule is stateful (each [Schedule.Next] advances the progression) and
// is therefore not safe for concurrent use. Create one per retry sequence.
type Schedule struct {
	next     float64        // pre-jitter delay for the next Next call, in nanoseconds
	factor   float64        // multiplicative growth factor applied after each call
	jitter   time.Duration  // additive jitter ceiling (exclusive)
	maxDelay time.Duration  // pre-jitter clamp; <= 0 means maxSafeDelay only
	strategy JitterStrategy // how jitter is derived from each delay
}

// New returns a [Schedule] for the given [Config].
//
// New never fails and never panics. Callers that need validation should perform
// it before constructing. Out-of-range inputs degrade gracefully: the pre-jitter
// delay is floored to the non-negative range [0, maxDelay], so a non-positive
// Base, a Factor < 1, and even a negative or NaN Factor can never yield a
// negative delay. The exact non-negative sequence for such inputs is
// unspecified (a negative Factor, for example, oscillates rather than decaying),
// but every value is non-negative and the growth stays bounded by maxSafeDelay.
func New(cfg Config) *Schedule {
	return &Schedule{
		next:     float64(cfg.Base),
		factor:   cfg.Factor,
		jitter:   cfg.Jitter,
		maxDelay: cfg.MaxDelay,
		strategy: cfg.Strategy,
	}
}

// Next returns the next backoff delay and advances the progression.
//
// The pre-jitter delay is clamped to MaxDelay (or the internal safety cap when
// MaxDelay <= 0), jitter is applied according to the configured [JitterStrategy],
// and the stored progression is multiplied by Factor and re-capped for the
// following call.
func (s *Schedule) Next() time.Duration {
	clamp := s.maxDelay
	if clamp <= 0 || clamp > maxSafeDelay {
		clamp = maxSafeDelay
	}

	delay := s.next
	if math.IsNaN(delay) || delay < 0 {
		// Out-of-contract Base/Factor (negative or NaN) would otherwise flow into
		// a negative int64 conversion; floor to zero so the delay is never negative.
		delay = 0
	}

	if delay > float64(clamp) {
		delay = float64(clamp)
	}

	d := s.applyJitter(time.Duration(int64(delay)))

	s.next *= s.factor
	if s.next > float64(maxSafeDelay) {
		s.next = float64(maxSafeDelay)
	}

	return d
}

// applyJitter derives the returned delay from the clamped exponential delay
// according to the configured [JitterStrategy]. An unknown strategy falls back
// to [JitterAdditive].
func (s *Schedule) applyJitter(delay time.Duration) time.Duration {
	switch s.strategy {
	case JitterFull:
		return AddJitter(0, delay)
	case JitterEqual:
		half := delay / 2

		return AddJitter(half, delay-half)
	case JitterAdditive:
		return AddJitter(delay, s.jitter)
	default:
		// Unknown strategy: fall back to additive.
		return AddJitter(delay, s.jitter)
	}
}

// AddJitter returns base plus a uniform random duration in [0, ceil).
//
// A ceil <= 0 disables jitter and returns base unchanged. The addition is
// overflow-safe: if base+jitter would exceed [math.MaxInt64] nanoseconds it
// saturates at math.MaxInt64 instead of wrapping. base is expected to be >= 0.
//
// AddJitter is safe for concurrent use.
func AddJitter(base, ceil time.Duration) time.Duration {
	if ceil <= 0 {
		return base
	}

	return time.Duration(addJitterValue(int64(base), rand.Int64N(int64(ceil)))) //nolint:gosec // jitter is decorative, not security-sensitive
}

// addJitterValue adds a non-negative jitter to base, saturating at
// math.MaxInt64 on int64 overflow. It is split out from [AddJitter] so the
// overflow guard is testable independently of the random draw.
func addJitterValue(base, jitter int64) int64 {
	sum := base + jitter
	if sum < base {
		// base+jitter overflowed int64 (jitter is non-negative).
		return math.MaxInt64
	}

	return sum
}
