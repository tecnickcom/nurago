/*
Package filter provides declarative, rule-based filtering for in-memory slices.
It evaluates structured [Rule] expressions against slice elements and filters the
slice in place.

Rules are grouped as `[][]Rule` with boolean semantics:
  - outer slice: AND
  - inner slice: OR

So `[[A], [B, C], [D]]` evaluates as `A AND (B OR C) AND D`. Every group must hold at least one
rule, and every rule must set all three fields; an empty group, an empty rule set, or a rule
missing a field is rejected as a malformed filter.

Rules can be supplied directly or parsed from JSON via [Processor.ParseJSON], and query
parameter payloads can be loaded with [Processor.ParseURLQuery]. A JSON rule object must carry
the "field", "type", and "value" keys (an empty "field" selects the whole element; a "value" of
null is a nil reference); the JSON grammar is defined by filter_schema.json. Supplying rules
directly as a [][]Rule in Go is not held to that shape; a Go caller may, for example, pass an
empty rule set to match every element.

# Features

  - Comparison operators: regexp, equality/equal-fold, prefix/suffix,
    contains, and numeric ordering (<, <=, >, >=) with a collection/string
    length fallback.
  - Optional negation prefix (`!`) for every operator.
  - Dot-path field selection for nested struct fields. By default selectors are matched
    against Go field names, case-sensitively (so `Address.Country`); use [WithFieldNameTag]
    to match a struct tag instead (so a JSON-style `address.country`).
  - In-place filtering with pagination-style controls via [Processor.ApplySubset]
    (offset + length), plus total-match count.
  - Limits to constrain runtime and untrusted-input cost: rule/result counts
    ([WithMaxRules], [WithMaxResults]), value length ([WithMaxValueLength]), payload
    size ([WithMaxFilterBytes]), and field-path depth ([WithMaxFieldDepth]).
  - Reflection-path caching for repeated evaluations on the same types.

# Important Behavior

  - The slice argument for [Processor.Apply] / [Processor.ApplySubset] must be a
    pointer to a slice and is modified in place: matching elements are compacted to
    the front and the slice is shortened (its backing array is not zeroed beyond the
    new length).
  - A field selector that cannot be resolved against a concrete element type (it names no
    such field, descends into a non-struct, targets an unexported field, or is deeper than
    [WithMaxFieldDepth]) is a deterministic client error, rejected with [ErrInvalidFilter]
    before any element is touched. A field that is merely unreachable on a given element makes
    that element a non-match (filtered out) rather than an error; this covers a nil pointer
    along the path, or a field absent from the concrete type of an element in an
    interface-typed slice (e.g. []any), knowable only per element. A pointer or interface leaf
    is dereferenced to the value it holds, and a nil leaf compares as a nil operand.
  - [Processor.ParseURLQuery] returns nil rules when the configured query key is
    missing or empty.
  - The `!` prefix inverts an operator's result, including for operands the base operator
    cannot handle: `!==` against a type it cannot compare, or `!<` against a non-ordered
    operand, matches. For any element whose field is actually read, exactly one of a rule and
    its negation matches. They fail to partition the slice only when the field is *unreachable*
    on an element (a nil pointer along the path, or a field absent on a []any element), since
    such an element is a non-match for both the rule and its negation (the evaluator is never
    reached). `!=` negates equal-fold (`=`), i.e. it is "not equal under case folding".
  - A [Processor] and a compiled [][]Rule are safe for concurrent use across
    goroutines. That safety does not extend to a target slice shared between
    concurrent [Processor.Apply] calls, since Apply mutates it in place.

# Security

This package performs filtering only; it does NOT perform authorization. Filter
expressions are untrusted client input, and the grammar lets a client select and
compare any exported field of the elements (including nested fields). Callers MUST
therefore apply it only to data the requesting user is already authorized to see:
pre-filter the slice to that user's permitted records (and, where relevant, project
away fields they may not read) before calling [Processor.Apply]. Passing records that
contain fields the user is not entitled to would let a crafted filter probe those
values through the match result and total-match count.

Regular-expression rules use Go's RE2 engine, which matches in linear time with no
catastrophic backtracking, so a malicious pattern cannot cause exponential-time
matching. Cost is instead driven by input size, which the Processor bounds by
default: a rule's string value (including a regexp pattern) is limited to
[DefaultMaxValueLength] bytes ([WithMaxValueLength]), the raw filter payload decoded by
[Processor.ParseJSON] and [Processor.ParseURLQuery] is limited to [DefaultMaxFilterBytes]
bytes ([WithMaxFilterBytes]), [WithMaxRules] caps how many rules may be applied, and
[WithMaxFieldDepth] bounds field-selector nesting. Array and object rule values are rejected
outright by the JSON parser. The number of distinct resolved field paths cached per Processor
is also capped internally, so filtering a recursive element type with an unbounded variety of
selectors cannot grow memory without limit. [Processor.Apply] still evaluates every
rule against every element regardless of the requested result window, so callers should also
bound the size of the slice being filtered. Decode untrusted filter JSON with
[Processor.ParseJSON] (or [Processor.ParseURLQuery]): these apply the payload-size and
rule-count limits.

Errors caused by a malformed or disallowed client filter are wrapped with
[ErrInvalidFilter]. A handler processing untrusted input can test
errors.Is(err, [ErrInvalidFilter]) to return a generic rejection (for example an HTTP
400) and log the detail server-side, rather than returning the underlying message,
which may echo client input or internal type names.

# Rule Encoding

The following pretty-printed JSON:

	[
	  [
	    {
	      "field": "name",
	      "type": "==",
	      "value": "doe"
	    },
	    {
	      "field": "age",
	      "type": "<=",
	      "value": 42
	    }
	  ],
	  [
	    {
	      "field": "address.country",
	      "type": "regexp",
	      "value": "^EN$|^FR$"
	    }
	  ]
	]

can be represented in one line as:

	[[{"field":"name","type":"==","value":"doe"},{"field":"age","type":"<=","value":42}],[{"field":"address.country","type":"regexp","value":"^EN$|^FR$"}]]

and URL-encoded as a query parameter:

	filter=%5B%5B%7B%22field%22%3A%22name%22%2C%22type%22%3A%22%3D%3D%22%2C%22value%22%3A%22doe%22%7D%2C%7B%22field%22%3A%22age%22%2C%22type%22%3A%22%3C%3D%22%2C%22value%22%3A42%7D%5D%2C%5B%7B%22field%22%3A%22address.country%22%2C%22type%22%3A%22regexp%22%2C%22value%22%3A%22%5EEN%24%7C%5EFR%24%22%7D%5D%5D

The equivalent logic is:

	((name==doe OR age<=42) AND (address.country match "EN" or "FR"))

These selectors (name, age, address.country) are JSON-style names, so the Processor must be
built with [WithFieldNameTag]("json") to resolve them against the corresponding struct tags;
see the [Processor.Apply] example. Without it, selectors resolve against Go field names
(Name, Age, Address.Country) and a JSON-style name is rejected as an unknown field selector.

# Available Rule Types

Supported rule types are:

  - `regexp` : matches the value against a reference regular expression (strings only).
  - `==`     : Equal to - matches exactly the reference value. Numbers compare numerically (so an int field equals a float reference of the same value); two nils are equal.
  - `=`      : Equal fold - for strings, matches when they are equal under simple Unicode case-folding, a more general form of case-insensitivity (for example `AB` matches `ab`); for numbers it behaves exactly like `==`.
  - `^=`     : Starts with - (strings only) matches when the value begins with the reference string.
  - `=$`     : Ends with - (strings only) matches when the value ends with the reference string.
  - `~=`     : Contains - (strings only) matches when the reference string is a sub-string of the value.
  - `<`      : Less than - matches when the value is less than the reference.
  - `<=`     : Less than or equal to - matches when the value is less than or equal the reference.
  - `>`      : Greater than - matches when the value is greater than reference.
  - `>=`     : Greater than or equal to - matches when the value is greater than or equal the reference.

Rule types are matched case-insensitively, so `==`, `REGEXP` and `regexp` are all valid.

The ordering operators (<, <=, >, >=) require a numeric reference value (a non-numeric
reference is a configuration error, [ErrInvalidFilter], not a silent false). They compare
numeric values directly; for strings, arrays, slices, and maps they compare the length of the
value against the reference (not lexicographic order). Anything else evaluates to false.

The string operators (regexp, ^=, =$, ~=) act only on string values; a non-string reference
is a configuration error, and a non-string value being tested is a non-match.

Every rule type can be prefixed with `!` to negate its result. `!==` matches values that are
not equal, `!<` values that are not less than the reference, and so on. Negation inverts the
whole result: a `!` rule also matches when the base operator could not apply at all (a type it
cannot compare, or an operand with no ordering). For any element whose field is actually read,
exactly one of a rule and its negation matches; they fail to partition the elements only when
the field is unreachable on an element (a nil pointer along the path, or a field absent on a
[]any element), which is a non-match for both. `!=` is the negation of `=`
(equal-fold), i.e. "not equal under case folding", not a distinct operator.

# When To Use

  - Filter criteria arrive from an API request or a configuration file.
  - Rules must be composable and evaluated against arbitrary struct fields.
*/
package filter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
)

