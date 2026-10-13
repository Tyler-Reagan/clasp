package inventory

import (
	"os/exec"
	"runtime"
	"strings"
)

// desktopExeSuffix ends the executable path of the Claude Desktop app. The
// Claude Code CLI bundled inside the app ends in "/MacOS/claude" or lives under
// claude-code/, so the exact case matters.
const desktopExeSuffix = ".app/Contents/MacOS/Claude"

// desktopRunning reports whether the Claude Desktop app is running; known is
// false when that could not be determined. Tests replace it.
var desktopRunning = osDesktopRunning

// DesktopRunning reports whether the Claude Desktop app is running. known is
// false off macOS or when the process list cannot be read.
func DesktopRunning() (running, known bool) { return desktopRunning() }

func osDesktopRunning() (running, known bool) {
	if runtime.GOOS != "darwin" {
		return false, false
	}
	out, err := exec.Command("/bin/ps", "-axo", "comm=").Output()
	if err != nil || len(out) == 0 {
		return false, false
	}
	return processListHasDesktop(string(out)), true
}

// processListHasDesktop scans `ps -axo comm=` output for the Desktop app.
func processListHasDesktop(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), desktopExeSuffix) {
			return true
		}
	}
	return false
}
