package grpcclient

import (
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/status"
)

// validRetryPolicy returns a valid default retry policy.
func validRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        time.Second,
		BackoffMultiplier: 2,
		RetryableCodes:    []codes.Code{codes.Unavailable},
	}
}

func TestServiceConfigJSON_golden(t *testing.T) {
	t.Parallel()

	reads := validRetryPolicy()
	reads.Services = []string{"items.v1.ItemService"}
	reads.Methods = []string{"/other.v1.Other/Get"}
	reads.MaxAttempts = 4
	reads.InitialBackoff = 1500 * time.Microsecond
	reads.MaxBackoff = 2 * time.Second
	reads.BackoffMultiplier = 1.5
	reads.RetryableCodes = []codes.Code{codes.Unavailable, codes.Canceled, codes.DeadlineExceeded}

	cfg := defaultConfig()
	require.NoError(t, cfg.addRetryPolicy(validRetryPolicy()))
	require.NoError(t, cfg.addRetryPolicy(reads))

	got := serviceConfigJSON(cfg.retryPolicies)

	want := `{"methodConfig":[` +
		`{"name":[{}],"retryPolicy":{"maxAttempts":3,"initialBackoff":"0.1s","maxBackoff":"1s",` +
		`"backoffMultiplier":2,"retryableStatusCodes":["UNAVAILABLE"]}},` +
		`{"name":[{"service":"items.v1.ItemService"},{"service":"other.v1.Other","method":"Get"}],` +
		`"retryPolicy":{"maxAttempts":4,"initialBackoff":"0.0015s","maxBackoff":"2s",` +
		`"backoffMultiplier":1.5,"retryableStatusCodes":["UNAVAILABLE","CANCELLED","DEADLINE_EXCEEDED"]}}]}` //nolint:misspell // google.rpc.Code name

	require.Equal(t, want, got)
}

func TestRetryPolicy_validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(p *RetryPolicy)
	}{
		{name: "max attempts", mutate: func(p *RetryPolicy) { p.MaxAttempts = 1 }},
		{name: "initial backoff", mutate: func(p *RetryPolicy) { p.InitialBackoff = 0 }},
		{name: "max backoff", mutate: func(p *RetryPolicy) { p.MaxBackoff = -1 }},
		{name: "multiplier", mutate: func(p *RetryPolicy) { p.BackoffMultiplier = 0 }},
		{name: "NaN multiplier", mutate: func(p *RetryPolicy) { p.BackoffMultiplier = math.NaN() }},
		{name: "infinite multiplier", mutate: func(p *RetryPolicy) { p.BackoffMultiplier = math.Inf(1) }},
		{name: "empty codes", mutate: func(p *RetryPolicy) { p.RetryableCodes = nil }},
		{name: "OK code", mutate: func(p *RetryPolicy) { p.RetryableCodes = []codes.Code{codes.OK} }},
		{name: "unknown code", mutate: func(p *RetryPolicy) { p.RetryableCodes = []codes.Code{17} }},
		{name: "empty service", mutate: func(p *RetryPolicy) { p.Services = []string{""} }},
		{name: "service with slash", mutate: func(p *RetryPolicy) { p.Services = []string{"a/b"} }},
		{name: "method without slash", mutate: func(p *RetryPolicy) { p.Methods = []string{"pkg.Svc/M"} }},
		{name: "method without name", mutate: func(p *RetryPolicy) { p.Methods = []string{"/pkg.Svc/"} }},
		{name: "method without service", mutate: func(p *RetryPolicy) { p.Methods = []string{"//M"} }},
		{name: "method with extra slash", mutate: func(p *RetryPolicy) { p.Methods = []string{"/pkg.Svc/M/x"} }},
		{name: "service only method", mutate: func(p *RetryPolicy) { p.Methods = []string{"/pkg.Svc"} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := validRetryPolicy()
			tt.mutate(&p)

			_, err := p.validate()
			require.ErrorIs(t, err, ErrInvalidRetryPolicy)
		})
	}

	names, err := validRetryPolicy().validate()
	require.NoError(t, err)
	require.Equal(t, []scName{{}}, names)
}

func TestAddRetryPolicy_scopes(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.NoError(t, cfg.addRetryPolicy(validRetryPolicy()))
	require.ErrorIs(t, cfg.addRetryPolicy(validRetryPolicy()), ErrInvalidRetryPolicy, "second default policy")

	svc := validRetryPolicy()
	svc.Services = []string{"pkg.Svc"}
	require.NoError(t, cfg.addRetryPolicy(svc))
	require.ErrorIs(t, cfg.addRetryPolicy(svc), ErrInvalidRetryPolicy, "same service twice")

	method := validRetryPolicy()
	method.Methods = []string{"/pkg.Svc/M"}
	require.NoError(t, cfg.addRetryPolicy(method), "a method scope does not overlap its service scope")

	dup := validRetryPolicy()
	dup.Methods = []string{"/pkg.Svc/A", "/pkg.Svc/A"}
	require.ErrorIs(t, cfg.addRetryPolicy(dup), ErrInvalidRetryPolicy, "duplicate within one policy")

	// A rejected policy does not reserve its scope.
	dup.Methods = []string{"/pkg.Svc/A"}
	require.NoError(t, cfg.addRetryPolicy(dup))

	require.Len(t, cfg.retryPolicies, 4)
}