const (
	// MaxResults is the maximum number of results that can be returned.
	MaxResults = 1<<31 - 1 // math.MaxInt32

	// DefaultMaxResults is the default number of results for Apply.
	// Can be overridden with WithMaxResults().
	DefaultMaxResults = MaxResults

	// DefaultMaxRules is the default maximum number of rules.
	//
	// The limit bounds two counts: the number of AND groups (the outer slice), and the
	// total number of rules summed across all the OR groups (the inner slices). So
	// "name AND age AND (country==EN OR country==FR)" counts as 3 groups and 4 rules.
	//
	// Can be overridden with WithMaxRules().
	DefaultMaxRules = 8

	// DefaultURLQueryFilterKey is the default URL query key used by Processor.ParseURLQuery().
	// Can be customized with WithQueryFilterKey().
	DefaultURLQueryFilterKey = "filter"

	// DefaultMaxValueLength is the default maximum byte length of a rule's string
	// value (e.g. a regexp pattern). It bounds regexp compilation and matching cost
	// for untrusted filters. Can be overridden with WithMaxValueLength().
	DefaultMaxValueLength = 4096

	// DefaultMaxFilterBytes is the default maximum byte length of the raw filter payload
	// accepted by Processor.ParseJSON() (and hence Processor.ParseURLQuery()) before it is
	// JSON-decoded. It bounds parse-time cost for untrusted input. Can be overridden with
	// WithMaxFilterBytes().
	DefaultMaxFilterBytes = 1 << 16 // 64 KiB

	// DefaultMaxFieldDepth is the default maximum number of dot-separated segments in a
	// rule field selector (e.g. "a.b.c" has depth 3). It bounds field-resolution cost per
	// selector. Can be overridden with WithMaxFieldDepth().
	DefaultMaxFieldDepth = 32
)

