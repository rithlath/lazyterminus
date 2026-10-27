// Package lazyterminus embeds the scrubbed Terminus fixtures that back the
// mock runner, so `lazyterminus --mock` works from an installed binary.
package lazyterminus

import (
	"embed"
	"io/fs"
)

//go:embed testdata/real testdata/mock
var fixtures embed.FS

// Fixtures returns the fixture tree rooted at testdata/ (real/ and mock/).
func Fixtures() fs.FS {
	sub, err := fs.Sub(fixtures, "testdata")
	if err != nil {
		panic(err)
	}
	return sub
}
