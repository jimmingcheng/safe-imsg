package securefile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CheckOwnerFile rejects symlinks, non-regular files, foreign ownership and
// files writable by group/other. Root-owned executables are allowed.
func CheckOwnerFile(path string, executable bool) error {
	file, err := OpenOwnerFile(path, executable)
	if err != nil {
		return err
	}
	return file.Close()
}

// OpenOwnerFile validates the descriptor that will actually be read, avoiding
// a check-then-open race on policy replacement. Parent directories must also
// protect the pathname from replacement by the restricted client.
func OpenOwnerFile(path string, executable bool) (*os.File, error) {
	if err := CheckParents(path); err != nil {
		return nil, err
	}
	file, err := openNoFollow(path)
	if err != nil {
		return nil, fmt.Errorf("open trusted file: %w", err)
	}
	info, err := file.Stat()
	if err == nil {
		err = checkInfo(info, executable)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func checkInfo(info os.FileInfo, executable bool) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe file: expected a regular file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("unsafe file: group/other writable")
	}
	uid, ok := ownerUID(info)
	if !ok || (uid != uint32(os.Geteuid()) && uid != 0) {
		return fmt.Errorf("unsafe file: not owned by broker user or root")
	}
	if executable && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("unsafe file: backend is not executable")
	}
	return nil
}

func ReadOwnerFile(path string, maxBytes int64) ([]byte, error) {
	file, err := OpenOwnerFile(path, false)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("trusted file exceeds size limit")
	}
	return data, nil
}

// Trusted ancestor symlinks (such as macOS /var) are permitted only when both
// the link's containing directory and its resolved target path are protected.
func CheckParents(path string) error {
	if filepath.Clean(path) != path {
		return fmt.Errorf("trusted path must be clean")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return checkDirectory(filepath.Dir(abs))
}

func checkDirectory(dir string) error {
	// Walk from the root, expanding symlinks one at a time. EvalSymlinks would
	// hide intermediate target directories that a restricted user could change.
	separator := string(os.PathSeparator)
	resolved := separator
	pending := strings.Split(dir, separator)
	links := 0
	for len(pending) > 0 {
		path := filepath.Join(resolved, pending[0])
		pending = pending[1:]
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		uid, ok := ownerUID(info)
		if !ok || (uid != 0 && uid != uint32(os.Geteuid())) {
			return fmt.Errorf("unsafe parent directory: not owned by broker user or root")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 40 {
				return fmt.Errorf("unsafe parent directory: too many symlinks")
			}
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) {
				resolved = separator
			}
			pending = append(strings.Split(target, separator), pending...)
			continue
		}
		if !info.IsDir() || (info.Mode().Perm()&0o022 != 0 && !(uid == 0 && info.Mode()&os.ModeSticky != 0)) {
			return fmt.Errorf("unsafe parent directory: group/other writable")
		}
		resolved = path
	}
	return nil
}
