package grpcutil

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

func TestHTTPStatusFromCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code codes.Code
		want int
	}{
		{code: codes.OK, want: http.StatusOK},
		{code: codes.Canceled, want: 499},
		{code: codes.Unknown, want: http.StatusInternalServerError},
		{code: codes.InvalidArgument, want: http.StatusBadRequest},
		{code: codes.DeadlineExceeded, want: http.StatusGatewayTimeout},
		{code: codes.NotFound, want: http.StatusNotFound},
		{code: codes.AlreadyExists, want: http.StatusConflict},
		{code: codes.PermissionDenied, want: http.StatusForbidden},
		{code: codes.ResourceExhausted, want: http.StatusTooManyRequests},
		{code: codes.FailedPrecondition, want: http.StatusBadRequest},
		{code: codes.Aborted, want: http.StatusConflict},
		{code: codes.OutOfRange, want: http.StatusBadRequest},
		{code: codes.Unimplemented, want: http.StatusNotImplemented},
		{code: codes.Internal, want: http.StatusInternalServerError},
		{code: codes.Unavailable, want: http.StatusServiceUnavailable},
		{code: codes.DataLoss, want: http.StatusInternalServerError},
		{code: codes.Unauthenticated, want: http.StatusUnauthorized},
		{code: codes.Code(17), want: http.StatusInternalServerError},
		{code: codes.Code(1 << 31), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, HTTPStatusFromCode(tt.code))
		})
	}
}

func TestCodeFromHTTPStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   codes.Code
	}{
		{status: http.StatusOK, want: codes.OK},
		{status: http.StatusCreated, want: codes.OK},
		{status: http.StatusNoContent, want: codes.OK},
		{status: http.StatusBadRequest, want: codes.InvalidArgument},
		{status: http.StatusUnauthorized, want: codes.Unauthenticated},
		{status: http.StatusForbidden, want: codes.PermissionDenied},
		{status: http.StatusNotFound, want: codes.NotFound},
		{status: http.StatusMethodNotAllowed, want: codes.Unimplemented},
		{status: http.StatusRequestTimeout, want: codes.DeadlineExceeded},
		{status: http.StatusConflict, want: codes.AlreadyExists},
		{status: http.StatusPreconditionFailed, want: codes.FailedPrecondition},
		{status: http.StatusRequestEntityTooLarge, want: codes.ResourceExhausted},
		{status: http.StatusRequestedRangeNotSatisfiable, want: codes.OutOfRange},
		{status: http.StatusUnprocessableEntity, want: codes.InvalidArgument},
		{status: http.StatusTooManyRequests, want: codes.ResourceExhausted},
		{status: 499, want: codes.Canceled},
		{status: http.StatusTeapot, want: codes.InvalidArgument},
		{status: http.StatusInternalServerError, want: codes.Internal},
		{status: http.StatusNotImplemented, want: codes.Unimplemented},
		{status: http.StatusBadGateway, want: codes.Unavailable},
		{status: http.StatusServiceUnavailable, want: codes.Unavailable},
		{status: http.StatusGatewayTimeout, want: codes.DeadlineExceeded},
		{status: http.StatusInsufficientStorage, want: codes.Internal},
		{status: http.StatusContinue, want: codes.Unknown},
		{status: http.StatusMovedPermanently, want: codes.Unknown},
		{status: 0, want: codes.Unknown},
		{status: 600, want: codes.Unknown},
		{status: -1, want: codes.Unknown},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, CodeFromHTTPStatus(tt.status))
		})
	}
}

func TestCodeFromHTTPStatus_roundTrip(t *testing.T) {
	t.Parallel()

	// Every code whose HTTP status is unique to it must map back to itself.
	for _, code := range []codes.Code{
		codes.OK,
		codes.Canceled,
		codes.NotFound,
		codes.PermissionDenied,
		codes.Unimplemented,
		codes.Unavailable,
		codes.Unauthenticated,
	} {
		require.Equal(t, code, CodeFromHTTPStatus(HTTPStatusFromCode(code)), code.String())
	}
}
