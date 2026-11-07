package srvlifecycle

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsValidAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		want bool
	}{
		{addr: ":8017", want: true},
		{addr: ":0", want: true},
		{addr: ":65535", want: true},
		{addr: "localhost:8017", want: true},
		{addr: "0.0.0.0:8017", want: true},
		{addr: "[::1]:8017", want: true},
		{addr: "[::]:8017", want: true},
		{addr: "", want: false},
		{addr: "::", want: false},
		{addr: ":", want: false},
		{addr: ":aaa", want: false},
		{addr: ":-1", want: false},
		{addr: ":65536", want: false},
		{addr: "localhost", want: false},
		{addr: "::1:8017", want: false},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, IsValidAddr(tt.addr), tt.addr)
	}
}
