package logsrv

import (
	"fmt"
	"log/slog"
	"reflect"
	"runtime"

	"github.com/rs/zerolog"
	"github.com/tecnickcom/nurago/pkg/logutil"
)

// timeLayoutBare formats the record timestamp as RFC 3339 with nanosecond precision. The field is
// written explicitly instead of via zerolog's Event.Time so it keeps sub-second resolution:
// Event.Time formats with the process-global zerolog.TimeFieldFormat, whose default (RFC 3339)
// carries no fractional-seconds field and would truncate every timestamp to a whole second. The
// console writer parses the field back with this same layout (see consoleTimestamp).
const timeLayoutBare = "2006-01-02T15:04:05.999999999Z07:00"

// timeLayout is timeLayoutBare pre-quoted, so the formatted value can be written as raw JSON.
const timeLayout = `"` + timeLayoutBare + `"`

// timeBufSize is the stack buffer used to format the record timestamp. Any year in 1..9999 renders
// in at most 37 bytes; time.Time's extremes reach 46 (a negative 12-digit year with a numeric zone
// offset), which this covers so even those stay allocation-free. A longer value would still be
// correct (AppendFormat grows the slice), merely at the cost of one allocation.
const timeBufSize = 48

// addRootAttrEvent writes a single root-level slog.Attr onto the record's Event and reports both
// whether it produced a field and whether that field was the reserved trace ID key, so writeRoot can
// suppress the injected trace ID without a second scan of the attributes.
//
// An inlined (empty-key) group is flattened onto the root by addGroupEvent, so its members are
// root-level attributes too and are walked here rather than handed off. The value is still resolved
// exactly once: a LogValuer that yields an inlined group carrying a trace_id is therefore detected
// without ever running LogValue twice. Only a field that is actually written counts as the trace ID:
// an elided one (a typed-nil error logged under trace_id, say) does not suppress the injection.
func addRootAttrEvent(e *zerolog.Event, a slog.Attr) (bool, bool) {
	v, empty := resolveAttr(a)
	if empty {
		return false, false
	}

	if a.Key == "" && v.Kind() == slog.KindGroup {
		wrote, trace := false, false

		for _, ga := range v.Group() {
			memberWrote, memberTrace := addRootAttrEvent(e, ga)
			wrote = wrote || memberWrote
			trace = trace || memberTrace
		}

		return wrote, trace
	}

	wrote := addValueEvent(e, a.Key, v)

	return wrote, wrote && a.Key == logutil.TraceIDKey
}

// addAttrEvent writes a single slog.Attr onto a zerolog Event (which may be the record's root
// event or a group Dict) and reports whether it produced a field. The attribute value is
// resolved exactly once. The zero Attr is elided, and a group that yields no fields is elided
// (its enclosing key is not emitted).
func addAttrEvent(e *zerolog.Event, a slog.Attr) bool {
	v, empty := resolveAttr(a)
	if empty {
		return false
	}

	return addValueEvent(e, a.Key, v)
}

// resolveAttr resolves a's value and reports whether it writes no field at all, mirroring the two
// checks slog's appendAttr makes before it dispatches on the value's kind.
//
// The value is resolved *before* the emptiness test, as appendAttr does, not after: a LogValuer is never
// equal to the zero Value (its kind differs), so testing first lets one that resolves to the zero Value
// slip through and be written as a null field under an empty key and, under the reserved key, replace
// the trace ID with an object holding it.
//
// A *slog.Source is then given slog's special case: an empty one (nil or zero-valued) writes no field,
// and a populated one is replaced by its group form, so it reaches the group path and inlines onto the
// enclosing level when its key is empty (as it does under the standard library's handlers) instead of
// nesting under "". Under a named key the group form renders the same object the value would have
// marshaled to, so only the empty-key case changes.
// Both shapes it has to recognize (the zero Attr and a *slog.Source) resolve to KindAny, so every
// other kind is decided by the kind check alone and the rest is kept out of line, off the path taken by
// an ordinary attribute.
func resolveAttr(a slog.Attr) (slog.Value, bool) {
	v := a.Value.Resolve()

	if v.Kind() == slog.KindAny {
		return resolveAnyAttr(a.Key, v)
	}

	return v, false
}

// resolveAnyAttr applies the two checks to a resolved KindAny value: the zero Attr writes no field (it
// necessarily has an empty key, so a named attribute skips the comparison), and a *slog.Source is either
// elided, when empty, or replaced by its group form. See resolveAttr.
func resolveAnyAttr(key string, v slog.Value) (slog.Value, bool) {
	if key == "" && (slog.Attr{Value: v}).Equal(slog.Attr{}) {
		return v, true
	}

	if src, ok := v.Any().(*slog.Source); ok {
		if isEmptySource(src) {
			return v, true
		}

		return slog.GroupValue(sourceAttrs(src)...), false
	}

	return v, false
}

