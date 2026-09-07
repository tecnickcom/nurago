/*
Package dnscache provides a local DNS cache that is safe for concurrent use,
bounded in size, and uses single-flight request collapsing to avoid duplicate
lookups.

It exposes [Cache.LookupHost] and [Cache.DialContext], a drop-in replacement for
an http.Transport DialContext.

# Caching

Resolved host names are cached for one cache-wide TTL; the authoritative DNS
record TTLs are not consulted. Concurrent callers asking for the same host share
a single lookup.

Host names are matched case-insensitively and an equivalent trailing dot (FQDN
form) is ignored, so "Example.com", "example.com" and "example.com." share a
single entry. A custom [Resolver] therefore observes the normalized name. Host
names that are already IP literals bypass the resolver and the cache entirely,
mirroring net.Resolver.LookupHost.

The configured size bounds the number of cached address sets, never the number of
distinct hosts looked up; [Cache.Len] can exceed it.

# Dialing

[Cache.DialContext] dials the resolved IPs sequentially (not raced) in the
resolver's preference order, interleaving address families (leading with the
resolver-preferred one) so a dead family is not exhausted before the other is
tried. Family-restricted networks ("tcp4", "udp6", ...) dial only addresses of the
matching family.

[WithDialer] configures the dialer, [WithDialTimeout] bounds each attempt, and
[WithAddressRotation] spreads connections across a host's records.

# Stale-if-error

[WithStaleOnFailure] serves the last successfully resolved addresses for a window
measured from the failed refresh, so rarely resolved hosts are protected too.
[WithStaleIfError] is the RFC 5861 variant, whose window is measured from the
addresses' original expiry.

# When To Use

  - A client dials the same hostnames repeatedly and resolver latency is
    measurable.
  - Concurrent lookups of the same name should collapse into one query.
*/
package dnscache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync/atomic"
	"time"

	"github.com/tecnickcom/nurago/pkg/sfcache"
)

// defaultKeepAlive mirrors the keep-alive used by net/http's default transport
// dialer, so the DialContext helper is a faithful drop-in replacement.
const defaultKeepAlive = 30 * time.Second

// ErrNoAddresses is returned by [Cache.DialContext] when the resolver reports
// no addresses for the requested host.
var ErrNoAddresses = errors.New("dnscache: no addresses resolved")

// ErrInvalidIP is returned by [Cache.DialContext] when a cached address is not
// a valid IP literal and therefore cannot be dialed.
var ErrInvalidIP = errors.New("dnscache: invalid IP address")

// Resolver is a net.Resolver interface for DNS lookups.
type Resolver interface {
	LookupHost(ctx context.Context, host string) (addrs []string, err error)
}

// dialFunc dials a single already-resolved address. It matches the signature of
// net.Dialer.DialContext and is the seam through which the underlying dialer is
// injected (and overridden in tests).
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Cache provides DNS resolution caching with TTL and single-flight deduplication.
// A Cache must not be copied after first use.
type Cache struct {
	cache *sfcache.Cache[string, []string]

	// dialCtx establishes a single connection to an already-resolved address.
	// It defaults to the configured net.Dialer's DialContext (see [WithDialer],
	// net.Dialer is safe for concurrent use) and is only replaced in tests.
	dialCtx dialFunc

	// dialTimeout bounds each individual dial attempt (see [WithDialTimeout]).
	// Zero leaves only the caller's context in force.
	dialTimeout time.Duration

	// rotate enables client-side rotation of the dial order (see
	// [WithAddressRotation]).
	rotate bool

	// dialSeq is the round-robin counter backing address rotation.
	dialSeq atomic.Uint64
}

// New creates a concurrent DNS cache with TTL expiry and single-flight lookups.
//
// If resolver is nil, a default net.Resolver is used. size bounds cache
// capacity (minimum effective size is 1), and ttl controls how long each
// hostname resolution remains valid. Behavior can be tuned with options such
// as [WithDialer], [WithDialTimeout], [WithAddressRotation],
// [WithStaleOnFailure], and [WithStaleIfError].
//
// A size <= 0 is clamped to a capacity of 1. A ttl <= 0 disables caching entirely
// (every call queries the resolver) while still coalescing concurrent lookups for the
// same host: a zero ttl means "never cache", not "cache forever".
func New(resolver Resolver, size int, ttl time.Duration, opts ...Option) *Cache {
	if resolver == nil {
		resolver = &net.Resolver{}
	}

	cfg := &config{
		dialer: &net.Dialer{KeepAlive: defaultKeepAlive},
	}

	for _, opt := range opts {
		opt(cfg)
	}

	lookupFn := func(ctx context.Context, key string) ([]string, error) {
		addrs, err := resolver.LookupHost(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("DNS lookup failed: %w", err)
		}

		return addrs, nil
	}

	return &Cache{
		cache: sfcache.New(lookupFn, sfcache.Config{
			Size:              size,
			TTL:               ttl,
			MaxStale:          cfg.maxStale,
			MaxStaleOnFailure: cfg.maxStaleOnFailure,
		}),
		dialCtx:     cfg.dialer.DialContext,
		dialTimeout: cfg.dialTimeout,
		rotate:      cfg.rotate,
	}
}

