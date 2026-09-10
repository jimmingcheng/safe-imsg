//go:build linux || darwin

package backend

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func databaseIdentity(path string) (string, error) {
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
