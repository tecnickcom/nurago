package grpcutil

import "google.golang.org/grpc"

// ServerInstrumenter supplies the server options that instrument a gRPC
// server, such as metrics interceptors or a stats handler.
type ServerInstrumenter interface {
	// ServerOptions returns the options added to the server.
	ServerOptions() []grpc.ServerOption
}

// ClientInstrumenter supplies the dial options that instrument a gRPC client
// connection, such as metrics interceptors or a stats handler.
type ClientInstrumenter interface {
	// DialOptions returns the options added to the client connection.
	DialOptions() []grpc.DialOption
}

// ServerInitializer is optionally implemented by a [ServerInstrumenter] that
// needs the registered services, for example to create metric series for
// every method before the first call.
type ServerInitializer interface {
	// InitializeServer is called once, after every service is registered on s
	// and before s starts serving.
	InitializeServer(s *grpc.Server)
}

// ServerInterceptorReporter is optionally implemented by a
// [ServerInstrumenter] to report whether its server options add interceptors.
// An instrumenter that does not implement it is treated as adding
// interceptors.
type ServerInterceptorReporter interface {
	// HasServerInterceptors reports whether ServerOptions adds unary or stream
	// interceptors.
	HasServerInterceptors() bool
}