// LookupHost resolves host to IP addresses using cache-first semantics.
//
// On cache miss or expiry, one goroutine performs the DNS lookup while other
// concurrent callers for the same host wait and share the result. Host names
// are matched case-insensitively and a trailing dot is ignored; an IP literal
// is returned as-is without a lookup.
//
// The returned slice is a copy: callers may freely modify it without affecting the
// cached entry shared with other callers.
//
// With [WithStaleOnFailure] or [WithStaleIfError] enabled, a failed refresh
// returns the last successfully resolved addresses with a NIL error: callers
// cannot tell a stale answer from a fresh one.
func (c *Cache) LookupHost(ctx context.Context, host string) ([]string, error) {
	val, err := c.lookup(ctx, host)
	if err != nil {
		return nil, err
	}

	// Return a copy: cached values are shared by reference across callers.
	return slices.Clone(val), nil
}

// DialContext resolves address through the cache and dials the resolved IPs.
//
// It is a drop-in replacement for a transport DialContext (for example in
// http.Transport). Addresses are tried sequentially (not raced), in the resolver's
// preference order with IPv4 and IPv6 candidates interleaved (leading with the
// resolver-preferred family), until one connection succeeds; if every attempt fails,
// the individual errors are aggregated into the returned error.
//
// For family-restricted networks ("tcp4", "udp6", ...) only addresses of the matching
// family are dialed; if none remain, an error wrapping [ErrNoAddresses] is returned.
func (c *Cache) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("failed to extract host and port from %s: %w", address, err)
	}

	ips, err := c.lookup(ctx, host)
	if err != nil {
		return nil, err
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("%w for %s", ErrNoAddresses, host)
	}

	cands := c.orderCandidates(network, ips)
	if len(cands) == 0 {
		return nil, fmt.Errorf("%w for %s on network %s", ErrNoAddresses, host, network)
	}

	conn, err := c.dialAddrs(ctx, network, port, cands)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", address, err)
	}

	return conn, nil
}

// Len reports the current number of cached host entries.
//
// It is not the cache's occupancy against its capacity: it also counts the hosts being
// resolved and the residue of a failed resolution, neither of which holds addresses, so
// it can exceed the configured size. The overrun is bounded by the caller's own request
// concurrency plus two, never by the number of distinct hosts looked up.
//
// During an outage, a stale serve that could evict nothing holds one address set more
// (see [WithStaleOnFailure]). It is reclaimed by the next host successfully resolved.
func (c *Cache) Len() int {
	return c.cache.Len()
}

// Reset clears all cached DNS entries.
//
// Resolutions in flight are invalidated: their results are returned to the
// callers that started them but not cached, and callers waiting on them are
// released to resolve again.
func (c *Cache) Reset() {
	c.cache.Reset()
}

// Remove evicts a single host entry from the cache. The host is normalized the
// same way as in [Cache.LookupHost], so any case or trailing-dot variant of a
// cached name removes the shared entry.
//
// A resolution in flight for the host is invalidated: its result is returned to
// the caller that started it but not cached, and callers waiting on it are
// released to resolve again, so a second concurrent resolver call for that host
// may start alongside the one it superseded.
func (c *Cache) Remove(host string) {
	c.cache.Remove(normalizeHost(host))
}

// PurgeExpired removes all expired host entries from the cache and returns
// the number of entries removed. Expired entries are otherwise only removed
// lazily, when capacity pressure or a new lookup replaces them.
//
// NOTE: it also removes the addresses retained by [WithStaleOnFailure] or
// [WithStaleIfError], forfeiting stale protection for those hosts until the
// next successful resolution. Calling it on a timer therefore voids the outage
// protection those options provide.
func (c *Cache) PurgeExpired() int {
	return c.cache.PurgeExpired()
}

// lookup resolves host through the cache and returns the shared cached slice.
// An IP-literal host bypasses the resolver and the cache entirely (as
// net.Resolver.LookupHost does). The returned slice is owned by the cache and
// MUST be treated as read-only by internal callers; the exported
// [Cache.LookupHost] clones it for the public.
func (c *Cache) lookup(ctx context.Context, host string) ([]string, error) {
	if isIPLiteral(host) {
		// Already an IP literal: nothing to resolve or cache.
		return []string{host}, nil
	}

	val, err := c.cache.Lookup(ctx, normalizeHost(host))
	if err != nil {
		return nil, fmt.Errorf("unable to resolve %s: %w", host, err)
	}

	return val, nil
}

