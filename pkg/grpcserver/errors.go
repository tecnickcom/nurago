package grpcserver

import "errors"

var (
	// ErrNilBinder is returned by New when a nil Binder is supplied.
	ErrNilBinder = errors.New("binder is required")

	// ErrServiceRegistration is returned by New when the Binder returns an
	// error or the gRPC server rejects a service registration (for example a
	// duplicate service name).
	ErrServiceRegistration = errors.New("service registration failed")

	// ErrInvalidTLSConfig is returned when a TLS configuration carries no
	// certificate material (no Certificates, GetCertificate, or GetConfigForClient).
	ErrInvalidTLSConfig = errors.New("invalid TLS configuration: no Certificates, GetCertificate, or GetConfigForClient set")

	// ErrInvalidFullMethod is returned when a method name is not in the
	// "/package.Service/Method" form.
	ErrInvalidFullMethod = errors.New("invalid full method name")

	// ErrInvalidMetadataKey is returned when a metadata key fails
	// grpcutil.ValidTraceIDMetadataKey.
	ErrInvalidMetadataKey = errors.New("invalid metadata key")

	// ErrInvalidServerOption is returned by New when grpc.NewServer rejects the
	// server options, for example a second grpc.InTapHandle.
	ErrInvalidServerOption = errors.New("invalid server option")
)
