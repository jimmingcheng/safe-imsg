//go:build !linux && !darwin

package contacts

import "os/exec"

func configureProcess(_ *exec.Cmd) {}