// isIPLiteral reports whether host is already a valid IP address literal.
func isIPLiteral(host string) bool {
	_, err := netip.ParseAddr(host)

	return err == nil
}

// dialAddrs dials the ordered candidates, returning the first successful
// connection or the aggregate of every failure. It stops early if the caller's
// context ends mid-loop.
// NOTE: cands must be non-empty, so that a nil connection is never returned
// alongside a nil error; [Cache.DialContext] guarantees it.
func (c *Cache) dialAddrs(ctx context.Context, network, port string, cands []dialCandidate) (net.Conn, error) {
	var errs []error

	for _, cand := range cands {
		cerr := ctx.Err()
		if cerr != nil {
			errs = append(errs, cerr)

			break
		}

		if !cand.addr.IsValid() {
			errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidIP, cand.raw))

			continue
		}

		// Dial the canonical address form (unmapped, lower-cased) so the dialed
		// literal matches the family the candidate was classified and filtered
		// as; cand.raw is kept only for the invalid-entry error above.
		dctx, cancel := c.attemptContext(ctx)
		conn, err := c.dialCtx(dctx, network, net.JoinHostPort(cand.addr.String(), port))

		cancel()

		if err == nil {
			return conn, nil
		}

		errs = append(errs, err)
	}

	return nil, errors.Join(errs...)
}

// attemptContext derives the context for a single dial attempt, applying the
// per-attempt timeout when one is configured. The returned cancel func must
// always be called.
func (c *Cache) attemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.dialTimeout <= 0 {
		// A capture-free func literal compiles to a static value: no
		// per-attempt allocation happens here.
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, c.dialTimeout)
}

// orderCandidates parses the resolved addresses into dial candidates and
// orders them for the given network: candidates unusable on a
// family-restricted network ("tcp4", "udp6", ...) are dropped first, then the
// remainder is optionally rotated (see [WithAddressRotation]) and interleaved
// by address family, always leading with the family of the first usable
// candidate (the resolver-preferred family). Rotation is applied within each
// family so it never changes which family leads.
func (c *Cache) orderCandidates(network string, ips []string) []dialCandidate {
	cands := filterByNetwork(newCandidates(ips), network)
	if len(cands) < 2 {
		return cands
	}

	lead := isIPv6Addr(cands[0].addr)

	if !isMixedFamily(cands, lead) {
		// Single family: nothing to interleave, only rotation applies.
		if c.rotate {
			return rotateCandidates(cands, c.nextDialOffset())
		}

		return cands
	}

	first, second := splitByFamily(cands, lead)

	if c.rotate {
		// One shared offset per dial: rotating each family group by its own
		// counter value would advance the counter twice per call and could
		// leave an even-sized group stuck on the same head.
		offset := c.nextDialOffset()
		first = rotateCandidates(first, offset)
		second = rotateCandidates(second, offset)
	}

	return interleave(first, second)
}

// filterByNetwork returns the candidates usable on the given network: networks
// ending in '4' (e.g. "tcp4") keep IPv4 candidates, networks ending in '6'
// keep IPv6 candidates, and any other network keeps all. Invalid (non-IP)
// entries are always kept so they still surface as [ErrInvalidIP].
func filterByNetwork(cands []dialCandidate, network string) []dialCandidate {
	if network == "" {
		return cands
	}

	switch network[len(network)-1] {
	case '4':
		return filterByFamily(cands, false)
	case '6':
		return filterByFamily(cands, true)
	default:
		return cands
	}
}

// filterByFamily keeps the candidates of the wanted family (IPv6 when v6 is
// true) plus invalid entries, preserving order.
func filterByFamily(cands []dialCandidate, v6 bool) []dialCandidate {
	out := make([]dialCandidate, 0, len(cands))

	for _, c := range cands {
		if !c.addr.IsValid() || (isIPv6Addr(c.addr) == v6) {
			out = append(out, c)
		}
	}

	return out
}

// nextDialOffset returns the current round-robin offset and advances it.
func (c *Cache) nextDialOffset() uint64 {
	return c.dialSeq.Add(1) - 1
}

// normalizeHost canonicalizes host for cache-key use so DNS case-insensitivity
// (RFC 4343) and the equivalent trailing-dot FQDN form do not fragment the
// cache into duplicate entries. Only ASCII A-Z are folded; the DNS root "."
// and non-ASCII bytes are left untouched.
func normalizeHost(host string) string {
	if n := len(host); n > 1 && host[n-1] == '.' {
		host = host[:n-1]
	}

	return asciiLower(host)
}