// ErrInvalidFilter wraps every error attributable to a malformed or disallowed
// client filter: invalid JSON, an oversized payload or value, too many rules, an
// unsupported or mistyped rule, an uncompilable regexp, or a field selector that
// cannot be resolved. Callers handling untrusted input can test
// errors.Is(err, ErrInvalidFilter) to reject the request generically (for example
// an HTTP 400) and log the detail server-side, instead of returning the underlying
// message, which may echo client input or internal type names.
//
// Errors caused by misuse of the Apply/ApplySubset arguments by the calling code
// (a non-slice-pointer target, or an out-of-range length) are NOT wrapped with it.
var ErrInvalidFilter = errors.New("invalid filter")

// Processor provides the filtering logic and methods. Construct one with [New]; the zero
// Processor has all limits set to zero and rejects every input.
type Processor struct {
	fields            fieldGetter
	maxRules          uint
	maxResults        uint
	maxValueLen       uint
	maxFilterBytes    uint
	urlQueryFilterKey string
}

// New constructs a Processor for declarative rule-based filtering with custom options.
// The first rule level uses AND semantics; the second uses OR ("[a,[b,c],d]" → "a AND (b OR c) AND d").
// Customizable via field-tag lookup, query-parameter keys, count limits, and the
// untrusted-input safety limits (value length, payload size, field-path depth).
func New(opts ...Option) (*Processor, error) {
	p := &Processor{
		fields:            fieldGetter{maxDepth: DefaultMaxFieldDepth},
		maxRules:          DefaultMaxRules,
		maxResults:        DefaultMaxResults,
		maxValueLen:       DefaultMaxValueLength,
		maxFilterBytes:    DefaultMaxFilterBytes,
		urlQueryFilterKey: DefaultURLQueryFilterKey,
	}

	for _, opt := range opts {
		err := opt(p)
		if err != nil {
			return nil, err
		}
	}

	return p, nil
}

