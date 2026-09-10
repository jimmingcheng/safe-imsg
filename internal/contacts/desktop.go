package contacts

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/jimmingcheng/safe-imsg/internal/rpc"
	"github.com/jimmingcheng/safe-imsg/internal/securefile"
)

// Launch Services preserves the approved app's TCC identity. A private,
// owner-authenticated socket carries one bounded request/response entirely in
// memory. The native app exits when this connection closes, including cancel.
func exchangeDesktop(ctx context.Context, request, result any, launch func(context.Context, string) error) error {
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	input, err := json.Marshal(request)
	if err != nil || len(input) > 64<<10 {
		return Invalid
	}
	// Avoid macOS's long per-user TMPDIR exceeding Unix socket path limits.
	dir, err := os.MkdirTemp("/tmp", "safe-imsg-contacts-")
	if err != nil {
		return Unavailable
	}
	// Only this freshly created private directory is removed; no data files.
	defer os.Remove(dir)
	socket := filepath.Join(dir, "rpc.sock")
	if securefile.CheckParents(socket) != nil {
		return UnsafeHelper
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return Unavailable
	}
	defer listener.Close()
	stopListener := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopListener()
	if os.Chmod(socket, 0600) != nil {
		return Unavailable
	}
	if err := launch(ctx, socket); err != nil {
		if ctx.Err() != nil {
			return Timeout
		}
		return Unavailable
	}
	conn, err := listener.AcceptUnix()
	if err != nil {
		if ctx.Err() != nil {
			return Timeout
		}
		return Unavailable
	}
	defer conn.Close()
	stopConn := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopConn()
	if !sameOwnerPeer(conn) {
		return UnsafeHelper
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := rpc.WriteFrame(conn, input); err != nil {
		return transportError(ctx)
	}
	output, err := rpc.ReadFrame(conn, maxOutput)
	if err != nil {
		return transportError(ctx)
	}
	// Require one complete response and process disconnect; reject trailing data.
	var extra [1]byte
	if n, err := conn.Read(extra[:]); n != 0 || err != io.EOF {
		return transportError(ctx)
	}
	return decodeHelperResponse(output, result)
}

func transportError(ctx context.Context) error {
	deadline, hasDeadline := ctx.Deadline()
	if ctx.Err() != nil || (hasDeadline && !time.Now().Before(deadline)) {
		return Timeout
	}
	return Invalid
}
