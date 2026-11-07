package grpcclient

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/random"
	"github.com/tecnickcom/nurago/pkg/redact"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/stats"
)

// defaultTimeout is the default deadline of a unary RPC whose context has none.
const defaultTimeout = time.Minute

// defaultMaxDumpSize is the default cap, in encoded message bytes, on unary
// messages dumped into debug-level log entries.
const defaultMaxDumpSize = 1 << 20 // 1 MiB

// RedactFn redacts sensitive values from logged byte payloads and returns a redacted string.
type RedactFn = grpcutil.RedactFn

// config contains the configuration for the gRPC client.
type config struct {
	component          string
	logPrefix          string
	traceIDMetadataKey string
	timeout            time.Duration
	redactFn           RedactFn
	maxDumpSize        int
	logger             *slog.Logger
	rnd                *random.Rnd
	tlsConfig          *tls.Config
	insecure           bool
	credsBundle        credentials.Bundle
	retryPolicies      []retryPolicy
	maxCallAttempts    int
	keepaliveParams    *keepalive.ClientParameters
	maxRecvMsgSize     int
	maxSendMsgSize     int
	perRPCCredentials  []credentials.PerRPCCredentials
	statsHandlers      []stats.Handler
	unaryInterceptors  []grpc.UnaryClientInterceptor
	streamInterceptors []grpc.StreamClientInterceptor
	extraDialOptions   []grpc.DialOption
}

// defaultConfig returns the default configuration for the gRPC client.
func defaultConfig() *config {
	return &config{
		component:          "-",
		traceIDMetadataKey: grpcutil.DefaultTraceIDMetadataKey,
		timeout:            defaultTimeout,
		redactFn:           redact.Default().BytesToString,
		maxDumpSize:        defaultMaxDumpSize,
		logger:             slog.Default(),
		rnd:                random.New(nil),
	}
}

// dialOptions returns the grpc.DialOption list: transport credentials, the
// default interceptor chain, the retry service config, call and transport
// settings, and any options supplied with WithDialOption.
func (c *config) dialOptions() ([]grpc.DialOption, error) {
	err := c.validate()
	if err != nil {
		return nil, err
	}

	unary := []grpc.UnaryClientInterceptor{c.unaryTraceInterceptor, c.unaryLogInterceptor}
	if c.timeout > 0 {
		unary = append(unary, c.unaryDeadlineInterceptor)
	}

	unary = append(unary, c.unaryInterceptors...)

	stream := append([]grpc.StreamClientInterceptor{
		c.streamTraceInterceptor,
		c.streamLogInterceptor,
	}, c.streamInterceptors...)

	opts := []grpc.DialOption{
		c.securityOption(),
		grpc.WithChainUnaryInterceptor(unary...),
		grpc.WithChainStreamInterceptor(stream...),
	}

	if len(c.retryPolicies) > 0 {
		opts = append(opts, grpc.WithDefaultServiceConfig(serviceConfigJSON(c.retryPolicies)))
	}

	if c.maxCallAttempts > 0 {
		opts = append(opts, grpc.WithMaxCallAttempts(c.maxCallAttempts))
	}

	opts = append(opts, c.transportOptions()...)

	return append(opts, c.extraDialOptions...), nil
}

// validate rejects combined transport security options and retry policies
// whose MaxAttempts exceeds the call attempt cap.
func (c *config) validate() error {
	if (c.insecure && c.tlsConfig != nil) || (c.credsBundle != nil && (c.insecure || c.tlsConfig != nil)) {
		return ErrConflictingTransportSecurity
	}

	attemptsCap := c.maxCallAttempts
	if attemptsCap == 0 {
		attemptsCap = defaultMaxCallAttempts
	}

	for _, p := range c.retryPolicies {
		if p.MaxAttempts > attemptsCap {
			return fmt.Errorf("%w: MaxAttempts %d exceeds the call attempt cap of %d",
				ErrInvalidRetryPolicy, p.MaxAttempts, attemptsCap)
		}
	}

	return nil
}

// securityOption returns the credentials bundle set with
// WithCredentialsBundle, the insecure credentials when WithInsecure is set,
// and TLS credentials otherwise. The default TLS configuration uses the system
// root certificates.
func (c *config) securityOption() grpc.DialOption {
	if c.credsBundle != nil {
		return grpc.WithCredentialsBundle(c.credsBundle)
	}

	if c.insecure {
		return grpc.WithTransportCredentials(insecure.NewCredentials())
	}

	tlsConfig := c.tlsConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	return grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))
}

// transportOptions returns the message size, keepalive, stats handler, and
// per-RPC credentials options.
func (c *config) transportOptions() []grpc.DialOption {
	var opts []grpc.DialOption

	var callOpts []grpc.CallOption

	if c.maxRecvMsgSize > 0 {
		callOpts = append(callOpts, grpc.MaxCallRecvMsgSize(c.maxRecvMsgSize))
	}

	if c.maxSendMsgSize > 0 {
		callOpts = append(callOpts, grpc.MaxCallSendMsgSize(c.maxSendMsgSize))
	}

	if len(callOpts) > 0 {
		opts = append(opts, grpc.WithDefaultCallOptions(callOpts...))
	}

	if c.keepaliveParams != nil {
		opts = append(opts, grpc.WithKeepaliveParams(*c.keepaliveParams))
	}

	for _, h := range c.statsHandlers {
		opts = append(opts, grpc.WithStatsHandler(h))
	}

	for _, pc := range c.perRPCCredentials {
		opts = append(opts, grpc.WithPerRPCCredentials(pc))
	}

	return opts
}

// addRetryPolicy validates p and adds it, rejecting a scope already covered
// by another policy.
func (c *config) addRetryPolicy(p RetryPolicy) error {
	names, err := p.validate()
	if err != nil {
		return err
	}

	for i, n := range names {
		if slices.Contains(names[:i], n) || slices.ContainsFunc(c.retryPolicies, func(rp retryPolicy) bool {
			return slices.Contains(rp.names, n)
		}) {
			return fmt.Errorf("%w: duplicate scope %q", ErrInvalidRetryPolicy, scopeString(n))
		}
	}

	p.Services = nil
	p.Methods = nil
	p.RetryableCodes = slices.Clone(p.RetryableCodes)

	c.retryPolicies = append(c.retryPolicies, retryPolicy{RetryPolicy: p, names: names})

	return nil
}

// scopeString returns the service config path of n: "" for the default
// policy, "/package.Service/" for a service, and "/package.Service/Method" for
// a method.
func scopeString(n scName) string {
	if n.Service == "" {
		return ""
	}

	return "/" + n.Service + "/" + n.Method
}
