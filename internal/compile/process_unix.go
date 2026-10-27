//go:build unix

package compile

import (
	"os/exec"
	"syscall"
)

// stopTree makes canceling cmd kill the compiler's whole process group, so
// child processes it started, such as a .NET build, don't keep running or
// hold its output open.
func stopTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
