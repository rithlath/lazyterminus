//go:build unix

package runs

import (
	"os/exec"
	"syscall"
)

// detach starts the command in its own session, so a hangup or Ctrl+C
// aimed at lazyterminus's terminal doesn't reach it.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