// ParseURLQuery unmarshals the JSON filter rule set from a URL query parameter.
// Defaults to the "filter" key; override with [WithQueryFilterKey].
// Returns nil rules when the key is missing or empty; otherwise it delegates to
// [Processor.ParseJSON] and applies the same limits ([WithMaxFilterBytes], [WithMaxRules]).
// Filter-attributable errors are wrapped with [ErrInvalidFilter].
func (p *Processor) ParseURLQuery(q url.Values) ([][]Rule, error) {
	value := q.Get(p.urlQueryFilterKey)
	if value == "" {
		return nil, nil
	}

	return p.ParseJSON(value)
}

// ParseJSON decodes a JSON filter rule set with the Processor's safety limits applied:
// it rejects payloads larger than the configured maximum ([WithMaxFilterBytes]) before
// decoding, and rejects rule sets exceeding [WithMaxRules]. The payload must match the shape of
// filter_schema.json (at least one non-empty AND group, and every rule object carrying the
// "field", "type", and "value" keys), otherwise it is rejected as a malformed filter. It is the
// only supported way to decode untrusted filter JSON; the unbounded decoder is internal.
// Filter-attributable errors are wrapped with [ErrInvalidFilter].
func (p *Processor) ParseJSON(s string) ([][]Rule, error) {
	// Reject oversized payloads before decoding, so a huge value cannot force a large
	// JSON allocation only to be rejected afterwards by the rule-count limit.
	if uint(len(s)) > p.maxFilterBytes {
		return nil, fmt.Errorf("%w: filter payload too large: got %d bytes max is %d", ErrInvalidFilter, len(s), p.maxFilterBytes)
	}

	rules, err := parseJSON(s)
	if err != nil {
		return nil, err
	}

	// Defense-in-depth: reject oversized client-provided rule sets at parse time.
	err = p.checkRulesCount(rules)
	if err != nil {
		return nil, err
	}

	return rules, nil
}

// Apply filters a slice by removing non-matching elements in place.
// Convenience method equivalent to ApplySubset with offset 0 and length the configured
// maximum ([WithMaxResults]). Returns filtered-slice length, total-match count, and any error.
func (p *Processor) Apply(rules [][]Rule, slicePtr any) (uint, uint, error) {
	return p.ApplySubset(rules, slicePtr, 0, p.maxResults)
}

// ApplySubset filters a slice by removing non-matching elements in place, with pagination support.
// The slicePtr must be a pointer to a slice; offset and length control pagination within matches.
// Returns filtered-slice length within window, total-match count overall, and any error.
func (p *Processor) ApplySubset(rules [][]Rule, slicePtr any, offset, length uint) (uint, uint, error) {
	if length < 1 {
		return 0, 0, errors.New("length must be at least 1")
	}

	if length > p.maxResults {
		return 0, 0, errors.New("length must not exceed maxResults")
	}

	err := p.checkRulesCount(rules)
	if err != nil {
		return 0, 0, err
	}

	// %T is nil-safe: an untyped-nil slicePtr must return this error, not panic. Formatting
	// reflect.ValueOf(nil).Type() would panic on the zero Value.
	vSlicePtr := reflect.ValueOf(slicePtr)
	if vSlicePtr.Kind() != reflect.Pointer {
		return 0, 0, fmt.Errorf("slicePtr should be a slice pointer but is %T", slicePtr)
	}

	vSlice := vSlicePtr.Elem()
	if vSlice.Kind() != reflect.Slice {
		return 0, 0, fmt.Errorf("slicePtr should be a slice pointer but is %T", slicePtr)
	}

	// Compile all evaluators and resolve field paths up front, before touching the
	// input slice. Paths for concrete element types are resolved once here rather than
	// per element; interface element types (e.g. []any) are resolved per element since
	// their concrete type varies. Compiling first also makes concurrent Apply on a
	// shared [][]Rule race-free (no per-rule lazy mutation) and ensures a misconfigured
	// rule fails before any in-place mutation truncates the slice.
	compiled, err := p.compileRules(rules, vSlice.Type().Elem())
	if err != nil {
		return 0, 0, err
	}

	matcher := func(elem reflect.Value) (bool, error) {
		return p.evaluateRules(compiled, elem)
	}

	n, m, err := p.filterSliceValue(vSlice, offset, int(length), matcher)

	return uint(n), m, err
}

