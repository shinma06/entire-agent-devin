//go:build !unix

package devin

import "os"

func lockFile(*os.File) (func(), error) { return func() {}, nil }
