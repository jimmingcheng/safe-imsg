//go:build linux

package broker

import (
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var inner error
	if err := raw.Control(func(fd uintptr) {
		cred, getErr := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if getErr != nil {
			inner = getErr
			return
		}
		uid = cred.Uid
	}); err != nil {
		return 0, err
	}
	if inner != nil {
		return 0, fmt.Errorf("peer credentials: %w", inner)
	}
	return uid, nil
}

func fileOwnerUID(info os.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}
