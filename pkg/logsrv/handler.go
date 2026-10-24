package logsrv

import (
	"context"
	"log/slog"

	"github.com/rs/zerolog"
	"github.com/tecnickcom/nurago/pkg/logutil"
)

// groupFrame is one open WithGroup level together with the WithAttrs added within it.
// Attributes added under an open group must nest below the group in the output, so they
// cannot be baked into the zerolog context (which only appends at the root); they are
// replayed per record instead.
type groupFrame struct {
	name  string
	attrs []slog.Attr
}

// zerologHandler is the native leaf slog.Handler. It writes records directly onto zerolog
// Events. Pre-group attributes are baked into logger via zerolog's context so the common,
// ungrouped path allocates nothing; open groups fall back to per-record nesting.
type zerologHandler struct {
	logger    zerolog.Logger      // pre-group attributes baked in via With()
	out       *errWriter          // the destination, remembering a failed write so Handle can report it
	groups    []groupFrame        // open groups (nil on the common ungrouped fast path)
	traceIDFn logutil.TraceIDFunc // resolves the root trace ID per record (nil omits the field)
	minLevel  slog.Level          // minimum enabled level (from logutil.Config.Level)
	source    bool                // whether to emit the caller "source" field
	traceAttr bool                // whether a root trace_id is already baked into logger (suppresses the injected one)
}