// addValueEvent writes an already-resolved value under key onto e and reports whether it produced a
// field: a group that yields nothing, and a nil or typed-nil error, write no field.
//
// Time and duration values are written explicitly rather than with zerolog's Event.Time/Event.Dur,
// which format with the process-global TimeFieldFormat and DurationFieldUnit: those default to
// whole-second RFC 3339 and milliseconds, so the same slog.Attr would render with a different unit
// (and a truncated timestamp) than under the standard library's handler, which the sibling logutil
// backend uses. A nanosecond count and a nanosecond-precision timestamp match slog's own encoding
// and keep the two backends interchangeable.
func addValueEvent(e *zerolog.Event, key string, v slog.Value) bool {
	switch v.Kind() {
	case slog.KindGroup:
		return addGroupEvent(e, key, v.Group())
	case slog.KindAny, slog.KindLogValuer:
		return addAnyEvent(e, key, v)
	case slog.KindString:
		e.Str(key, v.String())
	case slog.KindInt64:
		e.Int64(key, v.Int64())
	case slog.KindUint64:
		e.Uint64(key, v.Uint64())
	case slog.KindFloat64:
		e.Float64(key, v.Float64())
	case slog.KindBool:
		e.Bool(key, v.Bool())
	case slog.KindDuration:
		e.Int64(key, int64(v.Duration()))
	case slog.KindTime:
		var buf [timeBufSize]byte

		e.RawJSON(key, v.Time().AppendFormat(buf[:0], timeLayout))
	}

	return true
}

// addGroupEvent writes a group's attributes onto e and reports whether it produced any field:
// inlined at the current level when the key is empty, otherwise nested under a sub-dictionary
// keyed by name. An empty group emits nothing (no bare "{}").
//
//nolint:gocognit
func addGroupEvent(e *zerolog.Event, key string, attrs []slog.Attr) bool {
	if key == "" {
		wrote := false

		for _, ga := range attrs {
			if addAttrEvent(e, ga) {
				wrote = true
			}
		}

		return wrote
	}

	d := zerolog.Dict()
	wrote := false

	for _, ga := range attrs {
		if addAttrEvent(d, ga) {
			wrote = true
		}
	}

	if !wrote {
		d.Send() // recycle the unused pooled event (see buildGroupDict)

		return false
	}

	e.Dict(key, d)

	return true
}

// addAnyEvent writes an arbitrary value onto e and reports whether it produced a field: an error
// through addErrEvent (zerolog's native error form, its message string), anything implementing
// zerolog.LogObjectMarshaler as a JSON object, and any other value via reflection. The choice is made
// on the value's type, not on the key, so an error renders as its message under any key. An untyped nil
// is not an error value and is emitted as a null field.
//
// A *slog.Source never reaches here: resolveAttr elides an empty one and substitutes the group form of
// a populated one, as slog's handlers do.
//
// An error that also implements json.Marshaler still renders as its message here, whereas the
// standard library's handlers check json.Marshaler first and marshal the object: the same slog.Attr
// therefore reaches the wire as a string under this backend and as an object under logutil's. That is
// the price of zerolog's error form being the package's documented rendering for any error value.
//
// Rendering a value runs caller code (Error, MarshalJSON, MarshalText, MarshalZerologObject), which
// may panic. slog recovers such a panic and renders the "!PANIC" sentinel rather than letting a log
// call take the process down (the failure path is precisely where logging is called); zerolog does
// not, so it is recovered here, and the sentinel is reported as a written field so an enclosing group
// survives. A LogObjectMarshaler is routed through addObjectEvent, because a panic inside one cannot
// be recovered from once zerolog has begun writing it onto e.
//
//nolint:nonamedreturns // the deferred recover sets the result.
func addAnyEvent(e *zerolog.Event, key string, v slog.Value) (wrote bool) {
	defer func() {
		if r := recover(); r != nil {
			e.Str(key, panicSentinel(r))

			wrote = true
		}
	}()

	x := v.Any()

	if err, isErr := x.(error); isErr {
		return addErrEvent(e, key, err)
	}

	if obj, isObj := x.(zerolog.LogObjectMarshaler); isObj {
		// A typed nil would panic on its own nil receiver unless the marshaler guards against it, and
		// the "!PANIC" sentinel is a poor rendering of what is simply a nil value: slog writes null, so
		// write null too. An *error* that is a typed nil is not routed here: addErrEvent drops it
		// before the hook, whether or not it could render itself as an object.
		if isNilPointer(x) {
			e.Interface(key, nil)

			return true
		}

		return addObjectEvent(e, key, obj)
	}

	e.Interface(key, x)

	return true
}

