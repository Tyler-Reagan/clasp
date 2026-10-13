//go:build darwin

package inventory

import (
	"time"

	"golang.org/x/sys/unix"
)

// osProcStartTime reads the process start time from the kernel's proc table.
func osProcStartTime(pid int) (time.Time, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, false
	}
	st := kp.Proc.P_starttime
	if st.Sec == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(st.Sec), int64(st.Usec)*1000).UTC(), true
}