func (p *Processor) checkRulesCount(rules [][]Rule) error {
	// Bound the number of AND groups, not only the total rule count, so that a large set of
	// single-rule groups cannot slip past a per-group view of the limit.
	if uint(len(rules)) > p.maxRules {
		return fmt.Errorf("%w: too many rule groups: got %d max is %d", ErrInvalidFilter, len(rules), p.maxRules)
	}

	var count int

	for i := range rules {
		// An empty OR group is an empty disjunction, which is always false, so it would
		// silently drop every element (an AND of a never-true term). That is a malformed
		// filter, not a valid "match nothing" request, and the JSON schema forbids it too.
		if len(rules[i]) == 0 {
			return fmt.Errorf("%w: empty rule group at index %d", ErrInvalidFilter, i)
		}

		count += len(rules[i])
	}

	if uint(count) > p.maxRules {
		return fmt.Errorf("%w: too many rules: got %d max is %d", ErrInvalidFilter, count, p.maxRules)
	}

	return nil
}

// filterSliceValue filters a reflect.Value slice in place using a matcher function.
// Returns matched-element count within pagination window and total-match count overall.
//
// The matching pass is completed before any mutation: matched indices within the
// pagination window are collected first, and the in-place compaction is committed only
// once the full pass succeeds. A matcher error therefore leaves the input slice untouched.
func (p *Processor) filterSliceValue(slice reflect.Value, offset uint, length int, matcher func(reflect.Value) (bool, error)) (int, uint, error) {
	selected, total, err := selectMatches(slice, offset, length, matcher)
	if err != nil {
		return 0, 0, err
	}

	// Commit phase: only reached after a fully successful pass, so a mid-iteration
	// error above can never truncate or clobber the caller's slice.
	for n, i := range selected {
		if n != i {
			slice.Index(n).Set(slice.Index(i))
		}
	}

	n := len(selected)

	// shorten the slice to the actual number of elements
	slice.SetLen(n)

	return n, total, nil
}

// selectMatches runs the matcher over every element and returns the original indices that
// match within the pagination window, plus the overall match count. It mutates nothing.
func selectMatches(slice reflect.Value, offset uint, length int, matcher func(reflect.Value) (bool, error)) ([]int, uint, error) {
	skip := offset

	var (
		selected []int
		total    uint
	)

	for i := range slice.Len() {
		// The element is passed as a reflect.Value rather than boxed via Interface():
		// only the specific field an evaluator reads is ever materialized, avoiding a
		// per-element allocation and copy of the whole element.
		match, err := matcher(slice.Index(i))
		if err != nil {
			return nil, 0, err
		}

		if !match {
			continue
		}

		total++

		if skip > 0 {
			skip--
			continue
		}

		if len(selected) < length {
			selected = append(selected, i)
		}
	}

	return selected, total, nil
}

// compiledRule pairs a resolved field selector with its pre-built evaluator.
//
// For concrete element types the field path is resolved once, at compile time, into path;
// any resolution failure is reported then, before filtering. For interface element types the
// concrete type is only known per element, so dynamic is set and the path is resolved during
// evaluation using field.
type compiledRule struct {
	eval      evaluator   // pre-built type-specific evaluator
	field     string      // original dot-path selector (for dynamic resolution and errors)
	path      reflectPath // field-index path for concrete element types
	wholeElem bool        // field == "" → evaluate the element itself
	dynamic   bool        // interface element type → resolve path per element
}

// compileRules pre-builds the evaluator and resolves the field path for every rule,
// surfacing configuration errors (invalid type, bad regexp, non-numeric ordering
// reference, ...) before any filtering. The result is independent of the input
// [][]Rule, so a shared rule set can be filtered concurrently without mutating
// per-rule state.
func (p *Processor) compileRules(rules [][]Rule, elemType reflect.Type) ([][]compiledRule, error) {
	compiled := make([][]compiledRule, len(rules))

	for i := range rules {
		compiled[i] = make([]compiledRule, len(rules[i]))

		for j := range rules[i] {
			cr, err := p.compileRule(rules[i][j], elemType)
			if err != nil {
				return nil, err
			}

			compiled[i][j] = cr
		}
	}

	return compiled, nil
}

