/*
Package logsrv provides a zerolog backend exposed through the standard log/slog
API.

It bridges [log/slog] and zerolog with a native slog.Handler that writes each
record's attributes directly onto a zerolog Event. It reuses the shared
configuration model from nurago's logutil package.

[NewLogger] creates a slog.Logger backed by zerolog and applies:
  - log format selection (JSON, console, discard),
  - common structured attributes,
  - trace ID injection,
  - optional hook execution,
  - and full syslog level names (via logutil.LevelName).

# Compatibility

The logging model is compatible with:
  - Nicola Asuni, 2014-08-11, "Software Logging Format",
    https://technick.net/guides/software/software_logging_format/

See also:
  - github.com/tecnickcom/nurago/pkg/logutil

# Notes

Fields are written in the order the attributes were added (record attributes follow
the common/WithAttrs attributes), not sorted. Times and durations are written explicitly, matching
the standard library's encoding (RFC 3339 with nanosecond precision, and a nanosecond count) rather
than zerolog's process-global TimeFieldFormat and DurationFieldUnit: those default to whole-second
RFC 3339 and milliseconds, which would truncate every timestamp and render the same slog.Attr in a
different unit than logutil's standard-library backend does. The *values* of the timestamp and duration
fields are therefore independent of the zerolog globals that any other user of zerolog in the binary
can change; the rest of the output is not. ErrorMarshalFunc and InterfaceMarshalFunc still decide how
an error and an arbitrary value are rendered (ErrorMarshalFunc is invoked exactly once per error
attribute), FloatingPointPrecision still rounds every float attribute, and TimestampFieldName,
LevelFieldName and MessageFieldName still name the "time", "level" and "message" fields.

# Differences from the logutil backend

The two backends are interchangeable, but they are not byte-identical: this one encodes through zerolog
and logutil's through the standard library. The message field is named "message" here and "msg" there
(zerolog's MessageFieldName versus slog's), and the injected trace ID is written after the record's
attributes here and before them there. The deliberate value-level divergences are:

  - An error renders as its message string (zerolog's error form) even when it also implements
    json.Marshaler, which logutil's backend marshals as a JSON object instead.
  - A value implementing zerolog.LogObjectMarshaler is written as the object that marshaler produces;
    logutil's backend marshals it as JSON, so the same value can reach the wire with different field
    names.
  - A nil-pointer error writes no field at all, where the standard library renders it as the string
    "<nil>". Both backends agree (logutil's sanitizing handler drops it too, see
    logutil.NewSlogTraceIDHandler), but a bare slog handler does not. It matters beyond the field: a
    typed nil logged under "trace_id" would otherwise be read as a caller-supplied trace ID and suppress
    the injected one, leaving the record correlated by the string "<nil>".
  - A value JSON cannot represent renders as zerolog's text rather than slog's: NaN and ±Inf as the
    strings "NaN" and "+Inf" (slog: "!ERROR:json: unsupported value: NaN"), and an unmarshalable value
    as "marshaling error: ..." (slog: "!ERROR:..."). A log-search rule keyed on "!ERROR:" therefore does
    not fire on this backend.
  - Replacing the process-global zerolog.ErrorMarshalFunc with one that maps a non-nil error to nil, or
    to a typed-nil error, makes this backend write no field for it (and elide a group left empty by it),
    while logutil's backend, which cannot see a zerolog global, still renders the error. Those are the
    only shapes on which the two disagree about whether an attribute writes a field.
  - A value that renders differently on the two backends (any of the shapes above) and is logged under
    the reserved "trace_id" key correlates the record under a different ID on each: the field is written
    exactly once by both, but they render it differently.

In FormatConsole two further differences come from zerolog's ConsoleWriter, which re-parses the JSON
line into a map before rendering it:

  - Duplicate keys collapse last-wins. That only arises when the caller supplies a root "trace_id"
    twice themselves (see below), and the survivor is the *last* one, so the console shows the group,
    not the value supplied via With/CommonAttr. The JSON format keeps both.
  - A caller location renders as an object, where the standard library's text handler renders it as
    "file:line", whether it was logged as a *slog.Source attribute or emitted by cfg.Source. Which
    components it carries, and whether it is written at all, agree in both formats; only the rendering
    differs.

The emitted "level" field carries the full syslog severity name via logutil.LevelName
("emergency", "alert", "critical", "error", "warning", "notice", "info", "debug",
"trace"), matching logutil's backend: the extended severities are not collapsed onto
zerolog's fixed level set. In FormatConsole mode, zerolog's ConsoleWriter colorizes only
its own level vocabulary, so the extended names render without color.

An error value is rendered as its message string whatever key it is logged under (the "error"
and "err" keys are not special: the rendering is chosen by the value's type). An error implementing
zerolog.LogObjectMarshaler is the exception zerolog itself makes: it is written as a JSON object, as is
any non-error value implementing it.

Errors are rendered by mirroring zerolog's own AnErr dispatch, so the process-global ErrorMarshalFunc
decides the outcome and is invoked at most once per error attribute: whatever it returns is what is
written (nothing at all, for nil), and the field is reported as written only when it really was, so an
enclosing group is never left as a bare "{}".

A nil-pointer error (e.g. a nil *MyError) is omitted entirely, and a group left with no other fields by
such a value is elided along with it. That test precedes ErrorMarshalFunc, unlike zerolog's AnErr,
which tests for nil only within its error arm and so still writes a typed nil that can render itself as
an object (a LogObjectMarshaler guarding its nil receiver) or that the hook renders itself. Those are
dropped here too, and the hook is not invoked for them, because the sibling logutil backend decides the
same question with a filter that can see neither zerolog's interfaces nor its globals: a rule
conditional on either could not be mirrored there, and the two backends would ship different field sets
(and different trace IDs) for one slog.Attr. A nil error is no error; neither backend writes one.

A nil error of any other kind (a nil slice, map, func or channel, the shape of aggregate errors such as
validator.ValidationErrors) is not omitted: it is not a nil pointer, so Error() is called on it and it
renders as its message.

An untyped nil value (slog.Any(key, nil)) is emitted as a null field, and so is a typed-nil
LogObjectMarshaler that is not an error: calling its marshaler would panic on the nil receiver unless it
guards against one, and a nil value is more usefully rendered as null (which is what the standard
library writes) than as a panic sentinel. A *slog.Source is given slog's own special case: an empty one
(nil, or zero-valued) writes no field, and a populated one is written as its function/file/line group,
which inlines onto the enclosing level when its key is empty. A panic raised while a value renders
itself (Error, MarshalJSON, MarshalText, MarshalZerologObject) is recovered and written as slog's
"!PANIC" sentinel, so a log call cannot take the process down.

The built-in field names "level", "time", "message" and "source" are reserved: a user attribute
with the same name is written in addition to the built-in one, producing a duplicate JSON key
(as in the standard library's slog handlers). Its value is written as this backend renders it, which
for a slog.Level under the "level" key means slog's own name ("WARN") rather than the syslog name
logutil's backend rewrites it to ("warning"). "trace_id" is reserved too, but is deduplicated:
a caller-supplied root-level trace_id replaces the injected one instead of duplicating it,
whether it arrives as a record attribute, via WithAttrs/With, via cfg.CommonAttr, inside an
inlined (empty-key) group on any of those paths, or as a root group opened under the key
(WithGroup("trace_id")), which writes a root-level trace_id object of its own, unless that group
renders no fields, in which case the injected value is written as usual.

What that guarantees is that the *injected* trace ID never duplicates a caller-supplied one, so a
record normally carries exactly one root trace_id. It cannot stop a caller from writing the key twice
themselves: With(slog.String("trace_id", ...)) followed by WithGroup("trace_id") supplies two
root-level trace_id fields of the caller's own, and both are written, as they are by a bare
standard-library handler, since dropping either would discard the caller's data. A nil-pointer error
under the key is a related case: it writes no field, so the injected trace ID stands, and the record
still carries one.

The writer (cfg.Out) is wrapped so concurrent logging is safe even for a non-thread-safe
destination: the lock is held by the handler returned from a single NewHandler call and every
handler derived from it. Two handlers built from separate NewHandler calls hold separate locks, so
a non-thread-safe writer shared between them must be serialized by the caller.

Records are handed to zerolog at zerolog.NoLevel, which the process-global zerolog level still
gates: a caller who sets zerolog.SetGlobalLevel(zerolog.Disabled) anywhere in the binary silently
drops every record regardless of cfg.Level. No other global level affects the output. The record is
still encoded when it is dropped (every value is resolved and every caller marshaler runs, so their
side effects still fire) because zerolog discards the event at the write, not at the build; use
cfg.Level (or logutil.FormatNone) to stop the work as well as the output.

# When To Use

  - You want zerolog's encoding performance while application code depends
    only on log/slog.
  - Log output must be JSON in production and human-readable in development.
*/
package logsrv

