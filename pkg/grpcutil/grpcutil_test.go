package grpcutil

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc/metadata"
)

func TestValidTraceIDMetadataKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want bool
	}{
		{key: DefaultTraceIDMetadataKey, want: true},
		{key: traceid.DefaultHeader, want: true},
		{key: "x-trace_id.v1", want: true},
		{key: "0", want: true},
		{key: "", want: false},
		{key: "x request id", want: false},
		{key: ":authority", want: false},
		{key: "x-id\r\n", want: false},
		{key: "grpc-trace", want: false},
		{key: "GRPC-Trace", want: false},
		{key: "x-request-id-bin", want: false},
		{key: "content-type", want: false},
		{key: "User-Agent", want: false},
		{key: "te", want: false},
		{key: "Host", want: false},
		{key: "connection", want: false},
		{key: "keep-alive", want: false},
		{key: "proxy-connection", want: false},
		{key: "Transfer-Encoding", want: false},
		{key: "upgrade", want: false},
		{key: "x-connection", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, ValidTraceIDMetadataKey(tt.key))
		})
	}
}

func TestTraceIDFromIncomingContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  func(ctx context.Context) context.Context
		key  string
		want string
	}{
		{
			name: "no incoming metadata",
			ctx:  func(ctx context.Context) context.Context { return ctx },
			key:  DefaultTraceIDMetadataKey,
			want: "default",
		},
		{
			name: "key absent",
			ctx: func(ctx context.Context) context.Context {
				return metadata.NewIncomingContext(ctx, metadata.Pairs("other", "value"))
			},
			key:  DefaultTraceIDMetadataKey,
			want: "default",
		},
		{
			name: "valid value",
			ctx: func(ctx context.Context) context.Context {
				return metadata.NewIncomingContext(ctx, metadata.Pairs(DefaultTraceIDMetadataKey, "abc-123"))
			},
			key:  DefaultTraceIDMetadataKey,
			want: "abc-123",
		},
		{
			name: "key lookup is case-insensitive",
			ctx: func(ctx context.Context) context.Context {
				return metadata.NewIncomingContext(ctx, metadata.Pairs(DefaultTraceIDMetadataKey, "abc-123"))
			},
			key:  traceid.DefaultHeader,
			want: "abc-123",
		},
		{
			name: "first of multiple values",
			ctx: func(ctx context.Context) context.Context {
				return metadata.NewIncomingContext(ctx, metadata.Pairs(
					DefaultTraceIDMetadataKey, "first",
					DefaultTraceIDMetadataKey, "second",
				))
			},
			key:  DefaultTraceIDMetadataKey,
			want: "first",
		},
		{
			name: "invalid characters",
			ctx: func(ctx context.Context) context.Context {
				return metadata.NewIncomingContext(ctx, metadata.Pairs(DefaultTraceIDMetadataKey, "bad id\r\n"))
			},
			key:  DefaultTraceIDMetadataKey,
			want: "default",
		},
		{
			name: "too long",
			ctx: func(ctx context.Context) context.Context {
				return metadata.NewIncomingContext(ctx, metadata.Pairs(
					DefaultTraceIDMetadataKey, strings.Repeat("a", traceid.MaxIDLen+1),
				))
			},
			key:  DefaultTraceIDMetadataKey,
			want: "default",
		},
		{
			name: "reserved key",
			ctx: func(ctx context.Context) context.Context {
				return metadata.NewIncomingContext(ctx, metadata.Pairs("user-agent", "abc-123"))
			},
			key:  "user-agent",
			want: "default",
		},
		{
			name: "invalid key",
			ctx: func(ctx context.Context) context.Context {
				return metadata.NewIncomingContext(ctx, metadata.Pairs("x id", "abc-123"))
			},
			key:  "x id",
			want: "default",
		},
		{
			name: "empty value",
			ctx: func(ctx context.Context) context.Context {
				return metadata.NewIncomingContext(ctx, metadata.Pairs(DefaultTraceIDMetadataKey, ""))
			},
			key:  DefaultTraceIDMetadataKey,
			want: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := TraceIDFromIncomingContext(tt.ctx(t.Context()), tt.key, "default")
			require.Equal(t, tt.want, got)
		})
	}
}