// compileRule builds the evaluator and resolves the field path for a single rule
// against the slice element type. A selector that does not exist on a concrete element
// type is a deterministic client error and is rejected here; a field that is only
// unreachable per element (a nil pointer along the path, or an interface element whose
// concrete type lacks it) remains a non-match, decided during evaluation.
func (p *Processor) compileRule(rule Rule, elemType reflect.Type) (compiledRule, error) {
	// Reject oversized string values (e.g. a huge regexp pattern) before building the
	// evaluator, so a pathological pattern is never handed to regexp.Compile. Checked by
	// reflect kind, not a plain string assertion, so a named string type is bounded too.
	if rv := reflect.ValueOf(rule.Value); rv.Kind() == reflect.String && uint(rv.Len()) > p.maxValueLen {
		return compiledRule{}, fmt.Errorf("%w: rule value too large: got %d bytes max is %d", ErrInvalidFilter, rv.Len(), p.maxValueLen)
	}

	eval, err := rule.getEvaluator()
	if err != nil {
		return compiledRule{}, err
	}

	cr := compiledRule{eval: eval, field: rule.Field}

	switch {
	case rule.Field == "":
		cr.wholeElem = true
	case elemType.Kind() == reflect.Interface:
		cr.dynamic = true
	default:
		path, rerr := p.fields.resolvePath(elemType, rule.Field)

		switch {
		case errors.Is(rerr, errFieldNotFound):
			// The element type is known here, so a selector it does not have can never
			// match anything: it is a malformed client filter, not a data condition.
			// Reporting it (rather than silently filtering everything out) lets a handler
			// answer an unknown selector with a 400 instead of an empty list.
			return compiledRule{}, fmt.Errorf("%w: unknown field selector %q", ErrInvalidFilter, rule.Field)
		case rerr != nil:
			// Any other resolution failure (descent into a non-struct field, a selector
			// deeper than the configured limit, an unexported target) is likewise
			// deterministic for a concrete element type, so it is reported now rather than
			// deferred to evaluation, where it would surface only for a non-empty slice.
			return compiledRule{}, rerr
		default:
			cr.path = path
		}
	}

	return cr, nil
}

// evaluateRules applies AND-OR rule composition to determine element match: outer-AND, inner-OR.
//
//nolint:gocognit
func (p *Processor) evaluateRules(rules [][]compiledRule, elem reflect.Value) (bool, error) {
	for i := range rules {
		orResult := false

		for j := range rules[i] {
			match, err := p.evaluateRule(rules[i][j], elem)
			if err != nil {
				return false, err
			}

			if match {
				orResult = true
				break
			}
		}

		if !orResult {
			return false, nil
		}
	}

	return true, nil
}

// evaluateRule resolves a rule's target value within elem and applies its evaluator.
// Missing fields and nil pointers along the path are treated as non-matches without error.
func (p *Processor) evaluateRule(rule compiledRule, elem reflect.Value) (bool, error) {
	value, ok, err := p.ruleValue(rule, elem)
	if err != nil {
		return false, err
	}

	if !ok {
		return false, nil // missing field or nil pointer in path: non-match
	}

	return rule.eval.Evaluate(value), nil
}

// ruleValue resolves the reflect.Value a rule should evaluate for the given element.
// ok is false (with a nil error) when the field is made unreachable by a nil pointer along
// the path, or is absent from the concrete type of an interface-typed element: such
// elements are a non-match, not an error. A selector that is absent from (or unreadable on) a
// concrete element type never reaches here; it is rejected at compile time.
func (p *Processor) ruleValue(rule compiledRule, elem reflect.Value) (reflect.Value, bool, error) {
	// Unwrap an interface-typed element (e.g. []any) to its concrete dynamic value.
	value := elem
	if value.Kind() == reflect.Interface {
		value = value.Elem()
	}

	if rule.wholeElem {
		return value, true, nil
	}

	// Interface element types resolve their concrete field path per element; concrete
	// element types use the path resolved once at compile time (the common hot path).
	if rule.dynamic {
		return p.dynamicFieldValue(rule, value)
	}

	v, ok := walkFieldPath(value, rule.path)

	return v, ok, nil
}

