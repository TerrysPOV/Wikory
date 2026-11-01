//go:build windows

package publish

import "os"

const killedExitCode = 77

func killSelf() { os.Exit(77) }
