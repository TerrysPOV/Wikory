//go:build unix

package app

import (
	"os"
	"syscall"
)

const killedExitCode = -1

func killSelf() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL); select {} }