// sourceAttrs returns the attributes slog's handlers render a populated *slog.Source as (its unexported
// group form, see slog's Source.group), so this backend writes the same field names and, under an
// empty key, inlines them the same way.
//
// A zero component is omitted, exactly as slog omits it: a partially populated *slog.Source (a line
// number with no file, say) must not gain empty "function"/"file" fields here that the sibling backend
// does not write.
func sourceAttrs(src *slog.Source) []slog.Attr {
	attrs := make([]slog.Attr, 0, 3) //nolint:mnd // the three components of a slog.Source.

	if src.Function != "" {
		attrs = append(attrs, slog.String("function", src.Function))
	}

	if src.File != "" {
		attrs = append(attrs, slog.String("file", src.File))
	}

	if src.Line != 0 {
		attrs = append(attrs, slog.Int("line", src.Line))
	}

	return attrs
}

// addErrEvent writes an error onto e and reports whether it produced a field, mirroring zerolog's
// Event.AnErr (event.go) rather than delegating to it, for three reasons.
//
// It calls the process-global zerolog.ErrorMarshalFunc exactly once. Asking AnErr what it would do and
// then letting it do it invokes that hook twice per error: it doubles the cost of a hook that captures
// a stack or emits a metric (the usual reasons to replace it), and a stateful one would render the
// second result rather than the first.
//
// It routes a LogObjectMarshaler (which is what the hook returns for an error zerolog renders as an
// object) through addObjectEvent, whose detached dictionary survives a panicking marshaler.
//
// And it reports what was actually written. The nil case writes no field, so reporting one would leave
// an enclosing group rendered as a bare "{}".
//
// A typed-nil error writes no field, and the test for it PRECEDES the hook, unlike zerolog's AnErr,
// which tests for nil only inside its error arm, so a typed nil that can render itself as an object (a
// LogObjectMarshaler guarding its nil receiver), or that the hook renders itself, is written. This
// backend drops all of them, because the sibling logutil backend must: its filter decides the same
// question (logutil.valueRenders) and cannot see either zerolog's interfaces or its process-global
// hook, so a rule conditional on those two could not be mirrored there. The backends would then ship
// different field sets (and different trace IDs) for one slog.Attr, which is the failure the shared
// rule exists to prevent. A nil error is no error; neither backend writes one.
//
// The hook is therefore not invoked for a typed nil (there is nothing to render), which is the one
// respect in which "invoked exactly once per error attribute" is really "at most once".
func addErrEvent(e *zerolog.Event, key string, err error) bool {
	if isNilError(err) {
		return false
	}

	switch m := zerolog.ErrorMarshalFunc(err).(type) {
	case nil:
		return false
	case zerolog.LogObjectMarshaler:
		return addObjectEvent(e, key, m)
	case error:
		if isNilError(m) {
			return false
		}

		e.Str(key, m.Error())
	case string:
		e.Str(key, m)
	default:
		e.Interface(key, m)
	}

	return true
}

// isEmptySource reports whether src is an empty caller location (a nil *slog.Source, or a zero-valued
// one) which slog's own handlers give a special case and elide.
//
// It is elided here too, so the two backends agree. It matters beyond the field itself: such a value
// logged under the reserved trace ID key would otherwise be read as a caller-supplied trace ID and
// suppress the injected one while writing nothing usable, leaving the record with a null correlation ID.
func isEmptySource(src *slog.Source) bool {
	return src == nil || *src == slog.Source{}
}

// addObjectEvent renders a zerolog.LogObjectMarshaler into a detached dictionary and attaches it to e
// only once the marshaler has returned, so a panic inside it cannot corrupt the record.
//
// zerolog's Event.Object appends the key and the opening brace onto e *before* calling
// MarshalZerologObject, and the buffer it writes into is unexported, so a recover cannot roll that
// back: the record would carry an object that is never closed, swallowing every field written after it
// (the message and the trace ID included) into an unparseable line. Through a baked WithAttrs the
// corruption lands in the zerolog context and is replayed on every subsequent record of that logger.
// Building the object separately keeps the failure local: on a panic the dictionary is discarded whole
// and the "!PANIC" sentinel is written as a plain string field instead.
//
// The output is byte-identical to Event.Object's on the success path.
//
//nolint:nonamedreturns // the deferred recover sets the result.
func addObjectEvent(e *zerolog.Event, key string, obj zerolog.LogObjectMarshaler) (wrote bool) {
	d := zerolog.Dict()

	defer func() {
		if r := recover(); r != nil {
			d.Send() // recycle the abandoned pooled event (see buildGroupDict)
			e.Str(key, panicSentinel(r))

			wrote = true
		}
	}()

	obj.MarshalZerologObject(d)
	e.Dict(key, d)

	return true
}

