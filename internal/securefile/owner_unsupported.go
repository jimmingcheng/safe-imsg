//go:build !linux && !darwin

package securefile

import "os"

func ownerUID(_ os.FileInfo) (uint32, bool) { return 0, false }
