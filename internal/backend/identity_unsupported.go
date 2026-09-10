//go:build !linux && !darwin

package backend

import "fmt"

func databaseIdentity(_ string) (string, error) {
	return "", fmt.Errorf("database identity is unsupported on this platform")
}
