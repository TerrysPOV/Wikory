//go:build windows

package app

import "os"

const killedExitCode = 77

func killSelf() { os.Exit(77) }
