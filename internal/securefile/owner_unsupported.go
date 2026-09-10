//go:build !linux && !darwin

package securefile

import (
	"fmt"
	"os"
)

func ownerUID(_ os.FileInfo) (uint32, bool) { return 0, false }

func openNoFollow(_ string) (*os.File, error) {
	return nil, fmt.Errorf("secure file access unsupported")
}