// dynamicFieldValue resolves and reads a rule's field for an element of an
// interface-typed slice, whose concrete type is only known per element. ok is
// false without error when the field is absent (a non-match).
func (p *Processor) dynamicFieldValue(rule compiledRule, value reflect.Value) (reflect.Value, bool, error) {
	if !value.IsValid() {
		// A nil interface element has no fields: treat the selector as absent (a
		// non-match), consistently with a nil pointer along the path. This keeps a
		// single nil datum from failing the whole filter and avoids misattributing a
		// data condition to the client filter.
		return reflect.Value{}, false, nil
	}

	path, err := p.fields.resolvePath(value.Type(), rule.field)
	if errors.Is(err, errFieldNotFound) {
		return reflect.Value{}, false, nil
	}

	if err != nil {
		return reflect.Value{}, false, err
	}

	v, ok := walkFieldPath(value, path)

	return v, ok, nil
}

// walkFieldPath descends the field-index path from value, dereferencing pointers between
// segments. It returns ok=false (non-match) when a nil pointer makes the field unreachable.
// The resolved leaf is unwrapped through any pointers and interfaces to the concrete value an
// evaluator should read; a nil leaf becomes an invalid Value, which evaluators treat as a nil
// operand. Unexported and otherwise unreadable selectors cannot reach here; resolvePath
// rejects them when the path is compiled.
func walkFieldPath(value reflect.Value, path reflectPath) (reflect.Value, bool) {
	for _, fieldIndex := range path {
		value = reflect.Indirect(value)
		if !value.IsValid() {
			// nil pointer along the path: treat the field as missing (non-match).
			return reflect.Value{}, false
		}

		value = value.Field(fieldIndex)
	}

	return unwrapLeaf(value), true
}

// maxLeafIndirections caps how many pointer/interface hops unwrapLeaf follows. A real leaf needs
// only a few; the cap prevents an unbounded spin on a pathological cyclic reference (a pointer
// or interface chain that never reaches a concrete value, e.g. an any field set to point at
// itself), which would otherwise hang Apply. An ordinary linked list or tree does not approach
// it, since the chain stops at the first struct or scalar.
const maxLeafIndirections = 100

// unwrapLeaf follows pointers and interfaces at a resolved leaf so that an evaluator reads the
// concrete value rather than a pointer or an interface header (the pointer-leaf case a
// between-segments reflect.Indirect never reaches). A nil pointer or interface (or a cyclic chain
// that exceeds maxLeafIndirections) yields an invalid Value, matching how a nil operand is
// treated everywhere else.
func unwrapLeaf(value reflect.Value) reflect.Value {
	for range maxLeafIndirections {
		if value.Kind() != reflect.Pointer && value.Kind() != reflect.Interface {
			return value
		}

		if value.IsNil() {
			return reflect.Value{}
		}

		value = value.Elem()
	}

	// A pointer/interface chain this deep is a cyclic reference: treat it as a nil operand
	// rather than spin forever.
	return reflect.Value{}
}

// parseJSON unmarshals rule-set JSON into its composite AND-OR structure. It applies no size
// limits; use [Processor.ParseJSON] for untrusted input.
//
// It enforces the shape of filter_schema.json for untrusted input: at least one AND group, and
// every rule object carrying the field, type, and value keys (an omitted key is a client error,
// distinct from an explicit null). Numbers are decoded exactly rather than through float64 (see
// normalizeRuleValue).
func parseJSON(s string) ([][]Rule, error) {
	var raw [][]rawRule

	// json.Unmarshal (unlike a json.Decoder) rejects any data after the top-level value, which
	// keeps the parser strict for untrusted payloads without a separate check.
	err := json.Unmarshal([]byte(s), &raw)
	if err != nil {
		return nil, fmt.Errorf("%w: failed unmarshaling rules: %w", ErrInvalidFilter, err)
	}

	// The schema requires at least one AND group: an empty filter is not a valid narrowing
	// request. A caller wanting "no filter" omits it entirely (ParseURLQuery returns nil rules
	// for a missing key).
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: filter must contain at least one rule group", ErrInvalidFilter)
	}

	rules := make([][]Rule, len(raw))

	for i := range raw {
		rules[i] = make([]Rule, len(raw[i]))

		for j := range raw[i] {
			rule, rerr := raw[i][j].rule()
			if rerr != nil {
				return nil, rerr
			}

			rules[i][j] = rule
		}
	}

	return rules, nil
}

