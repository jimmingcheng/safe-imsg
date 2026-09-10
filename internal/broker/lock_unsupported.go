//go:build !linux && !darwin

package broker

import (
	"fmt"
	"os"
)

func acquireLock(_ string) (*os.File, error) { return nil, fmt.Errorf("broker locking is unsupported") }
