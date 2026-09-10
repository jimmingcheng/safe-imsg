package securefile

import (
	"fmt"
	"os"
)

// CheckOwnerFile rejects symlinks, non-regular files, foreign ownership and
// files writable by group/other. Root-owned executables are allowed.
func CheckOwnerFile(path string, executable bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe file %s: expected a regular non-symlink file", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("unsafe file %s: group/other writable", path)
	}
	uid, ok := ownerUID(info)
	if !ok || (uid != uint32(os.Geteuid()) && uid != 0) {
		return fmt.Errorf("unsafe file %s: not owned by broker user or root", path)
	}
	if executable && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("unsafe file %s: backend is not executable", path)
	}
	return nil
}