import (
	"log/slog"

	"github.com/rs/zerolog"
	"github.com/tecnickcom/nurago/pkg/logutil"
)

// NewLogger constructs a slog.Logger backed by zerolog, configured via logutil.Config,
// and installs it as the process-wide slog default.
//
// Use [NewHandler] (for example slog.New(logsrv.NewHandler(cfg))) when a logger is
// needed without replacing the global default.
//
// A nil cfg falls back to logutil.DefaultConfig. See [NewHandler] for the details of
// format selection, attributes, trace-ID injection, hooks, and level naming.
func NewLogger(cfg *logutil.Config) *slog.Logger {
	sl := slog.New(NewHandler(cfg))

	slog.SetDefault(sl)

	return sl
}

// NewHandler constructs the slog.Handler backing a logsrv logger, without mutating any
// global logger state. Applies format selection, common attributes, trace-ID injection,
// hooks, and full syslog level naming. A nil cfg falls back to logutil.DefaultConfig, and an unusable
// Out writer (nil, or a typed nil such as a nil *os.File assigned straight to the exported field)
// falls back to os.Stderr (see logutil.Config.OutWriter), so construction never yields a handler that
// panics on the first write.
//
// The trace ID is resolved per record via cfg.TraceIDFn (matching the logutil model), so
// a dynamic TraceIDFn reflects the current request/context on every line rather than being
// frozen at construction. The handler writes it natively at the root of every record (even
// for loggers derived with WithGroup), and a caller-supplied root trace_id takes precedence:
// supplying it as a record attribute, via WithAttrs/With, or via cfg.CommonAttr suppresses the
// injected one rather than emitting a second trace_id key, as does opening a root group under the
// key (WithGroup("trace_id")), which writes a root-level trace_id of its own. Suppression requires
// the caller's field to actually render: one that is elided (an empty group, a nil-pointer error)
// leaves the injected value in place. A trace_id logged *under* an open group nests inside that
// group (standard slog semantics) and does not suppress the root one, since the two live at
// different nesting levels. A nil TraceIDFn is valid and simply omits the field.
//
// The hook (cfg.HookFn) is invoked at the slog layer, before the record is handed to
// zerolog, so it receives the original record level (e.g. logutil.LevelNotice or
// logutil.LevelCritical) rather than any derived value.
//
// Note: the emitted "level" field carries the full syslog severity name via logutil.LevelName
// (e.g. "critical", "notice", "emergency"), matching logutil's backend rather than collapsing
// onto zerolog's fixed level set. In FormatConsole mode, zerolog's ConsoleWriter colorizes only
// its own level vocabulary, so the extended names render (uncolored) as their upper-cased prefix.
func NewHandler(cfg *logutil.Config) slog.Handler {
	if cfg == nil {
		cfg = logutil.DefaultConfig()
	}

	// FormatNone with no hook has nothing to write and no side effect to fire, so a
	// zero-cost DiscardHandler (Enabled == false) is used instead of running the full
	// zerolog encode path into io.Discard on every record.
	if cfg.Format == logutil.FormatNone && cfg.HookFn == nil {
		return slog.DiscardHandler
	}

	out := cfg.OutWriter()

	// SyncWriter serializes writes so concurrent logging to a non-thread-safe cfg.Out (or the
	// stateful ConsoleWriter) is race-free, matching logutil's standard-library backend. os.Stderr
	// is already serialized at the runtime FD layer; the extra lock is negligible. The lock belongs
	// to this handler and the handlers derived from it, not to cfg.Out: two handlers built from the
	// same Config hold different locks, so a caller sharing one non-thread-safe writer across
	// separate NewHandler calls must serialize it themselves.
	//
	// errWriter records the write error so Handle can return it (zerolog's Event API reports none),
	// while still letting zerolog apply its own fallback for callers that discard it, as slog.Logger
	// does.
	ew := &errWriter{w: writerByFormat(cfg.Format, out)}

	// TraceLevel base so the logger's own level never gates a record: enablement is decided at the
	// slog layer (via Enabled), matching cfg.Level exactly, including the Trace level, which a
	// default (Debug-level) zerolog logger would otherwise drop.
	//
	// The process-global zerolog level is the one exception left: records are written at
	// zerolog.NoLevel (ordinal 6), so only zerolog.SetGlobalLevel(zerolog.Disabled) (ordinal 7)
	// still gates them, silently dropping every record regardless of cfg.Level. Any lower global
	// level (Error, Info, ...) leaves the output untouched.
	zl := zerolog.New(zerolog.SyncWriter(ew)).Level(zerolog.TraceLevel)

	var h slog.Handler = &zerologHandler{
		logger:    zl,
		out:       ew,
		traceIDFn: cfg.TraceIDFn,
		minLevel:  cfg.Level,
		source:    cfg.Source,
	}

	// Bake the common attributes into the zerolog context once, so they are serialized
	// a single time and memcpy'd per record rather than re-encoded on every line.
	h = h.WithAttrs(cfg.CommonAttr)

	// The trace ID is written natively by the handler (at the root of every record, even under an
	// open group), so no trace-ID wrapper is needed. Wrap with the hook handler (as logutil does)
	// instead of hooking the zerolog event: a zerolog hook would only see the NoLevel event,
	// losing the original severity.
	if cfg.HookFn != nil {
		h = logutil.NewSlogHookHandler(h, cfg.HookFn)
	}

	return h
}
