package grpcclient

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"google.golang.org/grpc/codes"
)

// defaultMaxCallAttempts is the grpc-go cap on the attempts of a call when
// grpc.WithMaxCallAttempts is not set.
const defaultMaxCallAttempts = 5

// codeNames are the google.rpc.Code names used in a service config, indexed by
// gRPC code.
//
//nolint:gochecknoglobals // immutable lookup table
var codeNames = [...]string{
	codes.OK:                 "OK",
	codes.Canceled:           "CANCELLED", //nolint:misspell // google.rpc.Code name
	codes.Unknown:            "UNKNOWN",
	codes.InvalidArgument:    "INVALID_ARGUMENT",
	codes.DeadlineExceeded:   "DEADLINE_EXCEEDED",
	codes.NotFound:           "NOT_FOUND",
	codes.AlreadyExists:      "ALREADY_EXISTS",
	codes.PermissionDenied:   "PERMISSION_DENIED",
	codes.ResourceExhausted:  "RESOURCE_EXHAUSTED",
	codes.FailedPrecondition: "FAILED_PRECONDITION",
	codes.Aborted:            "ABORTED",
	codes.OutOfRange:         "OUT_OF_RANGE",
	codes.Unimplemented:      "UNIMPLEMENTED",
	codes.Internal:           "INTERNAL",
	codes.Unavailable:        "UNAVAILABLE",
	codes.DataLoss:           "DATA_LOSS",
	codes.Unauthenticated:    "UNAUTHENTICATED",
}

// RetryPolicy is a gRPC retry policy (gRFC A6), executed by grpc-go for the
// RPCs in its scope.
//
// The scope is the union of Services and Methods. A policy with both empty is
// the default policy: it applies to every method not covered by another
// policy. A method policy takes precedence over a service policy.
type RetryPolicy struct {
	// Services lists the services ("package.Service") whose methods the policy applies to.
	Services []string

	// Methods lists the methods ("/package.Service/Method") the policy applies to.
	Methods []string

	// MaxAttempts is the maximum number of attempts, including the original
	// one. It must be at least 2.
	MaxAttempts int

	// InitialBackoff is the upper bound of the random delay before the first
	// retry. It must be positive.
	InitialBackoff time.Duration

	// MaxBackoff caps the upper bound of the random delay. It must be positive.
	MaxBackoff time.Duration

	// BackoffMultiplier multiplies the upper bound of the random delay after
	// each retry. It must be positive and finite.
	BackoffMultiplier float64

	// RetryableCodes lists the status codes that trigger a retry. It must not
	// be empty and must not contain codes.OK.
	RetryableCodes []codes.Code
}

// retryPolicy is a validated RetryPolicy with the service config names of its
// scope.
type retryPolicy struct {
	RetryPolicy

	names []scName
}

// scName is a service config method name.
type scName struct {
	Service string `json:"service,omitempty"`
	Method  string `json:"method,omitempty"`
}

// scRetryPolicy is a service config retry policy.
type scRetryPolicy struct {
	MaxAttempts          int      `json:"maxAttempts"`
	InitialBackoff       string   `json:"initialBackoff"`
	MaxBackoff           string   `json:"maxBackoff"`
	BackoffMultiplier    float64  `json:"backoffMultiplier"`
	RetryableStatusCodes []string `json:"retryableStatusCodes"`
}

// scMethodConfig is a service config method config.
type scMethodConfig struct {
	Name        []scName      `json:"name"`
	RetryPolicy scRetryPolicy `json:"retryPolicy"`
}

// scServiceConfig is a service config carrying only method configs.
type scServiceConfig struct {
	MethodConfig []scMethodConfig `json:"methodConfig"`
}

// validate checks the policy fields and returns the service config names of
// its scope.
func (p RetryPolicy) validate() ([]scName, error) {
	switch {
	case p.MaxAttempts < 2:
		return nil, fmt.Errorf("%w: MaxAttempts must be at least 2", ErrInvalidRetryPolicy)
	case p.InitialBackoff <= 0:
		return nil, fmt.Errorf("%w: InitialBackoff must be positive", ErrInvalidRetryPolicy)
	case p.MaxBackoff <= 0:
		return nil, fmt.Errorf("%w: MaxBackoff must be positive", ErrInvalidRetryPolicy)
	case !(p.BackoffMultiplier > 0) || math.IsInf(p.BackoffMultiplier, 1):
		return nil, fmt.Errorf("%w: BackoffMultiplier must be positive and finite", ErrInvalidRetryPolicy)
	case len(p.RetryableCodes) == 0:
		return nil, fmt.Errorf("%w: RetryableCodes must not be empty", ErrInvalidRetryPolicy)
	}

	for _, c := range p.RetryableCodes {
		if c == codes.OK || int(c) >= len(codeNames) {
			return nil, fmt.Errorf("%w: invalid retryable code %v", ErrInvalidRetryPolicy, c)
		}
	}

	return p.names()
}

// names returns the service config names of the policy scope. The default
// policy has one empty name.
func (p RetryPolicy) names() ([]scName, error) {
	if len(p.Services) == 0 && len(p.Methods) == 0 {
		return []scName{{}}, nil
	}

	names := make([]scName, 0, len(p.Services)+len(p.Methods))

	for _, s := range p.Services {
		if !validServiceName(s) {
			return nil, fmt.Errorf("%w: invalid service name %q", ErrInvalidRetryPolicy, s)
		}

		names = append(names, scName{Service: s})
	}

	for _, m := range p.Methods {
		svc, method, ok := grpcutil.ParseFullMethod(m)
		if !ok {
			return nil, fmt.Errorf("%w: invalid full method name %q", ErrInvalidRetryPolicy, m)
		}

		names = append(names, scName{Service: svc, Method: method})
	}

	return names, nil
}

// validServiceName reports whether s is a non-empty service name without "/".
func validServiceName(s string) bool {
	return s != "" && !strings.Contains(s, "/")
}

// serviceConfigJSON returns the service config JSON for policies.
func serviceConfigJSON(policies []retryPolicy) string {
	sc := scServiceConfig{MethodConfig: make([]scMethodConfig, 0, len(policies))}

	for _, p := range policies {
		retryCodes := make([]string, 0, len(p.RetryableCodes))
		for _, c := range p.RetryableCodes {
			retryCodes = append(retryCodes, codeNames[c])
		}

		sc.MethodConfig = append(sc.MethodConfig, scMethodConfig{
			Name: p.names,
			RetryPolicy: scRetryPolicy{
				MaxAttempts:          p.MaxAttempts,
				InitialBackoff:       durationJSON(p.InitialBackoff),
				MaxBackoff:           durationJSON(p.MaxBackoff),
				BackoffMultiplier:    p.BackoffMultiplier,
				RetryableStatusCodes: retryCodes,
			},
		})
	}

	b, _ := json.Marshal(sc) //nolint:errchkjson // strings, ints, and a finite float always marshal

	return string(b)
}

// durationJSON formats a positive duration in the protobuf JSON form, for
// example "0.1s" or "2s".
func durationJSON(d time.Duration) string {
	secs := int64(d / time.Second)
	nanos := int64(d % time.Second)

	if nanos == 0 {
		return fmt.Sprintf("%ds", secs)
	}

	frac := strings.TrimRight(fmt.Sprintf("%09d", nanos), "0")

	return fmt.Sprintf("%d.%ss", secs, frac)
}
