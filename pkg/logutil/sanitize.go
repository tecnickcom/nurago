package logutil

import (
	"context"
	"log/slog"
	"reflect"
	"slices"
	"time"
)

// sanitizeAttrsInline is the number of a record's attributes sanitizeRecord rebuilds without
// allocating. Sixteen covers the widest request-log line in practice while keeping the array small
// enough to stay on the stack.
const sanitizeAttrsInline = 16

// slogSanitizeHandler is a slog.Handler that resolves every attribute value once and repairs, in every
// record and in every set of attributes baked in with WithAttrs, the two shapes the standard library's
// handlers encode incorrectly.
//
// The first is a group that produces no field. Such a group writes nothing, but the standard library
// rolls the output buffer back to before it without calling closeGroup, so the state it cleared when
// opening the group is never restored. Under the JSON handler the attribute separator stays empty and
// the next attribute is written with no comma before it, making the line invalid JSON; under the text
// handler the group's name stays in the key prefix and the next attribute is silently written under the
// wrong key ("z=1" becomes "g.z=1"). It bites wherever a rendering attribute follows an elided group in
// the same buffer: among a record's attributes, inside another group, and within a single WithAttrs
// call, which is how Config.CommonAttr is applied, so a single elided group there corrupts every line
// the process writes. Dropping the group changes nothing about the output and sidesteps the bug. The
// filter is recursive: a group that is kept is rebuilt without any non-rendering subgroup it holds.
//
// The second is an attribute holding a time.Time whose year falls outside [0,9999]. slog's JSON encoder
// writes an "!ERROR:" string for it and then writes the value anyway (appendJSONTime does not return
// after the error), so one key carries two JSON strings and the line is invalid. Such a value is
// rewritten here as the RFC 3339 string the logsrv backend writes for it, which is the only rendering
// that keeps the line parseable and keeps the two backends agreeing. It is reachable from ordinary code:
// a deadline built by adding a large duration, or a time.Unix on a corrupt field.
//
// Only an *attribute*: a record's own timestamp is not one and never passes through here. It carries the
// same defect, and is repaired by the ReplaceAttr callback Config.SlogHandler installs (see
// replaceLevelName), which is why a handler wrapped by NewSlogTraceIDHandler, where that callback is
// the caller's to supply, is not covered.
//
// Values are resolved here, and the resolved value is what is handed downstream: a LogValuer only
// yields its group once resolved, and a purely structural filter cannot see it (that mistake made this
// handler blind to the whole class). Substituting the resolved value keeps the cost at exactly one
// LogValue call per attribute per record: the handler below resolves again as it writes, and resolving
// an already-resolved value is a no-op. A LogValuer is therefore never invoked twice, and one whose
// result varies between calls cannot make this scan and that write disagree.
//
// It is installed above the trace handler rather than inside it (see Config.SlogHandler), for three
// reasons: it then runs whatever else is configured, including with TraceIDFn unset, where there is no
// trace handler to carry it; the trace handler receives records and derivations already filtered and
// resolved, so the trace ID it injects can never be the attribute that follows an elided group; and the
// per-record handler-chain replay a grouped logger performs runs below it, so it costs nothing there.
//
// The common attributes are the one set it cannot see (they are baked straight into the handler
// underneath) so Config.SlogHandler filters them itself before baking them.
//
// One rule it deliberately does not model: a ReplaceAttr callback that deletes an attribute (by
// returning the zero slog.Attr) can empty a group below this handler, re-creating the first defect.
// The filter cannot see a callback installed on a handler it merely wraps. Config.SlogHandler is
// unaffected (the only callback it installs is replaceLevelName, which never deletes) but a handler
// passed to NewSlogTraceIDHandler with a deleting ReplaceAttr is not protected (see attrRenders).
type slogSanitizeHandler struct {
	inner slog.Handler
}

// newSlogSanitizeHandler wraps h so records and baked attributes are resolved and stripped of the
// groups that render nothing (see slogSanitizeHandler).
func newSlogSanitizeHandler(h slog.Handler) *slogSanitizeHandler {
	return &slogSanitizeHandler{inner: h}
}

