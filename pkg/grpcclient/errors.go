package grpcclient

import "errors"

var (
	// ErrConflictingTransportSecurity is returned by New when more than one of
	// WithInsecure, WithTLSConfig, and WithCredentialsBundle is set.
	ErrConflictingTransportSecurity = errors.New("WithInsecure, WithTLSConfig, and WithCredentialsBundle cannot be combined")

	// ErrInvalidRetryPolicy is returned by WithRetryPolicy for a policy that
	// fails validation or overlaps the scope of a policy already set, and by
	// New for a MaxAttempts above the call attempt cap.
	ErrInvalidRetryPolicy = errors.New("invalid retry policy")

	// ErrInvalidMetadataKey is returned when a metadata key fails
	// grpcutil.ValidTraceIDMetadataKey.
	ErrInvalidMetadataKey = errors.New("invalid metadata key")

	// ErrNewClient is returned by New when grpc.NewClient rejects the target or
	// the dial options.
	ErrNewClient = errors.New("failed creating the grpc client")
)
