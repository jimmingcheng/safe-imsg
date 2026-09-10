//go:build !linux && !darwin

package broker

import (
	"fmt"
	"net"
	"os"
)

func peerUID(_ *net.UnixConn) (uint32, error) {
	return 0, fmt.Errorf("peer credentials are unsupported")
}
func fileOwnerUID(_ os.FileInfo) (uint32, bool) { return 0, false }
