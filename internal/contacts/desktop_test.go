package contacts

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jimmingcheng/safe-imsg/internal/rpc"
)

func TestDesktopExchangeBoundsCancellationAndCleanup(t *testing.T) {
	for _, mode := range []string{"success", "denied", "malformed", "overflow", "trailing", "timeout", "launch-failed", "never-connect"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			var socket string
			var peer sync.WaitGroup
			var result Snapshot
			err := exchangeDesktop(ctx, map[string]any{"v": 1, "operation": "discover"}, &result, func(ctx context.Context, path string) error {
				socket = path
				info, err := os.Stat(filepath.Dir(path))
				if err != nil || info.Mode().Perm() != 0700 {
					t.Fatal("transport directory is not owner-private")
				}
				if mode == "launch-failed" {
					return errors.New("PRIVATE LAUNCH ERROR")
				}
				if mode == "never-connect" {
					return nil
				}
				conn, err := net.Dial("unix", path)
				if err != nil {
					return err
				}
				peer.Add(1)
				go func() {
					defer peer.Done()
					defer conn.Close()
					if _, err := rpc.ReadFrame(conn, 64<<10); err != nil {
						t.Error(err)
						return
					}
					switch mode {
					case "timeout":
						_, _ = io.Copy(io.Discard, conn) // must unblock on owner cancel
						return
					case "overflow":
						var header [4]byte
						binary.BigEndian.PutUint32(header[:], maxOutput+1)
						_, _ = conn.Write(header[:])
						return
					case "denied":
						_ = rpc.WriteFrame(conn, []byte(`{"v":1,"ok":false,"code":"contacts_permission_denied"}`))
						return
					case "malformed":
						_ = rpc.WriteFrame(conn, []byte(`PRIVATE OUTPUT`))
						return
					}
					_ = rpc.WriteFrame(conn, []byte(`{"v":1,"ok":true,"result":{"container_id":"icloud","group_ids":[],"contacts":[],"complete":true}}`))
					if mode == "trailing" {
						_, _ = conn.Write([]byte("PRIVATE TRAILING DATA"))
					}
				}()
				return nil
			})
			peer.Wait()
			want := error(Invalid)
			switch mode {
			case "success":
				want = nil
			case "denied":
				want = PermissionDenied
			case "timeout", "never-connect":
				want = Timeout
			case "launch-failed":
				want = Unavailable
			}
			if err != want {
				t.Fatalf("got %v, want %v", err, want)
			}
			if _, err := os.Stat(filepath.Dir(socket)); !os.IsNotExist(err) {
				t.Fatal("private socket directory was not cleaned up")
			}
		})
	}
}