// Enabled reports whether a record at the given level should be handled. Groups and
// attributes do not affect enablement.
func (h *zerologHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

// Handle writes the record onto a zerolog Event: level, timestamp, optional source, the record's
// attributes (nested under any open groups) with the root trace ID written alongside them (after
// them on the ungrouped path, before the open group on the grouped one) and the message.
//
// It returns the destination's write error, if any (see errWriter). slog.Logger discards it, so
// zerolog's own diagnostic on os.Stderr remains the fallback; a wrapping handler, or a caller
// invoking Handle directly, can act on it.
func (h *zerologHandler) Handle(_ context.Context, record slog.Record) error {
	// NoLevel so zerolog writes no level field of its own; the level is written below as the
	// full syslog name, preserving the extended severities instead of collapsing them onto
	// zerolog's fixed set.
	e := h.logger.WithLevel(zerolog.NoLevel)

	e.Str(zerolog.LevelFieldName, logutil.LevelName(record.Level))

	// Omit the time field for a zero timestamp, matching slog's own handlers. The value is
	// formatted onto the stack and written as raw JSON (see timeLayout): allocation-free, and
	// independent of the process-global zerolog.TimeFieldFormat that any other zerolog user in
	// the binary can change.
	if !record.Time.IsZero() {
		var buf [timeBufSize]byte

		e.RawJSON(zerolog.TimestampFieldName, record.Time.AppendFormat(buf[:0], timeLayout))
	}

	// A zero PC, and one that does not resolve to a frame in this binary, both write no source field,
	// as under slog's own handlers, which elide an empty caller location (see sourceDict).
	if h.source {
		if d, ok := sourceDict(record); ok {
			e.Dict("source", d)
		}
	}

	if len(h.groups) == 0 {
		h.writeRoot(e, &record)

		e.Msg(record.Message)

		return h.out.takeErr()
	}

	// A group is open: the trace ID stays at the root while the record's attributes nest in the
	// group, which is omitted entirely when it (and its subgroups) carry no fields so output matches
	// slog's empty-group elision. The dictionary is built before the trace ID is written (though
	// emitted after it, keeping the field order) because whether it renders decides the injection: a
	// group named trace_id puts a root-level trace_id field of its own on the record, so it must
	// suppress the injected one, but only when it actually renders, since an elided group writes
	// nothing. A trace_id baked in before the group was opened (h.traceAttr) suppresses it likewise.
	dict, wrote := h.buildGroupDict(h.groups, &record)
	rootIsTrace := wrote && h.groups[0].name == logutil.TraceIDKey

	if h.traceIDFn != nil && !h.traceAttr && !rootIsTrace {
		e.Str(logutil.TraceIDKey, h.traceIDFn())
	}

	if wrote {
		e.Dict(h.groups[0].name, dict)
	}

	e.Msg(record.Message)

	return h.out.takeErr()
}

// WithAttrs returns a handler carrying the given attributes. With no open group the
// attributes are baked into the zerolog context (keeping the fast path allocation-free);
// under an open group they are appended to the innermost frame for per-record nesting.
// Per the slog.Handler contract, an empty attribute list returns the receiver.
func (h *zerologHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	nh := *h

	if len(h.groups) == 0 {
		// Baked attributes are serialized into the zerolog context here, so the per-record scan in
		// writeRoot cannot see them: applyRootContext reports a baked root trace_id as it writes it,
		// and the flag is remembered. Without it the injected trace ID would be written after the
		// baked one, and a last-wins JSON parser would resolve the caller's trace_id to the injected
		// value (empty by default), silently losing it.
		c := h.logger.With()
		trace := h.traceAttr

		for _, a := range attrs {
			var baked bool

			c, baked = applyRootContext(c, a)
			trace = trace || baked
		}

		nh.logger = c.Logger()
		nh.traceAttr = trace

		return &nh
	}

	// Attributes added under an open group nest inside it, so they never shadow the root trace ID.

	nh.groups = make([]groupFrame, len(h.groups))
	copy(nh.groups, h.groups)
	last := len(nh.groups) - 1
	merged := make([]slog.Attr, 0, len(nh.groups[last].attrs)+len(attrs))
	merged = append(merged, nh.groups[last].attrs...)
	merged = append(merged, attrs...)
	nh.groups[last].attrs = merged

	return &nh
}

// WithGroup returns a handler that nests subsequent attributes under name. Per the
// slog.Handler contract, an empty group name returns the receiver unchanged.
func (h *zerologHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	nh := *h
	nh.groups = make([]groupFrame, len(h.groups)+1)
	copy(nh.groups, h.groups)
	nh.groups[len(h.groups)] = groupFrame{name: name}

	return &nh
}

// writeRoot writes the record's attributes at the root (the ungrouped fast path) and injects the
// trace ID unless the caller already supplied a root-level trace_id (as a record attribute, reported
// by addRootAttrEvent as it writes it, or a baked WithAttrs/CommonAttr one via h.traceAttr), in which
// case theirs wins and TraceIDFn is not invoked, matching logutil's trace handler.
func (h *zerologHandler) writeRoot(e *zerolog.Event, record *slog.Record) {
	hasTrace := h.traceAttr

	record.Attrs(func(a slog.Attr) bool {
		if _, trace := addRootAttrEvent(e, a); trace {
			hasTrace = true
		}

		return true
	})

	if h.traceIDFn != nil && !hasTrace {
		e.Str(logutil.TraceIDKey, h.traceIDFn())
	}
}

// buildGroupDict builds the nested zerolog dictionary for the open group chain, adding each
// frame's attributes and, at the innermost frame, the record's attributes. It reports whether
// the chain produced any field, so the caller can omit an all-empty (sub)group rather than
// render it as a bare "{}" object. This is a single pass: attribute values are resolved once.
//
//nolint:gocognit
func (h *zerologHandler) buildGroupDict(frames []groupFrame, record *slog.Record) (*zerolog.Event, bool) {
	d := zerolog.Dict()
	wrote := false

	for _, a := range frames[0].attrs {
		if addAttrEvent(d, a) {
			wrote = true
		}
	}

	if len(frames) > 1 {
		if child, childWrote := h.buildGroupDict(frames[1:], record); childWrote {
			d.Dict(frames[1].name, child)

			wrote = true
		}
	} else {
		record.Attrs(func(a slog.Attr) bool {
			if addAttrEvent(d, a) {
				wrote = true
			}

			return true
		})
	}

	if !wrote {
		// Recycle the unused pooled event rather than leaking it: Send on a detached dict
		// (nil writer) emits nothing and returns the event to zerolog's pool.
		d.Send()

		return nil, false
	}

	return d, wrote
}
