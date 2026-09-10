//go:build !linux && !darwin

package backend

import "os/exec"

func configureProcess(_ *exec.Cmd) {}
func killedProcess(_ error) bool   { return false }