func TestOutgoingContextWithTraceID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		ctx          func(ctx context.Context) context.Context
		defaultValue string
		wantID       string
		wantMD       metadata.MD
	}{
		{
			name:         "id from context",
			ctx:          func(ctx context.Context) context.Context { return traceid.NewContext(ctx, "abc-123") },
			defaultValue: "",
			wantID:       "abc-123",
			wantMD:       metadata.Pairs(DefaultTraceIDMetadataKey, "abc-123"),
		},
		{
			name:         "default when context has no id",
			ctx:          func(ctx context.Context) context.Context { return ctx },
			defaultValue: "fallback",
			wantID:       "fallback",
			wantMD:       metadata.Pairs(DefaultTraceIDMetadataKey, "fallback"),
		},
		{
			name:         "default replaces invalid id",
			ctx:          func(ctx context.Context) context.Context { return traceid.NewContext(ctx, "bad id") },
			defaultValue: "fallback",
			wantID:       "fallback",
			wantMD:       metadata.Pairs(DefaultTraceIDMetadataKey, "fallback"),
		},
		{
			name:         "invalid id and invalid default",
			ctx:          func(ctx context.Context) context.Context { return traceid.NewContext(ctx, "bad id") },
			defaultValue: "bad default",
			wantID:       "",
			wantMD:       nil,
		},
		{
			name:         "no id and empty default",
			ctx:          func(ctx context.Context) context.Context { return ctx },
			defaultValue: "",
			wantID:       "",
			wantMD:       nil,
		},
		{
			name: "existing metadata is preserved and key replaced",
			ctx: func(ctx context.Context) context.Context {
				ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
					"other", "value",
					DefaultTraceIDMetadataKey, "old",
				))
				ctx = metadata.AppendToOutgoingContext(ctx, "added", "pair")

				return traceid.NewContext(ctx, "abc-123")
			},
			defaultValue: "",
			wantID:       "abc-123",
			wantMD: metadata.Pairs(
				"other", "value",
				"added", "pair",
				DefaultTraceIDMetadataKey, "abc-123",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := tt.ctx(t.Context())

			got, id := OutgoingContextWithTraceID(ctx, DefaultTraceIDMetadataKey, tt.defaultValue)
			require.Equal(t, tt.wantID, id)

			md, ok := metadata.FromOutgoingContext(got)
			if tt.wantMD == nil {
				require.Equal(t, ctx, got, "context must be returned unchanged")

				return
			}

			require.True(t, ok)
			require.Equal(t, tt.wantMD, md)
		})
	}
}

func TestOutgoingContextWithTraceID_invalidKey(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"", "x id", "grpc-trace", "x-request-id-bin", "user-agent"} {
		ctx := traceid.NewContext(t.Context(), "abc-123")

		got, id := OutgoingContextWithTraceID(ctx, key, "fallback")
		require.Empty(t, id, key)
		require.Equal(t, ctx, got, key)
	}
}

func TestOutgoingContextWithTraceID_doesNotMutateParent(t *testing.T) {
	t.Parallel()

	parentMD := metadata.Pairs(DefaultTraceIDMetadataKey, "old")
	parent := traceid.NewContext(metadata.NewOutgoingContext(t.Context(), parentMD), "abc-123")

	_, id := OutgoingContextWithTraceID(parent, DefaultTraceIDMetadataKey, "")
	require.Equal(t, "abc-123", id)

	md, ok := metadata.FromOutgoingContext(parent)
	require.True(t, ok)
	require.Equal(t, []string{"old"}, md.Get(DefaultTraceIDMetadataKey))
	require.Equal(t, []string{"old"}, parentMD.Get(DefaultTraceIDMetadataKey))
}
