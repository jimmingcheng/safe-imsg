//go:build linux

package contacts

import (
	"context"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func executeDesktopHelper(context.Context, string, any, any) error { return Unsupported }

// Linux uses the same transport in synthetic tests only.
func sameOwnerPeer(conn *net.UnixConn) bool {
	raw, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	valid := false
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		valid = err == nil && cred.Uid == uint32(os.Geteuid())
	})
	return err == nil && valid
}