func TestAddRetryPolicy_copiesSlices(t *testing.T) {
	t.Parallel()

	p := validRetryPolicy()
	p.Services = []string{"pkg.Svc"}

	cfg := defaultConfig()
	require.NoError(t, cfg.addRetryPolicy(p))

	p.Services[0] = "changed"
	p.RetryableCodes[0] = codes.Internal

	require.Equal(t, []scName{{Service: "pkg.Svc"}}, cfg.retryPolicies[0].names)
	require.Equal(t, []codes.Code{codes.Unavailable}, cfg.retryPolicies[0].RetryableCodes)
}

func TestDurationJSON(t *testing.T) {
	t.Parallel()

	tests := map[time.Duration]string{
		time.Nanosecond:         "0.000000001s",
		time.Millisecond:        "0.001s",
		100 * time.Millisecond:  "0.1s",
		time.Second:             "1s",
		1500 * time.Millisecond: "1.5s",
		90 * time.Second:        "90s",
	}

	for d, want := range tests {
		require.Equal(t, want, durationJSON(d), d.String())
	}
}

func TestCodeNames(t *testing.T) {
	t.Parallel()

	require.Len(t, codeNames, int(codes.Unauthenticated)+1)

	for c, name := range codeNames {
		require.NotEmpty(t, name, codes.Code(c).String())

		var got codes.Code

		require.NoError(t, got.UnmarshalJSON([]byte(`"`+name+`"`)))
		require.Equal(t, codes.Code(c), got)
	}
}

func TestRetryPolicy_retries(t *testing.T) {
	t.Parallel()

	fastPolicy := func(maxAttempts int) RetryPolicy {
		return RetryPolicy{
			MaxAttempts:       maxAttempts,
			InitialBackoff:    time.Millisecond,
			MaxBackoff:        time.Millisecond,
			BackoffMultiplier: 1,
			RetryableCodes:    []codes.Code{codes.Unavailable},
		}
	}

	t.Run("retried until success", func(t *testing.T) {
		t.Parallel()

		svc := &testService{failFirst: 2, failCode: codes.Unavailable}
		client := newTestClient(t, svc, WithRetryPolicy(fastPolicy(3)))

		_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		require.EqualValues(t, 3, svc.calls.Load())
	})

	t.Run("more than 5 attempts", func(t *testing.T) {
		t.Parallel()

		svc := &testService{failFirst: 6, failCode: codes.Unavailable}
		client := newTestClient(t, svc, WithRetryPolicy(fastPolicy(7)), WithMaxCallAttempts(7))

		_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		require.EqualValues(t, 7, svc.calls.Load())
	})

	t.Run("attempts exhausted", func(t *testing.T) {
		t.Parallel()

		svc := &testService{failFirst: 10, failCode: codes.Unavailable}
		client := newTestClient(t, svc, WithRetryPolicy(fastPolicy(2)))

		_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.Equal(t, codes.Unavailable, status.Code(err))
		require.EqualValues(t, 2, svc.calls.Load())
	})

	t.Run("non retryable code", func(t *testing.T) {
		t.Parallel()

		svc := &testService{failFirst: 10, failCode: codes.Internal}
		client := newTestClient(t, svc, WithRetryPolicy(fastPolicy(3)))

		_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
		require.EqualValues(t, 1, svc.calls.Load())
	})

	t.Run("method scope", func(t *testing.T) {
		t.Parallel()

		p := fastPolicy(3)
		p.Methods = []string{methodUnaryCall}

		svc := &testService{failFirst: 1, failCode: codes.Unavailable}
		client := newTestClient(t, svc, WithRetryPolicy(p))

		_, err := client.EmptyCall(rpcContext(t), &testgrpc.Empty{})
		require.Equal(t, codes.Unavailable, status.Code(err), "out of scope: not retried")

		_, err = client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		require.EqualValues(t, 2, svc.calls.Load())
	})

	t.Run("logged once per call", func(t *testing.T) {
		t.Parallel()

		logger, sink := newTestLogger(slog.LevelInfo)
		svc := &testService{failFirst: 2, failCode: codes.Unavailable}
		client := newTestClient(t, svc, WithRetryPolicy(fastPolicy(3)), WithLogger(logger))

		_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.NoError(t, err)

		e := sink.waitEntry(t)
		require.Equal(t, codes.OK.String(), e["grpc_code"])
	})
}
