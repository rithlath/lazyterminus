//go:build !unix

package runs

import "os/exec"

func detach(*exec.Cmd) {}
