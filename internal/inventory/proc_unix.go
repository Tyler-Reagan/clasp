//go:build unix

package inventory

import (
	"errors"
	"syscall"
)

// osProcAlive sends signal 0: success, or EPERM (alive, owned by someone
// else), means the process exists.
func osProcAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
