//go:build linux || darwin

package broker

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func acquireLock(path string) (*os.File, error) {
	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	fd, err := unix.Open(path, flags|unix.O_CREAT|unix.O_EXCL, 0o600)
	created := err == nil
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Open(path, flags, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("open broker lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	fail := func() (*os.File, error) {
		file.Close()
		return nil, fmt.Errorf("unsafe broker lock file")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Mode&0o077 != 0 {
		return fail()
	}
	if created {
		if err := file.Chmod(0o600); err != nil {
			return fail()
		}
	}
	opened, err := file.Stat()
	if err != nil {
		return fail()
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, pathInfo) {
		return fail()
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another broker owns the socket lock")
	}
	return file, nil
}
