//go:build !linux

package ui

import "io"

// discardTypeahead is a no-op where TCFLSH isn't available.
func discardTypeahead(io.Reader) {}
