//go:build darwin

package contacts

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

func executeDesktopHelper(ctx context.Context, path string, request, result any) error {
	bundle, err := helperBundle(path)
	if err != nil {
		return err
	}
	return exchangeDesktop(ctx, request, result, func(ctx context.Context, socket string) error {
		cmd := exec.CommandContext(ctx, "/usr/bin/open", "-n", "-g", "-a", bundle, "--args", "--connect", socket)
		configureProcess(cmd)
		cmd.WaitDelay = time.Second
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		return cmd.Run()
	})
}

func sameOwnerPeer(conn *net.UnixConn) bool {
	raw, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	valid := false
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		valid = err == nil && cred.Uid == uint32(os.Geteuid())
	})
	return err == nil && valid
}
