//go:build darwin

package contacts

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestNativeAppExitsWhenOwnerDisconnects(t *testing.T) {
	helper := os.Getenv("SAFE_IMSG_CONTACTS_APP_EXECUTABLE")
	if helper == "" {
		t.Skip("native app integration opt-in")
	}
	if err := CheckHelper(helper); err != nil {
		t.Fatal(err)
	}
	bundle, _ := helperBundle(helper)
	dir, err := os.MkdirTemp("/tmp", "safe-imsg-contacts-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dir)
	socket := filepath.Join(dir, "rpc.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.SetDeadline(time.Now().Add(10 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "/usr/bin/open", "-n", "-g", "-a", bundle, "--args", "--connect", socket).Run(); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if !sameOwnerPeer(conn) {
		t.Fatal("unexpected peer")
	}
	raw, _ := conn.SyscallConn()
	pid := 0
	_ = raw.Control(func(fd uintptr) { pid, err = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) })
	if err != nil || pid <= 1 || unix.Kill(pid, 0) != nil {
		t.Fatal("could not observe the helper instance")
	}
	// No request is sent: the app must exit even while blocked reading input.
	_ = conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if unix.Kill(pid, 0) == unix.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("native helper outlived its owner connection")
}
