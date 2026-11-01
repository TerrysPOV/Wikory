//go:build windows

package store

import "os"

const killedExitCode = 77

func killSelf() { os.Exit(77) }