// panicSentinel renders a panic raised while formatting a value, mirroring the sentinel the standard
// library's handlers emit (see slog's handleState.appendValue).
func panicSentinel(r any) string {
	return fmt.Sprintf("!PANIC: %v", r)
}

// isNilError reports whether err is nil or a typed nil (a non-nil interface holding a nil pointer,
// such as a nil *MyErr) which zerolog's AnErr renders as no field at all.
//
// Only a pointer counts, mirroring zerolog's own isNilValue exactly. A nil value of a slice, map or
// func kind (an aggregate error such as a nil validator.ValidationErrors, whose underlying type is a
// slice) is NOT nil for this purpose: zerolog calls Error() on it and writes the field, so treating
// it as nil here would silently drop a field the sibling backend renders and, since the result
// drives group elision, drop its enclosing group too.
func isNilError(err error) bool {
	return err == nil || isNilPointer(err)
}

// isNilPointer reports whether x is a non-nil interface holding a nil pointer.
func isNilPointer(x any) bool {
	v := reflect.ValueOf(x)

	return v.Kind() == reflect.Pointer && v.IsNil()
}

// applyRootContext bakes a single root-level slog.Attr into a zerolog context (used to precompute
// the common/WithAttrs attributes) and reports whether it baked the reserved trace ID key at the
// root, so WithAttrs can suppress the injected trace ID without a second scan of the attributes.
//
// It mirrors addRootAttrEvent: an inlined (empty-key) group is flattened onto the root and walked
// here, the value is resolved exactly once and before the zero-Attr test (see resolveAttr), and only a
// field actually baked counts as the trace ID.
func applyRootContext(c zerolog.Context, a slog.Attr) (zerolog.Context, bool) {
	v, empty := resolveAttr(a)
	if empty {
		return c, false
	}

	if a.Key == "" && v.Kind() == slog.KindGroup {
		trace := false

		for _, ga := range v.Group() {
			var memberTrace bool

			c, memberTrace = applyRootContext(c, ga)
			trace = trace || memberTrace
		}

		return c, trace
	}

	c, baked := applyValueContext(c, a.Key, v)

	return c, baked && a.Key == logutil.TraceIDKey
}

// applyValueContext bakes an already-resolved value under key into c and reports whether it produced
// a field. It mirrors addValueEvent's type handling.
func applyValueContext(c zerolog.Context, key string, v slog.Value) (zerolog.Context, bool) {
	switch v.Kind() {
	case slog.KindGroup:
		return applyGroupContext(c, key, v.Group())
	case slog.KindAny, slog.KindLogValuer:
		return applyAnyContext(c, key, v)
	case slog.KindString:
		c = c.Str(key, v.String())
	case slog.KindInt64:
		c = c.Int64(key, v.Int64())
	case slog.KindUint64:
		c = c.Uint64(key, v.Uint64())
	case slog.KindFloat64:
		c = c.Float64(key, v.Float64())
	case slog.KindBool:
		c = c.Bool(key, v.Bool())
	case slog.KindDuration:
		c = c.Int64(key, int64(v.Duration()))
	case slog.KindTime:
		var buf [timeBufSize]byte

		c = c.RawJSON(key, v.Time().AppendFormat(buf[:0], timeLayout))
	}

	return c, true
}

// applyGroupContext bakes a named group's attributes into c under a sub-dictionary keyed by name and
// reports whether it produced a field. An empty group bakes nothing (no bare "{}"). Root-level
// inlining of an empty-key group is handled by applyRootContext, so key is never empty here.
func applyGroupContext(c zerolog.Context, key string, attrs []slog.Attr) (zerolog.Context, bool) {
	d := zerolog.Dict()
	wrote := false

	for _, ga := range attrs {
		if addAttrEvent(d, ga) {
			wrote = true
		}
	}

	if !wrote {
		d.Send() // recycle the unused pooled event (see buildGroupDict)

		return c, false
	}

	return c.Dict(key, d), true
}

