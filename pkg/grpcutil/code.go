package grpcutil

import (
	"net/http"

	"google.golang.org/grpc/codes"
)

// statusClientClosedRequest is the non-standard HTTP status used for a request
// canceled by the client, as documented in google.rpc.Code.
const statusClientClosedRequest = 499

// httpStatusByCode is the google.rpc.Code HTTP mapping, indexed by gRPC code.
//
//nolint:gochecknoglobals // immutable lookup table
var httpStatusByCode = [...]int{
	codes.OK:                 http.StatusOK,
	codes.Canceled:           statusClientClosedRequest,
	codes.Unknown:            http.StatusInternalServerError,
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.DeadlineExceeded:   http.StatusGatewayTimeout,
	codes.NotFound:           http.StatusNotFound,
	codes.AlreadyExists:      http.StatusConflict,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.ResourceExhausted:  http.StatusTooManyRequests,
	codes.FailedPrecondition: http.StatusBadRequest,
	codes.Aborted:            http.StatusConflict,
	codes.OutOfRange:         http.StatusBadRequest,
	codes.Unimplemented:      http.StatusNotImplemented,
	codes.Internal:           http.StatusInternalServerError,
	codes.Unavailable:        http.StatusServiceUnavailable,
	codes.DataLoss:           http.StatusInternalServerError,
	codes.Unauthenticated:    http.StatusUnauthorized,
}

// codeByHTTPStatus maps the HTTP statuses that have a specific gRPC
// counterpart.
//
//nolint:gochecknoglobals // immutable lookup table
var codeByHTTPStatus = map[int]codes.Code{
	http.StatusBadRequest:                   codes.InvalidArgument,
	http.StatusUnauthorized:                 codes.Unauthenticated,
	http.StatusForbidden:                    codes.PermissionDenied,
	http.StatusNotFound:                     codes.NotFound,
	http.StatusMethodNotAllowed:             codes.Unimplemented,
	http.StatusRequestTimeout:               codes.DeadlineExceeded,
	http.StatusConflict:                     codes.AlreadyExists,
	http.StatusPreconditionFailed:           codes.FailedPrecondition,
	http.StatusRequestEntityTooLarge:        codes.ResourceExhausted,
	http.StatusRequestedRangeNotSatisfiable: codes.OutOfRange,
	http.StatusUnprocessableEntity:          codes.InvalidArgument,
	http.StatusTooManyRequests:              codes.ResourceExhausted,
	statusClientClosedRequest:               codes.Canceled,
	http.StatusInternalServerError:          codes.Internal,
	http.StatusNotImplemented:               codes.Unimplemented,
	http.StatusBadGateway:                   codes.Unavailable,
	http.StatusServiceUnavailable:           codes.Unavailable,
	http.StatusGatewayTimeout:               codes.DeadlineExceeded,
}

// HTTPStatusFromCode returns the HTTP status documented for code in
// google.rpc.Code (for example NotFound to 404, Unavailable to 503, and
// Canceled to the non-standard 499). Codes outside the defined range map to
// 500.
func HTTPStatusFromCode(code codes.Code) int {
	if int(code) >= len(httpStatusByCode) {
		return http.StatusInternalServerError
	}

	return httpStatusByCode[code]
}

// CodeFromHTTPStatus returns the gRPC code closest to an HTTP status produced
// by application logic.
//
// Statuses with a specific counterpart map to it (for example 404 to NotFound,
// 408 to DeadlineExceeded, 409 to AlreadyExists, 422 to InvalidArgument, 503
// to Unavailable). Any other 2xx maps to OK, 4xx to InvalidArgument, 5xx to
// Internal, and everything else to Unknown.
//
// This is not the transport-level mapping the gRPC protocol applies to HTTP
// responses that carry no grpc-status (where, for example, 404 means
// Unimplemented).
func CodeFromHTTPStatus(status int) codes.Code {
	if code, ok := codeByHTTPStatus[status]; ok {
		return code
	}

	switch {
	case status >= 200 && status < 300:
		return codes.OK
	case status >= 400 && status < 500:
		return codes.InvalidArgument
	case status >= 500 && status < 600:
		return codes.Internal
	default:
		return codes.Unknown
	}
}
