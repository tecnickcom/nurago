/*
Package logutil provides configuration-driven logging utilities built around
Go's standard log/slog package.

It wraps slog with a composable configuration model:
  - [Config] and [Option] build loggers from strongly typed settings.
  - [Config.SlogLogger], [Config.SlogHandler], and
    [Config.SlogDefaultLogger] construct handler/logger instances.
  - [ParseLevel]/[ParseFormat] and [ValidLevel]/[ValidFormat] convert and
    validate runtime configuration values.
  - [NewSlogHookHandler] allows interception of log messages via [HookFunc].
  - [NewSlogWriter] and [NewLogFromSlog] bridge standard log.Logger output
    into slog.

Output can be JSON, text console, or discard mode. The severity model extends
the syslog-style levels (emergency through debug) with a trace level.

# Notes

The handlers built by [Config.SlogHandler] and [NewSlogTraceIDHandler] write a record through the
standard library's JSON or text handler, but they filter it first (see the sanitizing handler
documented on [NewSlogTraceIDHandler]), so the output deliberately differs from a bare slog handler's
in three ways. Two are repairs of shapes the standard library encodes incorrectly:

  - A group whose members all render nothing is dropped. slog rolls the buffer back past such a group
    without closing it, so the next attribute is written with no separator: invalid JSON, or, in text
    format, a field silently renamed with the dead group's prefix.
  - A time.Time whose year falls outside [0,9999] is rewritten as an RFC 3339 string. slog's JSON
    encoder writes an "!ERROR:" string for it and then writes the value as well, putting two JSON
    strings under one key.

The third keeps the two backends of this module interchangeable, since logsrv encodes through zerolog:

  - A nil-pointer error writes no field, where slog renders it as the string "<nil>". A group left
    empty by one is dropped with it, and a typed nil logged under the trace ID key no longer suppresses
    the injected trace ID, which would otherwise correlate the record by the string "<nil>". A nil
    error of any other kind (a nil slice, map, func or channel, the shape of aggregate errors such as
    validator.ValidationErrors) still renders as its message.

A group that resolves to zero members is likewise dropped rather than written as a bare "{}".

# When To Use

  - Log level and format come from configuration strings rather than from
    code.
  - Every record needs common attributes such as service name and trace ID.
  - A hook must observe emitted records, for example to increment a metrics
    counter per level.
*/
package logutil