// applyAnyContext bakes an arbitrary value into c and reports whether it produced a field. It mirrors
// addAnyEvent exactly, including the null rendering of a typed-nil LogObjectMarshaler, the single
// invocation of zerolog.ErrorMarshalFunc (see addErrEvent), the routing of a LogObjectMarshaler through
// a detached dictionary, and the recovery of a panic raised while rendering the value.
//
//nolint:nonamedreturns // the deferred recover sets the results.
func applyAnyContext(c zerolog.Context, key string, v slog.Value) (out zerolog.Context, wrote bool) {
	defer func() {
		if r := recover(); r != nil {
			out = c.Str(key, panicSentinel(r))

			wrote = true
		}
	}()

	x := v.Any()

	if err, isErr := x.(error); isErr {
		return applyErrContext(c, key, err)
	}

	if obj, isObj := x.(zerolog.LogObjectMarshaler); isObj {
		if isNilPointer(x) {
			return c.Interface(key, nil), true
		}

		return applyObjectContext(c, key, obj)
	}

	return c.Interface(key, x), true
}

// applyErrContext bakes an error into c and reports whether it produced a field, the context
// counterpart of addErrEvent: it drops a typed nil before consulting the hook, then mirrors zerolog's
// Context.AnErr while invoking the process-global ErrorMarshalFunc exactly once and reporting only what
// was actually baked.
func applyErrContext(c zerolog.Context, key string, err error) (zerolog.Context, bool) {
	if isNilError(err) {
		return c, false
	}

	switch m := zerolog.ErrorMarshalFunc(err).(type) {
	case nil:
		return c, false
	case zerolog.LogObjectMarshaler:
		return applyObjectContext(c, key, m)
	case error:
		if isNilError(m) {
			return c, false
		}

		return c.Str(key, m.Error()), true
	case string:
		return c.Str(key, m), true
	default:
		return c.Interface(key, m), true
	}
}

// applyObjectContext bakes a zerolog.LogObjectMarshaler into c through a detached dictionary, the
// context counterpart of addObjectEvent. zerolog's Context.Object renders into a scratch event, so a
// panic inside the marshaler does not corrupt the context as it does an Event's buffer, but the
// scratch event is then never returned to zerolog's pool. Building the dictionary here keeps both
// paths on one rendering and recycles it on the failure branch.
//
//nolint:nonamedreturns // the deferred recover sets the results.
func applyObjectContext(c zerolog.Context, key string, obj zerolog.LogObjectMarshaler) (out zerolog.Context, wrote bool) {
	d := zerolog.Dict()

	defer func() {
		if r := recover(); r != nil {
			d.Send() // recycle the abandoned pooled event (see buildGroupDict)

			out = c.Str(key, panicSentinel(r))

			wrote = true
		}
	}()

	obj.MarshalZerologObject(d)

	return c.Dict(key, d), true
}

// sourceDict builds the caller-location "source" object and reports whether it produced one at all,
// mirroring slog's AddSource group. It applies the same write-or-elide rules as sourceAttrs, which
// renders a *slog.Source logged as an attribute (a zero component is omitted, and a location with no
// components at all writes no field) because slog reaches both through the same Source.group() and
// isEmpty(). The two are kept apart only so this one, on the per-record path, can resolve the frame
// without allocating a *slog.Source or a slice; TestBackends_AgreeOnTheSourceField pins them together.
//
// A PC that does not resolve to a frame in this binary yields an empty location and so writes nothing,
// as it does under slog's own handlers. Only a hand-built, replayed or tee'd record can carry one:
// slog.Logger always stamps a live PC.
func sourceDict(record slog.Record) (*zerolog.Event, bool) {
	if record.PC == 0 {
		return nil, false
	}

	fs := runtime.CallersFrames([]uintptr{record.PC})
	f, _ := fs.Next()

	return sourceDictOf(slog.Source{Function: f.Function, File: f.File, Line: f.Line})
}

// sourceDictOf renders a caller location, and is where sourceDict's rules actually live: taking the
// resolved location rather than the record lets a test drive a *partial* one, which a real PC produces
// only rarely (a frame the runtime can name a file for but not a function, say) and which no record a
// test can construct on demand will reliably yield.
func sourceDictOf(src slog.Source) (*zerolog.Event, bool) {
	if isEmptySource(&src) {
		return nil, false
	}

	d := zerolog.Dict()

	if src.Function != "" {
		d.Str("function", src.Function)
	}

	if src.File != "" {
		d.Str("file", src.File)
	}

	if src.Line != 0 {
		d.Int("line", src.Line)
	}

	return d, true
}
