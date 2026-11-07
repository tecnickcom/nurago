package grpcutil

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// RedactFn redacts sensitive values from logged byte payloads and returns a redacted string.
type RedactFn func(b []byte) string

// SplitFullMethod splits "/package.Service/Method" into service and method.
// A name without "/" after the optional leading one is returned whole as the
// method.
func SplitFullMethod(fullMethod string) (string, string) {
	service, method, ok := strings.Cut(strings.TrimPrefix(fullMethod, "/"), "/")
	if !ok {
		return "", fullMethod
	}

	return service, method
}

// ParseFullMethod splits a "/package.Service/Method" full method name into
// service and method. It returns false when the leading "/" is missing, the
// service or method is empty, or the method contains "/".
func ParseFullMethod(fullMethod string) (string, string, bool) {
	rest, found := strings.CutPrefix(fullMethod, "/")
	if !found {
		return "", "", false
	}

	service, method, ok := strings.Cut(rest, "/")
	if !ok || service == "" || method == "" || strings.Contains(method, "/") {
		return "", "", false
	}

	return service, method, true
}

// DumpMessage returns the protobuf JSON form of m passed through redact.
// Messages whose encoded size exceeds maxSize are replaced with an omission
// notice, and a marshaling failure with an error notice. Nil messages and
// values that are not protobuf messages produce an empty string.
func DumpMessage(m any, maxSize int, redact RedactFn) string {
	pm, ok := m.(proto.Message)
	if !ok || !pm.ProtoReflect().IsValid() {
		return ""
	}

	size := proto.Size(pm)
	if size > maxSize {
		return fmt.Sprintf("[omitted: %d bytes exceeds the dump cap of %d bytes]", size, maxSize)
	}

	b, err := protojson.Marshal(pm)
	if err != nil {
		return "[dump failed: " + err.Error() + "]"
	}

	return redact(b)
}
