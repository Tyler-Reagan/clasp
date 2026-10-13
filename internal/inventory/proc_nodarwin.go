//go:build !darwin

package inventory

import "time"

// osProcStartTime is unreadable off macOS; callers then treat a live PID as
// in use.
func osProcStartTime(pid int) (time.Time, bool) { return time.Time{}, false }