// rawRule mirrors a JSON rule object while distinguishing an omitted key from an explicit null:
// a pointer field is nil when its key is absent, and ruleValue records whether it was set. The
// three keys are all required, so an omitted one is rejected.
type rawRule struct {
	Field *string   `json:"field"`
	Type  *string   `json:"type"`
	Value ruleValue `json:"value"`
}

// ruleValue is a rule's decoded value plus a flag recording whether the "value" key was present.
// Its UnmarshalJSON is invoked only for a present key (including an explicit null), so set tells
// present-null apart from absent. Numbers are decoded exactly (as json.Number) rather than
// widened to float64.
type ruleValue struct {
	set   bool
	value any
}

// UnmarshalJSON decodes the raw value with exact numbers. It runs only when the key is present.
func (v *ruleValue) UnmarshalJSON(data []byte) error {
	v.set = true

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	// data is a single, already-validated JSON value (the outer decoder validated it before
	// calling this), so decoding it into an any cannot fail; the error is returned unwrapped
	// because it is unreachable.
	return dec.Decode(&v.value) //nolint:wrapcheck
}

// rule validates that all three keys are present and converts the raw rule into a [Rule],
// normalizing the value exactly (integers are not widened to float64) and rejecting composites.
func (r rawRule) rule() (Rule, error) {
	if r.Field == nil || r.Type == nil || !r.Value.set {
		return Rule{}, fmt.Errorf("%w: each rule must set the field, type and value keys", ErrInvalidFilter)
	}

	value, err := normalizeRuleValue(r.Value.value)
	if err != nil {
		return Rule{}, err
	}

	return Rule{Field: *r.Field, Type: *r.Type, Value: value}, nil
}

// normalizeRuleValue converts a decoded JSON rule value into the concrete Go type an
// evaluator expects. A number (decoded as a json.Number by the UseNumber decoder) becomes the
// exact Go integer or float it denotes; a string, boolean, or null passes through unchanged.
//
// Array and object values are rejected: the ordering and string operators cannot act on them,
// and equality against a composite is only ever a reflect.DeepEqual against an identically
// typed field, a corner the JSON grammar (see filter_schema.json) does not offer. Rejecting
// them here keeps the untrusted-input surface to scalars. A Go caller may still construct a
// composite-valued [Rule] directly if it needs one.
func normalizeRuleValue(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		return exactJSONNumber(x)
	case []any:
		return nil, fmt.Errorf("%w: array rule values are not supported", ErrInvalidFilter)
	case map[string]any:
		return nil, fmt.Errorf("%w: object rule values are not supported", ErrInvalidFilter)
	}

	return v, nil
}

// exactJSONNumber converts a JSON number to the narrowest Go type that holds it exactly:
// int64 when it fits, uint64 for the (math.MaxInt64, math.MaxUint64] range, float64 otherwise.
// Plain encoding/json would decode every number as a float64, silently making integers beyond
// 2^53 equal to their neighbors (see numeric.go).
//
// Exactness is preserved for integer literals only: a client that writes 9007199254740993 as
// 9.007199254740993e15 sends a JSON float and gets float64 precision, as it asked for.
func exactJSONNumber(n json.Number) (any, error) {
	i, err := strconv.ParseInt(n.String(), 10, 64)
	if err == nil {
		return i, nil
	}

	// Tried after ParseInt so that only the values above math.MaxInt64 land here, matching how
	// numeric.compareInt handles a signed/unsigned mix.
	u, err := strconv.ParseUint(n.String(), 10, 64)
	if err == nil {
		return u, nil
	}

	// ParseFloat returns ±Inf together with an error for out-of-range values (e.g. 1e400), so
	// the error must be honored: keeping the number as a string instead would turn a filter
	// that is currently rejected into a string comparison against the string fields.
	f, err := strconv.ParseFloat(n.String(), 64)
	if err != nil {
		return nil, fmt.Errorf("%w: rule value is not a representable number: %s", ErrInvalidFilter, n)
	}

	return f, nil
}