// Enabled reports whether a record at the given level should be handled. Attributes do not affect
// enablement, so it delegates to the wrapped handler.
func (h *slogSanitizeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle passes the record to the wrapped handler with its values resolved and its non-rendering
// groups removed.
func (h *slogSanitizeHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.inner.Handle(ctx, sanitizeRecord(record)) //nolint:wrapcheck
}

// WithAttrs returns a new handler whose wrapped handler carries the given attributes, resolved and
// with their non-rendering groups removed. Per the slog.Handler contract, an empty attribute list
// returns the receiver; so does one the filter empties, since it would bake nothing.
func (h *slogSanitizeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean, _ := sanitizeAttrs(attrs)
	if len(clean) == 0 {
		return h
	}

	return &slogSanitizeHandler{inner: h.inner.WithAttrs(clean)}
}

// WithGroup returns a new handler whose wrapped handler opens the given group. Per the slog.Handler
// contract, an empty group name returns the receiver.
func (h *slogSanitizeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &slogSanitizeHandler{inner: h.inner.WithGroup(name)}
}

// sanitizeRecord returns record with its values resolved and its non-rendering groups removed,
// rebuilding it only when something actually changes, so a record of plain attributes is left
// untouched.
//
// The record is rebuilt rather than mutated: slog.Record's contract forbids modifying a record whose
// copies have been handed out (AddAttrs on a shared record makes the standard library write a "!BUG"
// field into the line) and a handler cannot know whether an upstream one kept a copy.
func sanitizeRecord(record slog.Record) slog.Record {
	if !recordMayNeedSanitize(record) {
		return record
	}

	var stack [sanitizeAttrsInline]Attr

	attrs := stack[:0]
	changed := false

	record.Attrs(func(a slog.Attr) bool {
		clean, keep, dirty := sanitizeAttr(a)

		// An attribute that is dropped changes the record even when it was not itself rewritten: an
		// empty *slog.Source and a nil-pointer error are both dropped without being dirty, so !keep
		// counts too. Without it the record would keep an attribute the WithAttrs path drops, and the
		// two would hand different attribute sets downstream for the same input.
		//
		// (The zero Attr is the exception: it is dropped without being dirty as well, but
		// recordMayNeedSanitize never wakes for one, so a record carrying only that keeps it. slog
		// elides it harmlessly (before it opens any group) so the output is the same either way.)
		changed = changed || dirty || !keep

		if keep {
			attrs = append(attrs, clean)
		}

		return true
	})

	if !changed {
		return record
	}

	out := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	out.AddAttrs(attrs...)

	return out
}

// recordMayNeedSanitize reports whether any of record's attributes could need the filter: a group can
// hold something that renders nothing and a LogValuer can turn into one; a time.Time can carry a year
// slog's JSON encoder cannot write; and an Any can hold a value this package writes no field for where
// slog would write one, an empty *slog.Source, or a nil-pointer error (see valueRenders). Every other
// kind always writes itself as it stands, so a record of plain attributes (the hot path) resolves
// nothing, allocates nothing, and is passed on as it came.
func recordMayNeedSanitize(record slog.Record) bool {
	found := false

	record.Attrs(func(a slog.Attr) bool {
		switch a.Value.Kind() { //nolint:exhaustive // every other kind writes itself as it stands.
		case slog.KindGroup, slog.KindLogValuer:
			found = true

			return false
		case slog.KindTime:
			// Only an out-of-range year needs rewriting, so an ordinary timestamp attribute (much the
			// commoner case) stays on the fast path.
			if !encodableJSONTime(a.Value.Time()) {
				found = true

				return false
			}
		case slog.KindAny:
			// A type check only: an ordinary Any (including an ordinary, non-nil error) renders and
			// stays on the fast path.
			if !valueRenders(a.Value) {
				found = true

				return false
			}
		}

		return true
	})

	return found
}

// sanitizeAttrs returns attrs resolved and with every non-rendering group removed, at any depth, and
// reports whether that differs from the input. The input slice is returned as it came when there is
// nothing to change, so the common case allocates nothing.
//
//nolint:gocognit
func sanitizeAttrs(attrs []Attr) ([]Attr, bool) {
	var out []Attr // nil until the first attribute that needs changing (copy-on-write)

	for i, a := range attrs {
		clean, keep, dirty := sanitizeAttr(a)

		if out == nil {
			if keep && !dirty {
				continue
			}

			out = make([]Attr, i, len(attrs))
			copy(out, attrs[:i])
		}

		if keep {
			out = append(out, clean)
		}
	}

	if out == nil {
		return attrs, false
	}

	return out, true
}

// sanitizeAttr resolves a's value, rewrites a time slog cannot encode, and removes every non-rendering
// group from it, at any depth. It reports whether the result should be kept (it writes a field) and
// whether it differs from a.
func sanitizeAttr(a Attr) (Attr, bool, bool) {
	value := a.Value
	dirty := false

	if value.Kind() == slog.KindLogValuer {
		value = value.Resolve()
		dirty = true
	}

	// A year outside [0,9999] makes slog's JSON encoder write an "!ERROR:" string *and* the value,
	// putting two JSON strings under one key (see slogSanitizeHandler). Write it as the string the
	// logsrv backend writes, which keeps the line parseable and the two backends identical.
	if value.Kind() == slog.KindTime {
		if t := value.Time(); !encodableJSONTime(t) {
			return Attr{Key: a.Key, Value: slog.StringValue(t.Format(time.RFC3339Nano))}, true, true
		}
	}

	if value.Kind() != slog.KindGroup {
		out := Attr{Key: a.Key, Value: value}

		return out, attrRenders(out), dirty
	}

	members, membersDirty := sanitizeAttrs(value.Group())
	if len(members) == 0 {
		// Every member elides, so the group writes no field. Dropping it is what sidesteps the
		// standard library's separator bug; a group that was empty to begin with writes nothing
		// either, and slog.Record drops it from a record in any case.
		return a, false, true
	}

	if !dirty && !membersDirty {
		return a, true, false
	}

	return Attr{Key: a.Key, Value: slog.GroupValue(members...)}, true, true
}

// attrRenders reports whether a writes a field in the output, resolving its value. It mirrors the
// *elision* rules of the standard library's appendAttr (see log/slog's handler.go): the zero Attr, an
// empty *slog.Source, and a group that yields no field all write nothing.
//
// Two deliberate deviations, both so the two backends render the same slog.Attr the same way:
//
//   - A group that resolves to zero members counts as writing nothing here, where slog writes it as a
//     bare "{}". Eliding is the rule both backends already document, and it is what keeps
//     WithGroup(TraceIDKey) from replacing the trace ID with an empty object.
//   - A nil-pointer error counts as writing nothing, where slog renders it as the string "<nil>". The
//     logsrv backend drops it too, so the two agree. Without this a typed nil (the commonest error bug
//     in Go) logged under TraceIDKey would be read as a caller-supplied trace ID, suppressing the
//     injected one and shipping the record correlated by the string "<nil>".
//
// Both are disclosed in the package documentation.
//
// The nil-error rule is unconditional here, and it has to be: this package cannot see zerolog, so it
// cannot make the answer depend on zerolog's interfaces or on its process-global ErrorMarshalFunc. The
// logsrv backend therefore drops a typed nil *before* consulting that hook (see its addErrEvent) rather
// than after, so both backends answer this question the same way whatever the hook is. The one shape
// that still splits them is a hook mapping a *non-nil* error to nil: logsrv then writes no field, and
// this filter, blind to the hook, keeps one. That divergence is disclosed and cannot be closed here.
//
// The one rule it does not model is a ReplaceAttr callback that deletes an attribute, which slog applies
// below this handler and which can empty a group the filter has already passed (see slogSanitizeHandler).
func attrRenders(a Attr) bool {
	value := a.Value.Resolve()

	if (Attr{Key: a.Key, Value: value}).Equal(slog.Attr{}) {
		return false // slog drops the zero Attr
	}

	return valueRenders(value)
}

// valueRenders reports whether a resolved value writes a field in the output (see attrRenders for the
// rules and the two deviations).
//
// A group yields nothing when every member does: slog ignores a group with no members, drops the zero
// Attr, and elides a subgroup that itself renders nothing, so an arbitrarily nested empty group writes
// nothing at all, however many levels it has.
//
// An empty *slog.Source (a nil one, or a zero-valued one) writes nothing either: slog's handlers give
// that type a special case and elide it. A nil-pointer error writes nothing because the sibling zerolog
// backend omits it. Both matter beyond the field itself: such a value logged under TraceIDKey would
// otherwise be read as a caller-supplied trace ID, suppressing the injected one while writing nothing
// usable, leaving the record with no correlation ID.
func valueRenders(value slog.Value) bool {
	switch value.Kind() { //nolint:exhaustive // every other kind writes a field.
	case slog.KindGroup:
		return slices.ContainsFunc(value.Group(), attrRenders)
	case slog.KindAny:
		switch v := value.Any().(type) {
		case *slog.Source:
			return !emptySource(v)
		case error:
			return !nilPointerError(v)
		}
	}

	return true
}

// emptySource reports whether src is an empty caller location (a nil *slog.Source, or a zero-valued
// one) which slog's own handlers give a special case and elide.
func emptySource(src *slog.Source) bool {
	return src == nil || *src == slog.Source{}
}

// nilPointerError reports whether err is a typed nil: a non-nil interface holding a nil pointer, such
// as a nil *MyErr. (A nil interface cannot reach here: it does not match the error case of a type
// switch, and slog.Any(key, nil) is rendered as a null field, not as an error.)
//
// Only a pointer counts, mirroring zerolog's isNilValue (and so the logsrv backend's isNilError)
// exactly: a nil value of a slice, map, func or channel kind, an aggregate error such as a nil
// validator.ValidationErrors, is not nil for this purpose, because zerolog calls Error() on it and
// writes the field.
func nilPointerError(err error) bool {
	v := reflect.ValueOf(err)

	return v.Kind() == reflect.Pointer && v.IsNil()
}

// encodableJSONTime reports whether slog's JSON encoder can write t. Its appendJSONTime rejects a year
// outside [0,9999] (and then writes the value anyway, after the error string) so such a time must be
// rewritten before it reaches the handler (see slogSanitizeHandler).
func encodableJSONTime(t time.Time) bool {
	y := t.Year()

	return y >= 0 && y < 10000
}

// attrsRender reports whether any of attrs writes a field in the output.
func attrsRender(attrs []Attr) bool {
	return slices.ContainsFunc(attrs, attrRenders)
}

// recordRenders reports whether record carries any attribute that writes a field. It is consulted only
// for a logger whose root group is named TraceIDKey, to decide whether that group will render (and so
// supply the root trace ID) for this particular record.
func recordRenders(record slog.Record) bool {
	rendered := false

	record.Attrs(func(a slog.Attr) bool {
		if attrRenders(a) {
			rendered = true

			return false
		}

		return true
	})

	return rendered
}
