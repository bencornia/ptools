//go:build unix

package compile

import (
	"os/exec"
	"syscall"
	"time"
)

// stopTree makes canceling cmd kill the compiler's whole process group, so
// child processes it started, such as a .NET build, don't keep running or
// hold its output open.
func stopTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		group := -cmd.Process.Pid
		err := syscall.Kill(group, syscall.SIGKILL)
		// On macOS, a child forked as the group is killed can miss the
		// signal, so kill the group again until no one is left in it.
		go func() {
			for range 40 {
				time.Sleep(25 * time.Millisecond)
				if syscall.Kill(group, syscall.SIGKILL) != nil {
					return
				}
			}
		}()
		return err
	}
}
