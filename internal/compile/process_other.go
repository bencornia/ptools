//go:build !unix

package compile

import "os/exec"

// stopTree leaves cancellation to exec's default of killing the compiler
// process alone; cmd.WaitDelay bounds the wait for any children.
func stopTree(cmd *exec.Cmd) {}
