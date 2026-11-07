package srvlifecycle

import (
	"math"
	"net"
	"strconv"
)

// IsValidAddr reports whether addr is a valid TCP bind address. The host part
// may be empty, a hostname, an IPv4 or a bracketed IPv6 address (e.g. ":8017",
// "localhost:8017", "0.0.0.0:8017", "[::1]:8017"). Port 0 is valid: it asks
// the operating system for an ephemeral port.
func IsValidAddr(addr string) bool {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}

	portInt, err := strconv.Atoi(port)
	if err != nil {
		return false
	}

	return portInt >= 0 && portInt <= math.MaxUint16
}
