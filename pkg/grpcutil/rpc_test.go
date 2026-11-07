package grpcutil

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/protobuf/proto"
)

func TestStreamType(t *testing.T) {
	t.Parallel()

	require.Equal(t, RPCTypeBidiStream, StreamType(true, true))
	require.Equal(t, RPCTypeClientStream, StreamType(true, false))
	require.Equal(t, RPCTypeServerStream, StreamType(false, true))
}

func TestSplitFullMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		service string
		method  string
	}{
		{in: "/pkg.Service/Method", service: "pkg.Service", method: "Method"},
		{in: "pkg.Service/Method", service: "pkg.Service", method: "Method"},
		{in: "/malformed", service: "", method: "/malformed"},
		{in: "", service: "", method: ""},
	}

	for _, tt := range tests {
		service, method := SplitFullMethod(tt.in)
		require.Equal(t, tt.service, service, tt.in)
		require.Equal(t, tt.method, method, tt.in)
	}
}

func TestParseFullMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		service string
		method  string
		ok      bool
	}{
		{name: "/pkg.Service/Method", service: "pkg.Service", method: "Method", ok: true},
		{name: "/Service/Method", service: "Service", method: "Method", ok: true},
		{name: ""},
		{name: "/"},
		{name: "pkg.Service/Method"},
		{name: "/pkg.Service"},
		{name: "/pkg.Service/"},
		{name: "//Method"},
		{name: "/pkg.Service/Method/Extra"},
	}

	for _, tt := range tests {
		service, method, ok := ParseFullMethod(tt.name)
		require.Equal(t, tt.ok, ok, tt.name)
		require.Equal(t, tt.service, service, tt.name)
		require.Equal(t, tt.method, method, tt.name)
	}
}

func TestDumpMessage(t *testing.T) {
	t.Parallel()

	redact := func(b []byte) string { return string(b) }

	require.Empty(t, DumpMessage(nil, 16, redact))
	require.Empty(t, DumpMessage("not a proto message", 16, redact))
	require.Empty(t, DumpMessage((*testgrpc.SimpleResponse)(nil), 16, redact))
	require.JSONEq(t, `{"username":"u"}`, DumpMessage(&testgrpc.SimpleResponse{Username: "u"}, 16, redact))
	require.Equal(t, "x", DumpMessage(&testgrpc.SimpleResponse{Username: "u"}, 16, func([]byte) string { return "x" }))

	big := &testgrpc.SimpleRequest{Payload: &testgrpc.Payload{Body: make([]byte, 1020)}}
	require.Equal(t,
		fmt.Sprintf("[omitted: %d bytes exceeds the dump cap of 16 bytes]", proto.Size(big)),
		DumpMessage(big, 16, redact),
	)

	// protojson rejects strings that are not valid UTF-8.
	require.Contains(t, DumpMessage(&testgrpc.SimpleResponse{Username: "\xff"}, 16, redact), "[dump failed: ")
}
