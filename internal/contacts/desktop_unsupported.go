//go:build !linux && !darwin

package contacts

import (
	"context"
	"net"
)

func executeDesktopHelper(context.Context, string, any, any) error { return Unsupported }
func sameOwnerPeer(*net.UnixConn) bool                             { return false }
