package ui

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// discardTypeahead drops keys typed while a command ran, so an Enter pressed
// too early doesn't skip the pause that follows.
func discardTypeahead(in io.Reader) {
	if f, ok := in.(*os.File); ok {
		_ = unix.IoctlSetInt(int(f.Fd()), unix.TCFLSH, unix.TCIFLUSH)
	}
}
