//go:build linux || darwin

package broker

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLockRejectsLinksAndFIFO(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o400); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "symlink")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(dir, "hardlink")
	if err := os.Link(target, hardlink); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, hardlink, fifo} {
		if file, err := acquireLock(path); err == nil {
			file.Close()
			t.Errorf("unsafe lock accepted: %s", path)
		}
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Fatal("lock validation altered another file's permissions")
	}
}

func TestLockExclusion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := acquireLock(path); err == nil {
		second.Close()
		t.Fatal("two brokers acquired the same lock")
	}
}
