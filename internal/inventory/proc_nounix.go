//go:build !unix

package inventory

// osProcAlive cannot probe processes here, so every PID counts as alive: not
// knowing never frees a copy.
func osProcAlive(pid int) bool { return true }