// asciiLower folds ASCII A-Z to a-z in a single pass, leaving every non-ASCII
// byte untouched. An already-lower-case host is returned without a copy.
//
// It is deliberately not strings.ToLower: DNS case-insensitivity is ASCII-only
// (RFC 4343), whereas strings.ToLower folds Unicode and would rewrite a
// non-ASCII label into a DIFFERENT name ("İSTANBUL" -> "istanbul", fullwidth
// "Ａ" -> "ａ", U+212A KELVIN -> "k") before it reaches the resolver and the
// cache key.
func asciiLower(host string) string {
	var b []byte

	for i := range len(host) {
		c := host[i]
		if c < 'A' || c > 'Z' {
			continue
		}

		if b == nil {
			b = []byte(host)
		}

		b[i] = c + ('a' - 'A')
	}

	if b == nil {
		return host
	}

	return string(b)
}

// dialCandidate pairs a resolved address string with its canonical parsed form
// (IPv4-mapped IPv6 unmapped) so the dial path classifies, validates, and
// de-duplicates each address exactly once. A non-IP entry yields an invalid
// addr that [Cache.DialContext] reports as [ErrInvalidIP].
type dialCandidate struct {
	raw  string
	addr netip.Addr
}

// newCandidates parses and de-duplicates resolved addresses into dial
// candidates, preserving first-seen order. Duplicates are detected on the
// canonical parsed address, so equivalent spellings such as
// "2001:DB8::1"/"2001:db8::1" and "::ffff:192.0.2.1"/"192.0.2.1" collapse
// onto the first occurrence; invalid entries fall back to raw-string
// comparison.
func newCandidates(ips []string) []dialCandidate {
	cands := make([]dialCandidate, 0, len(ips))

	for _, ip := range ips {
		addr, _ := netip.ParseAddr(ip)
		cand := dialCandidate{raw: ip, addr: addr.Unmap()}

		if !containsCandidate(cands, cand) {
			cands = append(cands, cand)
		}
	}

	return cands
}

// containsCandidate reports whether cands already holds a candidate for the
// same destination as cand.
func containsCandidate(cands []dialCandidate, cand dialCandidate) bool {
	for _, c := range cands {
		if sameDestination(c, cand) {
			return true
		}
	}

	return false
}

// sameDestination reports whether two candidates identify the same dial
// destination: equal canonical addresses for valid entries, or equal raw
// strings for invalid ones.
func sameDestination(a, b dialCandidate) bool {
	if a.addr.IsValid() && b.addr.IsValid() {
		return a.addr == b.addr
	}

	return !a.addr.IsValid() && !b.addr.IsValid() && (a.raw == b.raw)
}

// rotateCandidates returns cands rotated left by a round-robin offset so the
// first-tried address varies between calls. Slices shorter than two elements
// are returned unchanged. The input slice is never mutated.
func rotateCandidates(cands []dialCandidate, offset uint64) []dialCandidate {
	n := len(cands)
	if n < 2 {
		return cands
	}

	rotated := make([]dialCandidate, n)
	start := int(offset % uint64(n))

	for i := range cands {
		rotated[i] = cands[(start+i)%n]
	}

	return rotated
}

// interleave merges two non-empty candidate groups by alternating between
// them, starting with the lead group and flushing the longer one's tail.
func interleave(first, second []dialCandidate) []dialCandidate {
	out := make([]dialCandidate, 0, len(first)+len(second))

	for i := 0; i < len(first) || i < len(second); i++ {
		if i < len(first) {
			out = append(out, first[i])
		}

		if i < len(second) {
			out = append(out, second[i])
		}
	}

	return out
}

// isMixedFamily reports whether cands holds addresses of more than one family,
// given the lead family (IPv6 when lead is true).
func isMixedFamily(cands []dialCandidate, lead bool) bool {
	for _, c := range cands {
		if isIPv6Addr(c.addr) != lead {
			return true
		}
	}

	return false
}

// splitByFamily partitions cands into the lead family and the other family,
// preserving relative order within each group.
func splitByFamily(cands []dialCandidate, lead bool) ([]dialCandidate, []dialCandidate) {
	var first, second []dialCandidate

	for _, c := range cands {
		if isIPv6Addr(c.addr) == lead {
			first = append(first, c)

			continue
		}

		second = append(second, c)
	}

	return first, second
}

// isIPv6Addr reports whether addr is a genuine IPv6 address (not an
// IPv4-mapped one, and not the invalid zero value).
func isIPv6Addr(addr netip.Addr) bool {
	return addr.Is6() && !addr.Is4In6()
}
